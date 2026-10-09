// Command ash-broker is a long-running, minimal-dependency HTTPS connection
// broker used by the ash CLI to share keep-alive connections across
// short-lived invocations. It deliberately avoids importing any AI provider
// SDK code so its binary size and startup cost stay small and constant
// regardless of how many providers the ash CLI itself supports.
package main

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"ash/internal/brokerproto"
	"ash/internal/localize"
	"ash/internal/mcp"
)

const (
	brokerTokenEnv          = "ASH_BROKER_TOKEN"
	aiEndpointEnv           = "AI_ENDPOINT"
	aiTimeoutEnv            = "AI_TIMEOUT"
	aiWarmupEnv             = "ASH_BROKER_AI_WARMUP"
	defaultAITimeout        = 3 * time.Minute
	brokerParentPollTick    = 5 * time.Second
	mcpWarmupWorkers        = 4
	mcpWarmupTimeout        = 15 * time.Second
	aiWarmupTimeout         = 5 * time.Second
	aiWarmupMaxResponseBody = 64 * 1024
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	return runBroker(ctx, args, stdout, stderr)
}

// aiTimeout returns the configured AI request timeout, or the default when unset or invalid.
// Kept as a minimal, self-contained duplicate of ash's config.go helper so this binary
// never needs to import ash's config/provider packages.
func aiTimeout() time.Duration {
	if raw := strings.TrimSpace(os.Getenv(aiTimeoutEnv)); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			return parsed
		}
	}
	return defaultAITimeout
}

// newBrokerLogger returns a JSON slog logger writing to w, matching the
// structured logging convention ash.go uses (lowercase level, "message" key).
// Kept as a minimal, self-contained duplicate of ash's support.go helper so
// this binary never needs to import ash's own packages.
func newBrokerLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			if attr.Key == slog.MessageKey {
				attr.Key = "message"
			}
			if attr.Key == slog.LevelKey {
				attr.Value = slog.StringValue(strings.ToLower(attr.Value.String()))
			}
			return attr
		},
	}))
}

