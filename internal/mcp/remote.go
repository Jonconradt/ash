package mcp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ash/internal/brokerproto"
	"ash/internal/workspace"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

const (
	toolNamePrefix           = "mcp_"
	authWait                 = 200 * time.Millisecond
	modernProtocolVersion    = "2026-07-28"
	protocolDiscoveryTimeout = 15 * time.Second
	legacyToolsTTL           = 5 * time.Minute
	toolsTimeout             = 15 * time.Second
)

var toolNameSanitizer = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// IsRemoteServerLine reports whether an allowlist line registers a remote HTTP MCP server.
func IsRemoteServerLine(line string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(line))
	return strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://")
}

// RemoteServersFromAllowlist extracts and validates MCP server URLs from allowlist lines.
func RemoteServersFromAllowlist(raw string) ([]RemoteServer, error) {
	var servers []RemoteServer
	seen := make(map[string]string)
	for lineIndex, line := range strings.Split(raw, "\n") {
		urlText := strings.TrimSpace(line)
		if !IsRemoteServerLine(urlText) {
			continue
		}
		parsed, err := url.Parse(urlText)
		if err != nil {
			return nil, fmt.Errorf("invalid MCP URL on .ash_allow line %d: %w", lineIndex+1, err)
		}
		parsed.Scheme = strings.ToLower(parsed.Scheme)
		server := RemoteServer{Name: remoteServerName(urlText), URL: parsed.String()}
		if err := validateRemoteServer(server); err != nil {
			return nil, fmt.Errorf("invalid MCP URL on .ash_allow line %d: %w", lineIndex+1, err)
		}
		if priorURL, ok := seen[server.Name]; ok {
			if priorURL == urlText {
				return nil, fmt.Errorf("duplicate MCP server URL on .ash_allow line %d", lineIndex+1)
			}
			return nil, fmt.Errorf("MCP server name hash collision on .ash_allow line %d", lineIndex+1)
		}
		seen[server.Name] = urlText
		servers = append(servers, server)
	}
	return servers, nil
}

// ResolveRemoteServers reads the first existing .ash_allow file using Ash's
// workspace, working-directory, then home-directory precedence.
func ResolveRemoteServers(home, cwd string, readFile func(string) ([]byte, error)) ([]RemoteServer, error) {
	if readFile == nil {
		readFile = os.ReadFile
	}
	for _, path := range []string{
		filepath.Join(workspace.Root(home), ".ash_allow"),
		filepath.Join(cwd, ".ash_allow"),
		filepath.Join(home, ".ash_allow"),
	} {
		content, err := readFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading MCP registrations from %s: %w", path, err)
		}
		return RemoteServersFromAllowlist(string(content))
	}
	return nil, nil
}

func remoteServerName(urlText string) string {
	hash := sha256.Sum256([]byte(strings.TrimSpace(urlText)))
	return hex.EncodeToString(hash[:4])
}

// ToolFunctionName returns the model-safe, server-qualified name of an MCP tool.
func ToolFunctionName(server, tool string) string {
	raw := server + "\x00" + tool
	name := strings.Trim(toolNameSanitizer.ReplaceAllString(server+"_"+tool, "_"), "_-")
	if name == "" {
		name = "tool"
	}
	const hashLength = 8
	const maxBaseLength = 64 - len(toolNamePrefix) - hashLength - 1
	if len(name) > maxBaseLength {
		name = name[:maxBaseLength]
	}
	hash := sha256.Sum256([]byte(raw))
	return toolNamePrefix + name + "_" + hex.EncodeToString(hash[:4])
}

