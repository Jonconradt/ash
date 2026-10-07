package mcp

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

const (
	toolNamePrefix = "mcp_"
	authWait       = 200 * time.Millisecond
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
	mu            sync.Mutex
	config        RemoteServer
	status        RemoteStatus
	client        *mcp.ClientSession
	authURL       chan string
	callback      chan authorizationCallback
	authActive    atomic.Bool
	expectedState string
	listener      net.Listener
	done          chan struct{}
	closeOnce     sync.Once
}

type authorizationCallback struct {
	result *auth.AuthorizationResult
	err    string
}

// RemoteManager owns long-lived HTTP MCP client sessions.
type RemoteManager struct {
	ctx        context.Context
	mu         sync.RWMutex
	sessions   map[string]*remoteSession
	credential credentialStorage
}

// NewRemoteManager creates a manager whose sessions end when ctx is canceled.
func NewRemoteManager(ctx context.Context, credential credentialStorage) *RemoteManager {
	return &RemoteManager{
		ctx:        ctx,
		sessions:   make(map[string]*remoteSession),
		credential: credential,
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
	session := m.sessions[server.Name]
	if session != nil {
		session.mu.Lock()
		retry := session.status.State == "error"
		session.mu.Unlock()
		if retry {
			delete(m.sessions, server.Name)
			session.close()
			session = nil
		}
	}
	if session == nil {
		session = &remoteSession{
			config:   server,
			status:   RemoteStatus{State: "connecting"},
			authURL:  make(chan string, 1),
			callback: make(chan authorizationCallback, 1),
			done:     make(chan struct{}),
		}
		m.sessions[server.Name] = session
		go m.connect(m.ctx, session)
	} else if session.config.URL != server.URL {
		m.mu.Unlock()
		return RemoteStatus{}, errors.New("MCP server name is already connected to a different URL")
	}
	m.mu.Unlock()
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

// ListTools returns the tools exposed by a connected remote session.
func (m *RemoteManager) ListTools(name string) ([]RemoteTool, error) {
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
	ctx, cancel := context.WithTimeout(m.ctx, 15*time.Second)
	defer cancel()
	result, err := client.ListTools(ctx, nil)
	if err != nil {
		return nil, err
	}
	tools := make([]RemoteTool, 0, len(result.Tools))
	for _, tool := range result.Tools {
		schema, ok := tool.InputSchema.(map[string]any)
		if !ok {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		tools = append(tools, RemoteTool{Server: name, Name: tool.Name, Description: tool.Description, InputSchema: schema})
	}
	return tools, nil
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
	return client.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
}

// Close terminates all sessions.
func (m *RemoteManager) Close() {
	m.mu.Lock()
	sessions := m.sessions
	m.sessions = make(map[string]*remoteSession)
	m.mu.Unlock()
	for _, session := range sessions {
		session.close()
	}
}

func (s *remoteSession) close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		listener := s.listener
		s.listener = nil
		client := s.client
		s.client = nil
		s.mu.Unlock()
		if listener != nil {
			_ = listener.Close()
		}
		if client != nil {
			_ = client.Close()
		}
	})
}

func (m *RemoteManager) connect(managerCtx context.Context, session *remoteSession) {
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
	go func() {
		_ = callbackServer.Serve(listener)
	}()

	redirectURL := "http://" + listener.Addr().String() + "/oauth/callback"
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
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
		Client:                          httpClient,
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
	client := mcp.NewClient(&mcp.Implementation{Name: "ash-broker", Version: "1.0.0"}, nil)
	transport := &mcp.StreamableClientTransport{Endpoint: parsed.String(), HTTPClient: httpClient, OAuthHandler: oauthHandler}
	ctx, cancel := context.WithCancel(managerCtx)
	defer cancel()
	clientSession, err := client.Connect(ctx, transport, nil)
	if err != nil {
		m.setStatus(session, RemoteStatus{State: "error", Error: err.Error()})
		return
	}
	session.mu.Lock()
	session.client = clientSession
	session.status = RemoteStatus{State: "connected"}
	session.mu.Unlock()
	<-managerCtx.Done()
	_ = clientSession.Close()
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
	session.status = status
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