func runBroker(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	logger := newBrokerLogger(stderr)
	var socket string
	var parentPID int
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--socket":
			index++
			if index >= len(args) {
				logger.Error("--socket requires a value", "EID", "Hb4nRq2K")
				return 2
			}
			socket = args[index]
		case "--parent-pid":
			index++
			if index >= len(args) {
				logger.Error("--parent-pid requires a value", "EID", "Wt7XmZ1p")
				return 2
			}
			parsedPID, err := strconv.Atoi(args[index])
			if err != nil || parsedPID <= 0 {
				logger.Error("--parent-pid must be a positive integer", "EID", "Qk3FbY8s")
				return 2
			}
			parentPID = parsedPID
		case "--lease":
			index++
			if index >= len(args) {
				logger.Error("--lease requires a value", "EID", "Zx9MdC4v")
				return 2
			}
		default:
			logger.Error(fmt.Sprintf("unknown broker option %q", args[index]), "EID", "Jp2VtR6w")
			return 2
		}
	}
	token := strings.TrimSpace(os.Getenv(brokerTokenEnv))
	if socket == "" || token == "" || parentPID == 0 {
		logger.Error("ash-broker requires --socket, --parent-pid, and ASH_BROKER_TOKEN", "EID", "Nc5LpK9x")
		return 2
	}
	endpoint := strings.TrimSpace(os.Getenv(aiEndpointEnv))
	if endpoint == "" {
		logger.Error("ash-broker requires a complete AI environment", "EID", "Ry8VqM3d")
		return 2
	}
	credentialKey := os.Getenv("ASH_MCP_CREDENTIAL_KEY")
	if err := os.Unsetenv("ASH_MCP_CREDENTIAL_KEY"); err != nil {
		logger.Error(fmt.Sprintf("failed to clear MCP credential key from broker environment: %v", err), "EID", "Mc8wQx2v")
		return 1
	}
	home, err := os.UserHomeDir()
	if err != nil {
		logger.Error(fmt.Sprintf("failed to locate home directory for MCP credentials: %v", err), "EID", "Fh4TzP9b")
		return 1
	}
	if err := localize.Init(os.Getenv("LANG"), filepath.Join(home, ".ash", "languages")); err != nil {
		_, _ = fmt.Fprintf(stderr, "language catalog error: %v\n", err)
		return 1
	}
	credentialStorage, err := mcp.NewCredentialStorage(filepath.Join(home, ".ash", "mcp"), credentialKey)
	if err != nil {
		logger.Error(fmt.Sprintf("failed to initialize MCP credential storage: %v", err), "EID", "Xk7NfV3c")
		return 1
	}
	endpointURL, err := url.Parse(endpoint)
	if err != nil || endpointURL.Host == "" {
		logger.Error("ash-broker requires a valid AI_ENDPOINT host", "EID", "Ft6BwX2n")
		return 2
	}
	aiWarmup, err := aiWarmupEnabled()
	if err != nil {
		logger.Error(fmt.Sprintf("invalid AI broker warm-up setting: %v", err), "EID", "AiWarmUp1")
		return 2
	}
	allowedHost := endpointURL.Host
	// #nosec G703 -- the broker socket path is supplied by the same-user shell setup.
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		logger.Error(fmt.Sprintf("failed to create broker socket directory: %v", err), "EID", "Vd4KpS7m")
		return 1
	}
	dialCtx, cancelDial := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancelDial()
	if existing, dialErr := (&net.Dialer{}).DialContext(dialCtx, "unix", socket); dialErr == nil {
		_ = existing.Close()
		logger.Error("broker socket is already in use", "EID", "Xm2QwZ9k")
		return 1
	}
	// #nosec G703 -- the broker socket path is supplied by the same-user shell setup.
	_ = os.Remove(socket)
	listener, listenErr := (&net.ListenConfig{}).Listen(ctx, "unix", socket)
	if listenErr != nil {
		logger.Error(fmt.Sprintf("failed to listen on broker socket: %v", listenErr), "EID", "Bn7ZtL4p")
		return 1
	}
	defer func() {
		_ = listener.Close()
		// #nosec G703 -- the broker socket path is supplied by the same-user shell setup.
		_ = os.Remove(socket)
	}()
	// #nosec G703 -- the broker socket path is supplied by the same-user shell setup.
	if err := os.Chmod(socket, 0o600); err != nil {
		logger.Error(fmt.Sprintf("failed to secure broker socket permissions: %v", err), "EID", "Kw3RmV8t")
		return 1
	}
	client := newBrokerHTTPClient()
	brokerCtx, cancelBroker := context.WithCancel(ctx)
	defer cancelBroker()
	mcpManager := mcp.NewRemoteManager(brokerCtx, credentialStorage, logger)
	defer mcpManager.Close()
	var active sync.WaitGroup
	var startup sync.WaitGroup
	startup.Add(1)
	go func() {
		defer startup.Done()
		warmConfiguredMCP(brokerCtx, logger, mcpManager, home)
	}()
	if aiWarmup {
		startup.Add(1)
		go func() {
			defer startup.Done()
			startedAt := time.Now()
			status, reused, warmErr := warmAIConnection(brokerCtx, client, endpoint)
			if warmErr != nil {
				logger.Warn(fmt.Sprintf("AI connection warm-up failed: %v", warmErr), "EID", "AiWarmUp2", "warmup_status", status, "duration_ms", time.Since(startedAt).Milliseconds())
			} else {
				logger.Info("AI connection warm-up completed", "EID", "AiWarmUp3", "http_status", status, "connection_reused", reused, "duration_ms", time.Since(startedAt).Milliseconds())
			}
		}()
	}
	go func() {
		<-brokerCtx.Done()
		_ = listener.Close()
	}()
	go func() {
		ticker := time.NewTicker(brokerParentPollTick)
		defer ticker.Stop()
		for {
			select {
			case <-brokerCtx.Done():
				return
			case <-ticker.C:
				if !brokerParentAlive(parentPID) {
					cancelBroker()
					_ = listener.Close()
					return
				}
			}
		}
	}()
	for {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			break
		}
		active.Add(1)
		go func() { defer active.Done(); handleBrokerConn(brokerCtx, conn, token, client, allowedHost, mcpManager) }()
	}
	startup.Wait()
	active.Wait()
	if transport, ok := client.Transport.(*http.Transport); ok {
		transport.CloseIdleConnections()
	}
	cancelBroker()
	return 0
}

func aiWarmupEnabled() (bool, error) {
	switch strings.TrimSpace(os.Getenv(aiWarmupEnv)) {
	case "", "1":
		return true, nil
	case "0":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be 0 or 1", aiWarmupEnv)
	}
}

func warmAIConnection(ctx context.Context, client *http.Client, endpoint string) (int, bool, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return 0, false, errors.New("AI warm-up endpoint must be a valid http or https URL without embedded credentials")
	}
	probeCtx, cancel := context.WithTimeout(ctx, aiWarmupTimeout)
	defer cancel()
	probeURL := &url.URL{Scheme: parsed.Scheme, Host: parsed.Host}
	request, err := http.NewRequestWithContext(probeCtx, http.MethodHead, probeURL.String(), nil)
	if err != nil {
		return 0, false, fmt.Errorf("creating AI warm-up request: %w", err)
	}
	reused := false
	trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused }}
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), trace))
	probeClient := *client
	probeClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := probeClient.Do(request)
	if err != nil {
		return 0, reused, fmt.Errorf("sending AI warm-up request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if copied, err := io.Copy(io.Discard, io.LimitReader(response.Body, aiWarmupMaxResponseBody+1)); err != nil {
		return response.StatusCode, reused, fmt.Errorf("draining AI warm-up response: %w", err)
	} else if copied > aiWarmupMaxResponseBody {
		return response.StatusCode, reused, errors.New("AI warm-up response body exceeds size limit")
	}
	return response.StatusCode, reused, nil
}