// RemoteServer is a configured HTTP MCP server.
type RemoteServer struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// RemoteTool is a tool exposed by a remote MCP server.
type RemoteTool struct {
	Server      string         `json:"server"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// RemoteStatus describes the state of a broker-owned MCP session.
type RemoteStatus struct {
	State   string `json:"state"`
	AuthURL string `json:"auth_url,omitempty"`
	Error   string `json:"error,omitempty"`
}

type persistedOAuth struct {
	Config oauth2.Config `json:"config"`
	Token  oauth2.Token  `json:"token"`
}

type remoteSession struct {
	mu               sync.Mutex
	config           RemoteServer
	ctx              context.Context
	cancel           context.CancelFunc
	status           RemoteStatus
	client           *mcp.ClientSession
	authURL          chan string
	callback         chan authorizationCallback
	toolRefreshQueue chan struct{}
	authActive       atomic.Bool
	expectedState    string
	listener         net.Listener
	done             chan struct{}
	closeOnce        sync.Once
	closed           bool
	tools            []RemoteTool
	toolsExpiry      time.Time
	toolsValid       bool
	toolsGen         uint64
	refresh          *toolRefresh
	retryAt          time.Time
	retryDelay       time.Duration
	workers          sync.WaitGroup
}

type toolRefresh struct {
	done  chan struct{}
	tools []RemoteTool
	err   error
}

type oauthDialPolicy struct {
	targetAuthority string
	proxyAuthority  string
}

type oauthDialPolicyKey struct{}

type protocolNegotiationState struct {
	mu           sync.Mutex
	blockedError error
}

type protocolProbeRoundTripper struct {
	base  http.RoundTripper
	state *protocolNegotiationState
}

func (t *protocolProbeRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	method := ""
	if request.Method == http.MethodPost {
		method = jsonRPCRequestMethod(request)
	}
	if method == "initialize" {
		t.state.mu.Lock()
		err := t.state.blockedError
		t.state.mu.Unlock()
		if err != nil {
			return nil, err
		}
	}
	if method != "server/discover" {
		return t.base.RoundTrip(request)
	}
	t.state.mu.Lock()
	t.state.blockedError = nil
	t.state.mu.Unlock()
	response, err := t.base.RoundTrip(request)
	if err != nil {
		t.blockLegacyFallback(fmt.Errorf("newest MCP protocol discovery failed: %w", err))
		return nil, err
	}
	if response != nil {
		errorCode, validResponse, supportsModern := responseDiscoveryProtocol(response)
		legacyOnlyDiscovery := response.StatusCode >= http.StatusOK &&
			response.StatusCode < http.StatusMultipleChoices &&
			validResponse && errorCode == 0 && !supportsModern
		if legacyFallbackRequired(response.StatusCode, errorCode) || legacyOnlyDiscovery {
			return response, nil
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices ||
			!validResponse || errorCode != 0 {
			if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
				t.blockLegacyFallback(fmt.Errorf("newest MCP protocol discovery requires authorization (HTTP status %d)", response.StatusCode))
			} else {
				t.blockLegacyFallback(fmt.Errorf("newest MCP protocol discovery failed with HTTP status %d", response.StatusCode))
			}
		}
	} else {
		t.blockLegacyFallback(errors.New("newest MCP protocol discovery returned no HTTP response"))
	}
	return response, err
}

func (t *protocolProbeRoundTripper) blockLegacyFallback(err error) {
	t.state.mu.Lock()
	t.state.blockedError = err
	t.state.mu.Unlock()
}

func jsonRPCRequestMethod(request *http.Request) string {
	if request.GetBody == nil {
		return ""
	}
	body, err := request.GetBody()
	if err != nil {
		return ""
	}
	defer func() { _ = body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(body, 1<<20))
	if err != nil {
		return ""
	}
	var message struct {
		Method string `json:"method"`
	}
	if json.Unmarshal(payload, &message) != nil {
		return ""
	}
	return message.Method
}

func responseDiscoveryProtocol(response *http.Response) (int, bool, bool) {
	if response.Body == nil {
		return 0, false, false
	}
	var payload []byte
	var err error
	if strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
		payload, err = firstSSEData(response)
	} else {
		body := response.Body
		payload, err = readBoundedDiscoveryBody(body)
		response.Body = bufferedResponseBody(payload, body, body)
	}
	if err != nil || len(payload) > brokerproto.MaxBody {
		return 0, false, false
	}
	message, err := jsonrpc.DecodeMessage(payload)
	if err != nil {
		return 0, false, false
	}
	reply, ok := message.(*jsonrpc.Response)
	if !ok {
		return 0, false, false
	}
	if reply.Error != nil {
		return jsonRPCErrorCode(reply.Error), true, false
	}
	var discovery struct {
		SupportedVersions []string `json:"supportedVersions"`
	}
	if json.Unmarshal(reply.Result, &discovery) != nil {
		return 0, false, false
	}
	for _, version := range discovery.SupportedVersions {
		if version >= modernProtocolVersion {
			return 0, true, true
		}
	}
	return 0, true, false
}

func readBoundedDiscoveryBody(body io.ReadCloser) ([]byte, error) {
	timer := time.AfterFunc(protocolDiscoveryTimeout, func() { _ = body.Close() })
	payload, err := io.ReadAll(io.LimitReader(body, brokerproto.MaxBody+1))
	if !timer.Stop() {
		return payload, context.DeadlineExceeded
	}
	return payload, err
}

func firstSSEData(response *http.Response) ([]byte, error) {
	body := response.Body
	reader := bufio.NewReader(body)
	var consumed, line, data bytes.Buffer
	timer := time.AfterFunc(protocolDiscoveryTimeout, func() { _ = body.Close() })
	defer timer.Stop()
	restore := func() {
		response.Body = bufferedResponseBody(consumed.Bytes(), reader, body)
	}
	for consumed.Len() <= brokerproto.MaxBody {
		fragment, err := reader.ReadSlice('\n')
		if consumed.Len()+len(fragment) > brokerproto.MaxBody {
			restore()
			return nil, errors.New("MCP server discovery response exceeds limit")
		}
		consumed.Write(fragment)
		line.Write(fragment)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil && !errors.Is(err, io.EOF) {
			restore()
			return nil, err
		}
		lineText := strings.TrimSuffix(strings.TrimSuffix(line.String(), "\n"), "\r")
		if lineText == "" && data.Len() > 0 {
			payload := bytes.TrimSuffix(data.Bytes(), []byte("\n"))
			if !timer.Stop() {
				restore()
				return nil, context.DeadlineExceeded
			}
			restore()
			return append([]byte(nil), payload...), nil
		}
		if value, ok := strings.CutPrefix(lineText, "data:"); ok {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(value))
		}
		line.Reset()
		if errors.Is(err, io.EOF) {
			if data.Len() > 0 {
				payload := append([]byte(nil), data.Bytes()...)
				if !timer.Stop() {
					restore()
					return nil, context.DeadlineExceeded
				}
				restore()
				return payload, nil
			}
			restore()
			return nil, io.EOF
		}
		if err != nil {
			restore()
			return nil, err
		}
	}
	restore()
	return nil, errors.New("MCP server discovery response exceeds limit")
}

func bufferedResponseBody(prefix []byte, remaining io.Reader, body io.Closer) io.ReadCloser {
	return struct {
		io.Reader
		io.Closer
	}{Reader: io.MultiReader(bytes.NewReader(prefix), remaining), Closer: body}
}

func jsonRPCErrorCode(err error) int {
	var wireError *jsonrpc.Error
	if errors.As(err, &wireError) {
		return int(wireError.Code)
	}
	return 0
}

func legacyFallbackRequired(status, errorCode int) bool {
	return status == http.StatusNotFound ||
		status == http.StatusMethodNotAllowed ||
		errorCode == jsonrpc.CodeMethodNotFound ||
		errorCode == mcp.CodeUnsupportedProtocolVersion
}

// remoteOAuthTransport permits non-public addresses only for the explicitly registered
// MCP authority (or a configured proxy), and pins DNS results to the actual dial.
type remoteOAuthTransport struct {
	base             *http.Transport
	trustedAuthority string
	lookupNetIP      func(context.Context, string, string) ([]netip.Addr, error)
	dial             func(context.Context, string, string) (net.Conn, error)
}

func (t *remoteOAuthTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	policy := oauthDialPolicy{targetAuthority: normalizeOAuthAuthority(request.URL)}
	if t.base.Proxy != nil {
		proxyURL, err := t.base.Proxy(request)
		if err != nil {
			return nil, fmt.Errorf("resolving OAuth proxy: %w", err)
		}
		if proxyURL != nil {
			policy.proxyAuthority = normalizeOAuthAuthority(proxyURL)
		}
	}
	ctx := context.WithValue(request.Context(), oauthDialPolicyKey{}, policy)
	return t.base.RoundTrip(request.WithContext(ctx))
}

func (t *remoteOAuthTransport) CloseIdleConnections() {
	t.base.CloseIdleConnections()
}

func (t *remoteOAuthTransport) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid OAuth dial address %q: %w", address, err)
	}
	authority := net.JoinHostPort(normalizeOAuthHost(host), port)
	policy, _ := ctx.Value(oauthDialPolicyKey{}).(oauthDialPolicy)
	trustedPrivateAddress := authority == t.trustedAuthority || authority == policy.proxyAuthority
	if !trustedPrivateAddress && authority != policy.targetAuthority {
		return nil, fmt.Errorf("OAuth transport attempted to connect to unexpected authority %q", authority)
	}

	var addresses []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		addresses = []netip.Addr{ip}
	} else {
		addresses, err = t.lookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, fmt.Errorf("resolving OAuth host %q: %w", host, err)
		}
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("OAuth host %q resolved to no IP addresses", host)
	}
	if !trustedPrivateAddress {
		for _, ip := range addresses {
			if isNonPublicOAuthIP(ip) {
				return nil, fmt.Errorf("refusing OAuth connection to non-public IP address %q for host %q", ip, host)
			}
		}
	}
	var lastErr error
	for _, ip := range addresses {
		conn, err := t.dial(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func normalizeOAuthAuthority(parsed *url.URL) string {
	host := normalizeOAuthHost(parsed.Hostname())
	port := parsed.Port()
	if port == "" {
		if strings.EqualFold(parsed.Scheme, "https") {
			port = "443"
		} else {
			port = "80"
		}
	}
	return net.JoinHostPort(host, port)
}

func normalizeOAuthHost(host string) string {
	return strings.TrimSuffix(strings.ToLower(host), ".")
}

var oauthCGNATRange = netip.MustParsePrefix("100.64.0.0/10")

func isNonPublicOAuthIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	return !ip.IsValid() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() ||
		oauthCGNATRange.Contains(ip)
}

type authorizationCallback struct {
	result *auth.AuthorizationResult
	err    string
}

// RemoteManager owns long-lived HTTP MCP client sessions.
type RemoteManager struct {
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.RWMutex
	sessions   map[string]*remoteSession
	credential credentialStorage
	logger     *slog.Logger
	closed     bool
}

// NewRemoteManager creates a manager whose sessions end when ctx is canceled.
func NewRemoteManager(ctx context.Context, credential credentialStorage, loggers ...*slog.Logger) *RemoteManager {
	managerCtx, cancel := context.WithCancel(ctx)
	logger := slog.Default()
	if len(loggers) > 0 && loggers[0] != nil {
		logger = loggers[0]
	}
	return &RemoteManager{
		ctx:        managerCtx,
		cancel:     cancel,
		sessions:   make(map[string]*remoteSession),
		credential: credential,
		logger:     logger,
	}
}

// Start creates a remote server session if one does not already exist.
func (m *RemoteManager) Start(ctx context.Context, server RemoteServer) (RemoteStatus, error) {
	server.Name = strings.TrimSpace(server.Name)
	server.URL = strings.TrimSpace(server.URL)
	if err := validateRemoteServer(server); err != nil {
		return RemoteStatus{}, err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return RemoteStatus{}, errors.New("MCP manager is closed")
	}
	session := m.sessions[server.Name]
	if session != nil && session.config.URL != server.URL {
		m.mu.Unlock()
		return RemoteStatus{}, errors.New("MCP server name is already connected to a different URL")
	}
	var closeSession *remoteSession
	var retryDelay time.Duration
	if session != nil {
		session.mu.Lock()
		retry := session.status.State == "error" && (session.retryAt.IsZero() || !time.Now().Before(session.retryAt))
		switch {
		case retry:
			retryDelay = session.retryDelay
			delete(m.sessions, server.Name)
			closeSession = session
			session = nil
		case session.status.State == "error" && !session.retryAt.IsZero():
			retryErr := session.status.Error
			session.mu.Unlock()
			m.mu.Unlock()
			return RemoteStatus{}, fmt.Errorf("MCP server %q reconnect is cooling down: %s", server.Name, retryErr)
		default:
			session.mu.Unlock()
		}
		if closeSession != nil {
			closeSession.mu.Unlock()
		}
	}
	if session == nil {
		sessionCtx, cancel := context.WithCancel(m.ctx)
		session = &remoteSession{
			config:           server,
			ctx:              sessionCtx,
			cancel:           cancel,
			status:           RemoteStatus{State: "connecting"},
			retryDelay:       retryDelay,
			authURL:          make(chan string, 1),
			callback:         make(chan authorizationCallback, 1),
			toolRefreshQueue: make(chan struct{}, 1),
			done:             make(chan struct{}),
		}
		session.workers.Add(1)
		m.sessions[server.Name] = session
		go m.connect(session.ctx, session)
	}
	m.mu.Unlock()
	if closeSession != nil {
		closeSession.close()
	}
	return m.waitForChange(ctx, session)
}

// AcknowledgeAuth clears a pending authorization URL after ash has opened it.
func (m *RemoteManager) AcknowledgeAuth(name string) error {
	m.mu.RLock()
	session := m.sessions[name]
	m.mu.RUnlock()
	if session == nil {
		return errors.New("MCP server session has not been started")
	}
	session.mu.Lock()
	if session.status.State == "authorizing" {
		session.status.AuthURL = ""
	}
	session.mu.Unlock()
	return nil
}

// Status returns the current state of a remote session.
func (m *RemoteManager) Status(ctx context.Context, name string) (RemoteStatus, error) {
	m.mu.RLock()
	session := m.sessions[name]
	m.mu.RUnlock()
	if session == nil {
		return RemoteStatus{}, errors.New("MCP server session has not been started")
	}
	return m.waitForChange(ctx, session)
}

// StatusNow returns the current session state without waiting for a transition.
func (m *RemoteManager) StatusNow(name string) (RemoteStatus, error) {
	m.mu.RLock()
	session := m.sessions[name]
	m.mu.RUnlock()
	if session == nil {
		return RemoteStatus{}, errors.New("MCP server session has not been started")
	}
	select {
	case authURL := <-session.authURL:
		m.setStatus(session, RemoteStatus{State: "authorizing", AuthURL: authURL})
	default:
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.status, nil
}

// ListTools returns the tools exposed by a connected remote session.
func (m *RemoteManager) ListTools(name string) ([]RemoteTool, error) {
	return m.ListToolsContext(m.ctx, name)
}

// ListToolsContext returns a complete catalog, coalescing discovery across
// callers while allowing each caller to stop waiting independently.
func (m *RemoteManager) ListToolsContext(ctx context.Context, name string) ([]RemoteTool, error) {
	m.mu.RLock()
	session := m.sessions[name]
	m.mu.RUnlock()
	if session == nil {
		return nil, errors.New("MCP server session has not been started")
	}
	session.mu.Lock()
	client := session.client
	status := session.status
	session.mu.Unlock()
	if client == nil {
		if status.Error != "" {
			return nil, errors.New(status.Error)
		}
		return nil, errors.New("MCP server session is not connected")
	}
	result, err := m.listToolsForSession(ctx, session)
	if err != nil {
		return nil, err
	}
	return cloneRemoteTools(result), nil
}

func (m *RemoteManager) listToolsForSession(ctx context.Context, session *remoteSession) ([]RemoteTool, error) {
	for {
		session.mu.Lock()
		if session.closed {
			session.mu.Unlock()
			return nil, errors.New("MCP server session is closed")
		}
		client := session.client
		status := session.status
		if client == nil {
			session.mu.Unlock()
			if status.Error != "" {
				return nil, errors.New(status.Error)
			}
			return nil, errors.New("MCP server session is not connected")
		}
		init := client.InitializeResult()
		if init == nil || init.Capabilities == nil || init.Capabilities.Tools == nil {
			session.mu.Unlock()
			return []RemoteTool{}, nil
		}
		legacy := init.ProtocolVersion < modernProtocolVersion
		if legacy && session.toolsValid && time.Now().Before(session.toolsExpiry) {
			tools := session.tools
			session.mu.Unlock()
			return tools, nil
		}
		if pending := session.refresh; pending != nil {
			session.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-pending.done:
				if pending.err != nil {
					return nil, pending.err
				}
				return pending.tools, nil
			}
		}
		refresh := &toolRefresh{done: make(chan struct{})}
		generation := session.toolsGen
		session.refresh = refresh
		session.workers.Add(1)
		session.mu.Unlock()
		go m.refreshTools(session, client, refresh, generation)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-refresh.done:
			if refresh.err != nil {
				return nil, refresh.err
			}
			return cloneRemoteTools(refresh.tools), nil
		}
	}
}

func (m *RemoteManager) refreshTools(session *remoteSession, client *mcp.ClientSession, refresh *toolRefresh, generation uint64) {
	defer session.workers.Done()
	ctx, cancel := context.WithTimeout(session.ctx, toolsTimeout)
	defer cancel()
	for {
		tools, err := discoverTools(ctx, client, session.config.Name)
		session.mu.Lock()
		if session.closed || session.ctx.Err() != nil {
			refresh.err = errors.New("MCP server session is closed")
			session.refresh = nil
			close(refresh.done)
			session.mu.Unlock()
			return
		}
		if session.client != client {
			refresh.err = errors.New("MCP server session changed during tool discovery")
			session.refresh = nil
			close(refresh.done)
			session.mu.Unlock()
			return
		}
		if generation != session.toolsGen {
			generation = session.toolsGen
			session.mu.Unlock()
			if ctx.Err() != nil {
				session.mu.Lock()
				refresh.err = ctx.Err()
				session.refresh = nil
				close(refresh.done)
				session.mu.Unlock()
				return
			}
			continue
		}
		if err != nil {
			refresh.err = err
			session.tools = nil
			session.toolsValid = false
			session.refresh = nil
			close(refresh.done)
			session.mu.Unlock()
			return
		}
		init := client.InitializeResult()
		legacy := init == nil || init.ProtocolVersion < modernProtocolVersion
		if legacy {
			session.tools = cloneRemoteTools(tools)
			session.toolsExpiry = time.Now().Add(legacyToolsTTL)
			session.toolsValid = true
		}
		refresh.tools = cloneRemoteTools(tools)
		session.retryDelay = 0
		session.retryAt = time.Time{}
		session.refresh = nil
		close(refresh.done)
		session.mu.Unlock()
		return
	}
}

func discoverTools(ctx context.Context, client *mcp.ClientSession, server string) ([]RemoteTool, error) {
	var tools []RemoteTool
	seenCursors := make(map[string]struct{})
	seenNames := make(map[string]struct{})
	cursor := ""
	for {
		result, err := client.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, fmt.Errorf("listing tools: %w", err)
		}
		for _, tool := range result.Tools {
			if tool == nil || strings.TrimSpace(tool.Name) == "" {
				return nil, errors.New("MCP server returned a tool without a name")
			}
			if _, exists := seenNames[tool.Name]; exists {
				return nil, fmt.Errorf("MCP server returned duplicate tool %q", tool.Name)
			}
			schema, ok := tool.InputSchema.(map[string]any)
			if !ok || schema == nil {
				return nil, fmt.Errorf("MCP tool %q returned a non-object input schema", tool.Name)
			}
			seenNames[tool.Name] = struct{}{}
			tools = append(tools, RemoteTool{
				Server: server, Name: tool.Name, Description: tool.Description, InputSchema: schema,
			})
			encoded, err := json.Marshal(tools)
			if err != nil {
				return nil, fmt.Errorf("encoding MCP tool catalog: %w", err)
			}
			if len(encoded) > brokerproto.MaxBody {
				return nil, errors.New("MCP tool catalog exceeds broker response limit")
			}
		}
		if result.NextCursor == "" {
			break
		}
		if _, exists := seenCursors[result.NextCursor]; exists {
			return nil, errors.New("MCP server repeated a tools/list pagination cursor")
		}
		seenCursors[result.NextCursor] = struct{}{}
		cursor = result.NextCursor
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	return tools, nil
}

func cloneRemoteTools(tools []RemoteTool) []RemoteTool {
	cloned := make([]RemoteTool, len(tools))
	for index, tool := range tools {
		cloned[index] = tool
		if tool.InputSchema != nil {
			cloned[index].InputSchema = cloneJSONMap(tool.InputSchema)
		}
	}
	return cloned
}

func cloneJSONMap(source map[string]any) map[string]any {
	cloned := make(map[string]any, len(source))
	for key, value := range source {
		cloned[key] = cloneJSONValue(value)
	}
	return cloned
}

func cloneJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneJSONMap(typed)
	case []any:
		cloned := make([]any, len(typed))
		for index, item := range typed {
			cloned[index] = cloneJSONValue(item)
		}
		return cloned
	default:
		return value
	}
}

// CallTool invokes a tool on a connected remote session.
func (m *RemoteManager) CallTool(ctx context.Context, server, name string, arguments map[string]any) (*mcp.CallToolResult, error) {
	m.mu.RLock()
	session := m.sessions[server]
	m.mu.RUnlock()
	if session == nil {
		return nil, errors.New("MCP server session has not been started")
	}
	session.mu.Lock()
	client := session.client
	session.mu.Unlock()
	if client == nil {
		return nil, errors.New("MCP server session is not connected")
	}
	result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil && errors.Is(err, mcp.ErrConnectionClosed) {
		m.markSessionFailed(session, err)
	}
	return result, err
}

// Close terminates all sessions.
func (m *RemoteManager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	sessions := m.sessions
	m.sessions = make(map[string]*remoteSession)
	m.mu.Unlock()
	m.cancel()
	for _, session := range sessions {
		session.close()
	}
}

func (s *remoteSession) close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.cancel()
		listener := s.listener
		s.listener = nil
		client := s.client
		s.client = nil
		s.mu.Unlock()
		if client != nil {
			_ = client.Close()
		}
		if listener != nil {
			_ = listener.Close()
		}
		s.workers.Wait()
	})
}

func (m *RemoteManager) markSessionFailed(session *remoteSession, err error) {
	session.mu.Lock()
	if session.closed {
		session.mu.Unlock()
		return
	}
	session.client = nil
	session.tools = nil
	session.toolsValid = false
	if session.retryDelay == 0 {
		session.retryDelay = time.Second
	} else {
		session.retryDelay *= 2
		if session.retryDelay > 30*time.Second {
			session.retryDelay = 30 * time.Second
		}
	}
	session.retryAt = time.Now().Add(session.retryDelay)
	session.status = RemoteStatus{State: "error", Error: err.Error()}
	session.cancel()
	session.mu.Unlock()
}

func (m *RemoteManager) connect(managerCtx context.Context, session *remoteSession) {
	defer session.workers.Done()
	defer close(session.done)
	defer func() {
		session.mu.Lock()
		listener := session.listener
		session.listener = nil
		session.mu.Unlock()
		if listener != nil {
			_ = listener.Close()
		}
	}()

	listener, err := (&net.ListenConfig{}).Listen(managerCtx, "tcp", "127.0.0.1:0")
	if err != nil {
		m.setStatus(session, RemoteStatus{State: "error", Error: err.Error()})
		return
	}
	session.mu.Lock()
	if session.closed || session.ctx.Err() != nil {
		session.mu.Unlock()
		_ = listener.Close()
		return
	}
	session.listener = listener
	session.mu.Unlock()
	callbackServer := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.Path != "/oauth/callback" {
				http.NotFound(w, r)
				return
			}
			query := r.URL.Query()
			if len(query.Get("code")) > 8192 || len(query.Get("state")) > 4096 || len(query.Get("iss")) > 2048 || len(query.Get("error")) > 256 {
				http.Error(w, "OAuth callback parameters exceed limits.", http.StatusBadRequest)
				return
			}
			session.mu.Lock()
			expectedState := session.expectedState
			session.mu.Unlock()
			if expectedState == "" || subtle.ConstantTimeCompare([]byte(query.Get("state")), []byte(expectedState)) != 1 {
				http.Error(w, "OAuth callback state did not match.", http.StatusBadRequest)
				return
			}
			if query.Get("error") != "" {
				if session.authActive.CompareAndSwap(true, false) {
					session.callback <- authorizationCallback{err: query.Get("error")}
					_, _ = w.Write([]byte("Authorization was not completed. You may close this window."))
					return
				}
				http.Error(w, "No authorization request is waiting.", http.StatusConflict)
				return
			}
			result := &auth.AuthorizationResult{Code: query.Get("code"), State: query.Get("state"), Iss: query.Get("iss")}
			if query.Get("error") != "" || result.Code == "" || result.State == "" {
				http.Error(w, "OAuth authorization was not completed.", http.StatusBadRequest)
				return
			}
			if !session.authActive.CompareAndSwap(true, false) {
				http.Error(w, "No authorization request is waiting.", http.StatusConflict)
				return
			}
			select {
			case session.callback <- authorizationCallback{result: result}:
				_, _ = w.Write([]byte("Authorization complete. You may close this window."))
			case <-r.Context().Done():
				http.Error(w, "No authorization request is waiting.", http.StatusConflict)
			}
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	defer func() { _ = callbackServer.Close() }()
	go func() {
		_ = callbackServer.Serve(listener)
	}()

	redirectURL := "http://" + listener.Addr().String() + "/oauth/callback"
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		m.setStatus(session, RemoteStatus{State: "error", Error: "default HTTP transport is not an *http.Transport"})
		return
	}
	baseTransport := defaultTransport.Clone()
	defer baseTransport.CloseIdleConnections()
	parsedServerURL, err := url.Parse(session.config.URL)
	if err != nil {
		m.setStatus(session, RemoteStatus{State: "error", Error: fmt.Sprintf("parsing MCP server URL: %v", err)})
		return
	}
	oauthTransport := &remoteOAuthTransport{
		base:             baseTransport,
		trustedAuthority: normalizeOAuthAuthority(parsedServerURL),
		lookupNetIP:      net.DefaultResolver.LookupNetIP,
		dial: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}
	baseTransport.DialContext = oauthTransport.dialContext
	baseTransport.ResponseHeaderTimeout = 30 * time.Second
	protocolState := &protocolNegotiationState{}
	httpClient := &http.Client{
		Transport: &protocolProbeRoundTripper{base: oauthTransport, state: protocolState},
		Timeout:   0,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	oauthHTTPClient := &http.Client{
		Transport: oauthTransport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	var oauthHandler *auth.AuthorizationCodeHandler
	authorizationFetcher := func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
		parsedAuthURL, err := url.Parse(args.URL)
		if err != nil || parsedAuthURL.Query().Get("state") == "" {
			return nil, errors.New("OAuth authorization URL does not contain state")
		}
		session.mu.Lock()
		session.expectedState = parsedAuthURL.Query().Get("state")
		session.mu.Unlock()
		session.authActive.Store(true)
		defer func() {
			session.authActive.Store(false)
			session.mu.Lock()
			session.expectedState = ""
			session.mu.Unlock()
		}()
		select {
		case session.authURL <- args.URL:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-managerCtx.Done():
			return nil, managerCtx.Err()
		}
		select {
		case result := <-session.callback:
			if result.err != "" {
				return nil, fmt.Errorf("OAuth authorization denied: %s", result.err)
			}
			return result.result, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-managerCtx.Done():
			return nil, managerCtx.Err()
		}
	}
	dynamicClient := &oauthex.ClientRegistrationMetadata{
		RedirectURIs:            []string{redirectURL},
		TokenEndpointAuthMethod: "none",
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		ClientName:              "Ash",
		ApplicationType:         "native",
	}
	oauthConfig := &auth.AuthorizationCodeHandlerConfig{
		DynamicClientRegistrationConfig: &auth.DynamicClientRegistrationConfig{Metadata: dynamicClient},
		RedirectURL:                     redirectURL,
		AuthorizationCodeFetcher:        authorizationFetcher,
		RequestRefreshToken:             true,
		Client:                          oauthHTTPClient,
	}
	saveCredential := func(record persistedOAuth) error {
		payload, err := json.Marshal(record)
		if err != nil {
			return fmt.Errorf("encoding OAuth credentials: %w", err)
		}
		return m.credential.Set(credentialID(session.config), payload)
	}
	oauthConfig.NewTokenSource = func(ctx context.Context, config *oauth2.Config, token *oauth2.Token) (oauth2.TokenSource, error) {
		record := persistedOAuth{Config: *config, Token: *token}
		if err := saveCredential(record); err != nil {
			return nil, fmt.Errorf("persisting OAuth credentials: %w", err)
		}
		return newPersistedTokenSource(config.TokenSource(ctx, token), record, saveCredential), nil
	}
	if saved, loadErr := m.credential.Get(credentialID(session.config)); loadErr == nil {
		var record persistedOAuth
		if err := json.Unmarshal(saved, &record); err != nil {
			m.setStatus(session, RemoteStatus{State: "error", Error: "stored MCP credentials are invalid"})
			return
		}
		tokenSource := record.Config.TokenSource(managerCtx, &record.Token)
		oauthConfig.InitialTokenSource = newPersistedTokenSource(tokenSource, record, saveCredential)
	} else if !errors.Is(loadErr, errCredentialNotFound) {
		m.setStatus(session, RemoteStatus{State: "error", Error: loadErr.Error()})
		return
	}
	oauthHandler, err = auth.NewAuthorizationCodeHandler(oauthConfig)
	if err != nil {
		m.setStatus(session, RemoteStatus{State: "error", Error: err.Error()})
		return
	}

	parsed, _ := url.Parse(session.config.URL)
	client := mcp.NewClient(&mcp.Implementation{Name: "ash-broker", Version: "1.0.0"}, &mcp.ClientOptions{
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) {
			session.mu.Lock()
			if session.closed {
				session.mu.Unlock()
				return
			}
			session.toolsGen++
			session.toolsValid = false
			select {
			case session.toolRefreshQueue <- struct{}{}:
			default:
			}
			session.mu.Unlock()
		},
	})
	transport := &mcp.StreamableClientTransport{
		Endpoint:             parsed.String(),
		HTTPClient:           httpClient,
		OAuthHandler:         oauthHandler,
		DisableStandaloneSSE: false,
	}
	// Nil session options make the SDK negotiate its newest supported version first.
	clientSession, err := client.Connect(session.ctx, transport, nil)
	if err != nil {
		m.setStatus(session, RemoteStatus{State: "error", Error: err.Error()})
		return
	}
	session.mu.Lock()
	if session.closed || session.ctx.Err() != nil {
		session.mu.Unlock()
		_ = clientSession.Close()
		return
	}
	session.client = clientSession
	session.status = RemoteStatus{State: "connected"}
	session.retryAt = time.Time{}
	session.retryDelay = 0
	session.workers.Add(1)
	session.workers.Add(1)
	session.mu.Unlock()
	go m.refreshNotifiedTools(session)
	go func() {
		defer session.workers.Done()
		if _, err := m.listToolsForSession(session.ctx, session); err != nil && session.ctx.Err() == nil {
			m.logger.Warn("MCP startup tool catalog warm-up failed", "server", session.config.Name, "error", err, "EID", "mcpWrM01")
		}
	}()
	waitDone := make(chan error, 1)
	go func() { waitDone <- clientSession.Wait() }()
	select {
	case <-session.ctx.Done():
		_ = clientSession.Close()
	case err := <-waitDone:
		if err == nil {
			err = errors.New("MCP server closed the session")
		}
		m.markSessionFailed(session, err)
		_ = clientSession.Close()
	}
}

func (m *RemoteManager) refreshNotifiedTools(session *remoteSession) {
	defer session.workers.Done()
	for {
		select {
		case <-session.ctx.Done():
			return
		case <-session.toolRefreshQueue:
			if _, err := m.listToolsForSession(session.ctx, session); err != nil && session.ctx.Err() == nil {
				m.logger.Warn("refreshing MCP tool catalog after notification failed", "server", session.config.Name, "error", err, "EID", "mcpL1stC")
			}
		}
	}
}

func (m *RemoteManager) waitForChange(ctx context.Context, session *remoteSession) (RemoteStatus, error) {
	for {
		session.mu.Lock()
		status := session.status
		session.mu.Unlock()
		if status.State == "connected" || status.State == "error" || status.AuthURL != "" {
			return status, nil
		}
		select {
		case authURL := <-session.authURL:
			m.setStatus(session, RemoteStatus{State: "authorizing", AuthURL: authURL})
			return RemoteStatus{State: "authorizing", AuthURL: authURL}, nil
		case <-session.done:
			session.mu.Lock()
			status := session.status
			session.mu.Unlock()
			return status, nil
		case <-time.After(authWait):
		case <-ctx.Done():
			return RemoteStatus{}, ctx.Err()
		}
	}
}

func (m *RemoteManager) setStatus(session *remoteSession, status RemoteStatus) {
	session.mu.Lock()
	if !session.closed {
		session.status = status
	}
	if status.State == "error" && !session.closed {
		if session.retryDelay == 0 {
			session.retryDelay = time.Second
		} else {
			session.retryDelay *= 2
			if session.retryDelay > 30*time.Second {
				session.retryDelay = 30 * time.Second
			}
		}
		session.retryAt = time.Now().Add(session.retryDelay)
		session.cancel()
	}
	session.mu.Unlock()
}

func validateRemoteServer(server RemoteServer) error {
	name := strings.TrimSpace(server.Name)
	if name == "" || len(name) > 128 {
		return errors.New("MCP server name must contain 1 to 128 characters")
	}
	u, err := url.Parse(strings.TrimSpace(server.URL))
	scheme := ""
	if err == nil {
		scheme = strings.ToLower(u.Scheme)
	}
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (scheme != "https" && scheme != "http") {
		return errors.New("MCP server URL must be a valid http or https URL without embedded credentials, query, or fragment")
	}
	if scheme == "http" && !isLocalHost(u.Hostname()) {
		return errors.New("MCP server URLs must use https unless they target localhost")
	}
	if strings.ContainsAny(name, "/\\\x00") {
		return errors.New("MCP server name contains invalid characters")
	}
	return nil
}

func isLocalHost(host string) bool {
	return strings.EqualFold(host, "localhost") || host == "127.0.0.1" || host == "::1"
}

func credentialID(server RemoteServer) string {
	hash := sha256.Sum256([]byte(server.Name + "\x00" + server.URL))
	return "mcp-" + hex.EncodeToString(hash[:])
}

type persistedTokenSource struct {
	mu     sync.Mutex
	source oauth2.TokenSource
	record persistedOAuth
	save   func(persistedOAuth) error
}

func newPersistedTokenSource(source oauth2.TokenSource, record persistedOAuth, save func(persistedOAuth) error) oauth2.TokenSource {
	return &persistedTokenSource{source: source, record: record, save: save}
}

func (s *persistedTokenSource) Token() (*oauth2.Token, error) {
	token, err := s.source.Token()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if token.AccessToken != s.record.Token.AccessToken ||
		token.RefreshToken != s.record.Token.RefreshToken ||
		token.TokenType != s.record.Token.TokenType ||
		!token.Expiry.Equal(s.record.Token.Expiry) {
		s.record.Token = *token
		if err := s.save(s.record); err != nil {
			return nil, fmt.Errorf("persisting refreshed MCP credentials: %w", err)
		}
	}
	return token, nil
}