func warmConfiguredMCP(ctx context.Context, logger *slog.Logger, manager *mcp.RemoteManager, home string) {
	cwd, err := os.Getwd()
	if err != nil {
		logger.Error(fmt.Sprintf("resolving working directory for MCP warm-up: %v", err), "EID", "McPWrk01")
		return
	}
	servers, err := mcp.ResolveRemoteServers(home, cwd, os.ReadFile)
	if err != nil {
		logger.Error(fmt.Sprintf("loading MCP registrations for broker warm-up: %v", err), "EID", "McPWrk02")
		return
	}
	if len(servers) == 0 {
		return
	}
	semaphore := make(chan struct{}, mcpWarmupWorkers)
	var workers sync.WaitGroup
	for _, server := range servers {
		select {
		case <-ctx.Done():
			workers.Wait()
			return
		case semaphore <- struct{}{}:
		}
		workers.Add(1)
		go func(server mcp.RemoteServer) {
			defer workers.Done()
			defer func() { <-semaphore }()
			startedAt := time.Now()
			startCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			status, err := manager.Start(startCtx, server)
			if err != nil {
				logger.Warn("MCP server startup warm-up failed", "EID", "McPWrk03", "server", server.Name, "error", err, "duration_ms", time.Since(startedAt).Milliseconds())
				return
			}
			if status.State == "connected" {
				discoveryCtx, discoveryCancel := context.WithTimeout(ctx, mcpWarmupTimeout)
				defer discoveryCancel()
				if _, err := manager.ListToolsContext(discoveryCtx, server.Name); err != nil {
					logger.Warn("MCP tool discovery warm-up failed", "EID", "McPWrk04", "server", server.Name, "error", err, "duration_ms", time.Since(startedAt).Milliseconds())
					return
				}
				logger.Debug("MCP server startup warm-up completed", "EID", "McPWrk05", "server", server.Name, "duration_ms", time.Since(startedAt).Milliseconds())
				return
			}
			logger.Debug("MCP server startup warm-up deferred", "EID", "McPWrk06", "server", server.Name, "state", status.State, "duration_ms", time.Since(startedAt).Milliseconds())
		}(server)
	}
	workers.Wait()
}

func brokerParentAlive(parentPID int) bool {
	if parentPID <= 0 {
		return false
	}
	err := syscall.Kill(parentPID, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func handleBrokerConn(ctx context.Context, conn net.Conn, token string, client *http.Client, allowedHost string, mcpManagers ...*mcp.RemoteManager) {
	defer func() { _ = conn.Close() }()
	if !brokerPeerAllowed(conn) {
		return
	}
	_ = conn.SetDeadline(time.Now().Add(aiTimeout()))
	payload, err := brokerproto.ReadFrame(bufio.NewReader(conn))
	if err != nil {
		return
	}
	var request brokerproto.Request
	if json.Unmarshal(payload, &request) != nil || request.Version != brokerproto.Version || subtle.ConstantTimeCompare([]byte(request.Token), []byte(token)) != 1 {
		_ = brokerproto.WriteFrame(conn, mustBrokerJSON(brokerproto.Response{Version: brokerproto.Version, Error: "broker request rejected"}))
		return
	}
	if request.MCPAction != "" {
		if len(mcpManagers) != 1 || mcpManagers[0] == nil {
			_ = brokerproto.WriteFrame(conn, mustBrokerJSON(brokerproto.Response{Version: brokerproto.Version, Error: "MCP broker is unavailable"}))
			return
		}
		response := handleMCPRequest(ctx, request, mcpManagers[0])
		_ = brokerproto.WriteFrame(conn, mustBrokerJSON(response))
		return
	}
	if !brokerproto.URLAllowed(request.URL, allowedHost) || len(request.Body) > brokerproto.MaxBody {
		_ = brokerproto.WriteFrame(conn, mustBrokerJSON(brokerproto.Response{Version: brokerproto.Version, Error: "broker request rejected"}))
		return
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, request.URL, strings.NewReader(string(request.Body)))
	requestReused := false
	requestTrace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { requestReused = info.Reused }}
	if err == nil {
		httpRequest = httpRequest.WithContext(httptrace.WithClientTrace(httpRequest.Context(), requestTrace))
	}
	if err == nil {
		headerBytes := 0
		for name, value := range request.Headers {
			if !brokerproto.HeaderAllowed(name) || len(value) > brokerproto.MaxHeader {
				err = errors.New("broker header rejected")
				break
			}
			headerBytes += len(name) + len(value)
			if headerBytes > brokerproto.MaxHeader {
				err = errors.New("broker headers exceed limit")
				break
			}
			httpRequest.Header.Set(name, value)
		}
	}
	if err == nil {
		httpResponse, doErr := client.Do(httpRequest)
		if doErr != nil {
			err = doErr
		} else {
			body, readErr := io.ReadAll(io.LimitReader(httpResponse.Body, brokerproto.MaxBody+1))
			closeErr := httpResponse.Body.Close()
			switch {
			case readErr != nil:
				err = readErr
			case closeErr != nil:
				err = closeErr
			case len(body) > brokerproto.MaxBody:
				err = errors.New("broker response exceeds limit")
			default:
				err = brokerproto.WriteFrame(conn, mustBrokerJSON(brokerproto.Response{Version: brokerproto.Version, Status: httpResponse.StatusCode, Reused: requestReused, ContentType: httpResponse.Header.Get("Content-Type"), Body: body}))
			}
		}
	}
	if err != nil {
		_ = brokerproto.WriteFrame(conn, mustBrokerJSON(brokerproto.Response{Version: brokerproto.Version, Error: err.Error()}))
	}
}

func handleMCPRequest(ctx context.Context, request brokerproto.Request, manager *mcp.RemoteManager) brokerproto.Response {
	response := brokerproto.Response{Version: brokerproto.Version}
	var status mcp.RemoteStatus
	var err error
	actionTimeout := mcpWarmupTimeout
	switch request.MCPAction {
	case "start":
		actionTimeout = 30 * time.Second
	case "auth_ack", "status_now":
		actionTimeout = 5 * time.Second
	case "call":
		actionTimeout = aiTimeout()
	}
	actionCtx, cancel := context.WithTimeout(ctx, actionTimeout)
	defer cancel()
	switch request.MCPAction {
	case "start":
		status, err = manager.Start(actionCtx, mcp.RemoteServer{Name: request.MCPServer, URL: request.MCPURL})
	case "status":
		status, err = manager.Status(actionCtx, request.MCPServer)
	case "status_now":
		status, err = manager.StatusNow(request.MCPServer)
	case "auth_ack":
		err = manager.AcknowledgeAuth(request.MCPServer)
	case "list":
		var tools []mcp.RemoteTool
		tools, err = manager.ListToolsContext(actionCtx, request.MCPServer)
		if err == nil {
			response.MCPToolsReady = true
			response.MCPTools = make([]brokerproto.MCPTool, 0, len(tools))
			for _, tool := range tools {
				response.MCPTools = append(response.MCPTools, brokerproto.MCPTool{
					Server: tool.Server, Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema,
				})
			}
		}
	case "call":
		var result any
		result, err = manager.CallTool(actionCtx, request.MCPServer, request.MCPTool, request.MCPArgs)
		if err == nil {
			response.MCPResult, err = json.Marshal(result)
		}
	default:
		err = errors.New("unsupported MCP broker action")
	}
	if err != nil {
		response.Error = err.Error()
		return response
	}
	if request.MCPAction == "start" || request.MCPAction == "status" || request.MCPAction == "status_now" {
		response.MCPStatus = &brokerproto.MCPStatus{State: status.State, AuthURL: status.AuthURL, Error: status.Error}
		if request.MCPIncludeTools && status.State == "connected" && (request.MCPAction == "start" || request.MCPAction == "status") {
			tools, listErr := manager.ListToolsContext(actionCtx, request.MCPServer)
			if listErr != nil {
				response.Error = listErr.Error()
				return response
			}
			response.MCPToolsReady = true
			response.MCPTools = make([]brokerproto.MCPTool, 0, len(tools))
			for _, tool := range tools {
				response.MCPTools = append(response.MCPTools, brokerproto.MCPTool{
					Server: tool.Server, Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema,
				})
			}
		}
	}
	return response
}

func mustBrokerJSON(value brokerproto.Response) []byte {
	payload, _ := json.Marshal(value)
	return payload
}

func newBrokerHTTPClient() *http.Client {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		transport = &http.Transport{}
	} else {
		transport = transport.Clone()
	}
	transport.MaxIdleConns = 32
	transport.MaxIdleConnsPerHost = 8
	transport.MaxConnsPerHost = 16
	transport.IdleConnTimeout = 0
	return &http.Client{Transport: transport, Timeout: aiTimeout()}
}
