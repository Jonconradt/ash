// Copyright (c) 2026 Jonathan L. Conradt
// SPDX-License-Identifier: MIT

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
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Jonconradt/ash/internal/brokerproto"
	"github.com/Jonconradt/ash/internal/localize"
	"github.com/Jonconradt/ash/internal/mcp"
	"github.com/Jonconradt/ash/internal/scratchlife"
	"github.com/Jonconradt/ash/internal/workspace"
)

const (
	brokerTokenEnv          = "ASH_BROKER_TOKEN"
	aiEndpointEnv           = "ASH_AI_ENDPOINT"
	aiTimeoutEnv            = "ASH_AI_TIMEOUT"
	aiWarmupEnv             = "ASH_BROKER_AI_WARMUP"
	defaultAITimeout        = 3 * time.Minute
	brokerParentPollTick    = 5 * time.Second
	mcpWarmupWorkers        = 4
	mcpWarmupTimeout        = 15 * time.Second
	aiWarmupTimeout         = 5 * time.Second
	aiWarmupMaxResponseBody = 64 * 1024
	brokerLogDirectory      = "brokers"
)

var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	restarts := make(chan brokerRestart, 1)
	code := runBrokerServer(ctx, args, stdout, stderr, restarts)
	select {
	case restart := <-restarts:
		execArgs := append([]string{restart.binary}, restart.args...)
		// #nosec G204 -- restart.binary is validated as an owner-only ash-broker executable before it is queued.
		if syscall.Exec(restart.binary, execArgs, restart.env) != nil {
			return 1
		}
	default:
	}
	return code
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
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			if attr.Key == slog.MessageKey {
				attr.Key = "message"
			}
			if attr.Key == slog.LevelKey {
				attr.Value = slog.StringValue(strings.ToLower(attr.Value.String()))
			}
			return attr
		},
	})
	return slog.New(localize.NewSlogHandler(handler))
}

func runBroker(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return runBrokerServer(ctx, args, stdout, stderr, nil)
}

type brokerRestart struct {
	binary string
	args   []string
	env    []string
}

func runBrokerServer(ctx context.Context, args []string, stdout, stderr io.Writer, restarts chan<- brokerRestart) int {
	if len(args) == 2 && args[0] == "--restart" {
		if err := requestBrokerRestart(args[1]); err != nil {
			return 1
		}
		return 0
	}
	if len(args) == 1 {
		switch args[0] {
		case "-h", "--help":
			_, _ = fmt.Fprintln(stdout, localize.Text("usage.broker"))
			_, _ = fmt.Fprintln(stdout, localize.Text("legal.ash_attribution"))
			return 0
		case "--version", "version":
			_, _ = fmt.Fprintln(stdout, version)
			return 0
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if err := localize.Init(localize.EnvironmentLocale(), filepath.Join(workspace.Root(home), "languages")); err != nil {
			if fallbackErr := localize.Init("en_US", ""); fallbackErr != nil {
				_, _ = fmt.Fprintln(stderr, localize.Format("log.broker.language_error", []any{fallbackErr}))
				return 1
			}
		}
	}
	logger := newBrokerLogger(stderr)
	previousLogger := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(previousLogger)
	if err := workspace.ValidateBaseDirs(); err != nil {
		logger.Error(err.Error())
		return 2
	}
	if raw := strings.TrimSpace(os.Getenv(aiTimeoutEnv)); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 {
			logger.Error(localize.Format("ash.invalid_config_value", []any{aiTimeoutEnv}))
			return 2
		}
	}
	var socket string
	var sessionID string
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
		case "--session-id":
			index++
			if index >= len(args) {
				logger.Error(localize.Text("log.broker.session_id_value_required"))
				return 2
			}
			sessionID = args[index]
		default:
			logger.Error(localize.Format("log.broker.unknown_option", []any{args[index]}), "EID", "Jp2VtR6w")
			return 2
		}
	}
	token := strings.TrimSpace(os.Getenv(brokerTokenEnv))
	if socket == "" || sessionID == "" || token == "" || parentPID == 0 {
		logger.Error(localize.Text("log.broker.arguments_required"), "EID", "Nc5LpK9x")
		return 2
	}
	if err := scratchlife.ValidateSessionID(sessionID); err != nil {
		logger.Error(localize.Text("log.broker.session_id_invalid"), "error", err)
		return 2
	}
	instanceID := strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	endpoint := strings.TrimSpace(os.Getenv(aiEndpointEnv))
	if endpoint == "" {
		logger.Error("ash-broker requires a complete AI environment", "EID", "Ry8VqM3d")
		return 2
	}
	credentialKey := os.Getenv("ASH_MCP_CREDENTIAL_KEY")
	if err := os.Unsetenv("ASH_MCP_CREDENTIAL_KEY"); err != nil {
		logger.Error(localize.Format("log.broker.clear_credential_failed", []any{err}), "EID", "Mc8wQx2v")
		return 1
	}
	home, err := os.UserHomeDir()
	if err != nil {
		logger.Error(localize.Format("log.broker.home_failed", []any{err}), "EID", "Fh4TzP9b")
		return 1
	}
	brokerLog, err := openBrokerLog(home, sessionID)
	if err != nil {
		slog.Warn(localize.Format("log.broker.log_file_open_failed", []any{err}), "error", err, "EID", "2R2gNHhb")
	} else {
		stderrLogger := logger
		writers := make([]io.Writer, 0, 2)
		if stderr != nil {
			writers = append(writers, stderr)
		}
		writers = append(writers, brokerLog)
		logger = newBrokerLogger(io.MultiWriter(writers...))
		slog.SetDefault(logger)
		defer func() {
			if err := brokerLog.Close(); err != nil {
				stderrLogger.Warn(localize.Format("log.broker.log_file_close_failed", []any{err}), "error", err, "EID", "zGIS2QmN")
			}
		}()
	}
	scratchRoot := filepath.Join(workspace.StateRoot(home), "scratch")
	toolCapabilities := newToolCapabilityCache(filepath.Join(workspace.CacheRoot(home), "tool-capabilities"))
	scratchLock, err := scratchlife.Acquire(scratchRoot, sessionID)
	if err != nil {
		logger.Error(localize.Format("log.scratch.lifecycle_failed", []any{err}), "error", err)
		return 1
	}
	defer func() {
		if err := scratchLock.Close(); err != nil {
			logger.Warn(localize.Format("log.scratch.lifecycle_failed", []any{err}), "error", err)
		}
	}()
	credentialStorage, err := mcp.NewCredentialStorage(filepath.Join(workspace.DataRoot(home), "mcp"), credentialKey)
	if err != nil {
		logger.Error(localize.Format("log.broker.credential_storage_failed", []any{err}), "EID", "Xk7NfV3c")
		return 1
	}
	endpointURL, err := url.Parse(endpoint)
	if err != nil || endpointURL.Host == "" {
		logger.Error("ash-broker requires a valid ASH_AI_ENDPOINT host", "EID", "Ft6BwX2n")
		return 2
	}
	aiWarmup, err := aiWarmupEnabled()
	if err != nil {
		logger.Error(localize.Format("log.broker.warmup_setting_invalid", []any{err}), "EID", "AiWarmUp1")
		return 2
	}
	allowedHost := endpointURL.Host
	// #nosec G703 -- the broker socket path is supplied by the same-user shell setup.
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		logger.Error(localize.Format("log.broker.socket_directory_failed", []any{err}), "EID", "Vd4KpS7m")
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
		logger.Error(localize.Format("log.broker.listen_failed", []any{listenErr}), "EID", "Bn7ZtL4p")
		return 1
	}
	defer func() {
		_ = listener.Close()
		// #nosec G703 -- the broker socket path is supplied by the same-user shell setup.
		_ = os.Remove(socket)
	}()
	// #nosec G703 -- the broker socket path is supplied by the same-user shell setup.
	if err := os.Chmod(socket, 0o600); err != nil {
		logger.Error(localize.Format("log.broker.socket_permissions_failed", []any{err}), "EID", "Kw3RmV8t")
		return 1
	}
	client := newBrokerHTTPClient()
	brokerCtx, cancelBroker := context.WithCancel(ctx)
	defer cancelBroker()
	mcpManager := mcp.NewRemoteManager(brokerCtx, credentialStorage, logger)
	defer mcpManager.Close()
	var active sync.WaitGroup
	var startup sync.WaitGroup
	startup.Go(func() {
		warmConfiguredMCP(brokerCtx, logger, mcpManager, home)
	})
	if aiWarmup {
		startup.Go(func() {
			startedAt := time.Now()
			status, reused, warmErr := warmAIConnection(brokerCtx, client, endpoint)
			if warmErr != nil {
				logger.Warn(fmt.Sprintf("AI connection warm-up failed: %v", warmErr), "EID", "AiWarmUp2", "warmup_status", status, "duration_ms", time.Since(startedAt).Milliseconds())
			} else {
				logger.Info("AI connection warm-up completed", "EID", "AiWarmUp3", "http_status", status, "connection_reused", reused, "duration_ms", time.Since(startedAt).Milliseconds())
			}
		})
	}
	var restartRequests chan string
	if restarts != nil {
		restartRequests = make(chan string, 1)
	}
	restartResult := make(chan string, 1)
	go func() {
		select {
		case binary := <-restartRequests:
			restartResult <- binary
			cancelBroker()
			_ = listener.Close()
		case <-brokerCtx.Done():
			_ = listener.Close()
		}
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
		active.Go(func() {
			handleBrokerConnWithRestart(brokerCtx, conn, token, client, allowedHost, toolCapabilities, restartRequests, instanceID, mcpManager)
		})
	}
	startup.Wait()
	active.Wait()
	if transport, ok := client.Transport.(*http.Transport); ok {
		transport.CloseIdleConnections()
	}
	cancelBroker()
	select {
	case binary := <-restartResult:
		if binary != "" && restarts != nil {
			restarts <- brokerRestart{
				binary: binary,
				args:   append([]string(nil), args...),
				env:    brokerRestartEnvironment(os.Environ(), credentialKey),
			}
		}
	default:
	}
	return 0
}

func brokerRestartEnvironment(environment []string, credentialKey string) []string {
	const key = "ASH_MCP_CREDENTIAL_KEY"
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if strings.HasPrefix(entry, key+"=") {
			continue
		}
		result = append(result, entry)
	}
	return append(result, key+"="+credentialKey)
}

func openBrokerLog(home, sessionID string) (*os.File, error) {
	if err := scratchlife.ValidateSessionID(sessionID); err != nil {
		return nil, fmt.Errorf("invalid broker log session ID: %w", err)
	}
	statePath := workspace.StateRoot(home)
	if err := os.MkdirAll(statePath, 0o700); err != nil {
		return nil, fmt.Errorf("creating broker state directory: %w", err)
	}
	stateRoot, err := os.OpenRoot(statePath)
	if err != nil {
		return nil, fmt.Errorf("opening broker state directory: %w", err)
	}
	defer func() { _ = stateRoot.Close() }()
	logDirectory := filepath.Join("logs", brokerLogDirectory)
	if err := stateRoot.MkdirAll(logDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("creating broker log directory: %w", err)
	}
	logRoot, err := stateRoot.OpenRoot(logDirectory)
	if err != nil {
		return nil, fmt.Errorf("opening broker log directory: %w", err)
	}
	defer func() { _ = logRoot.Close() }()
	// A private directory needs owner execute permission; gosec G302 only
	// accepts file-style 0600 permissions and misflags this required 0700.
	// #nosec G302
	if err := logRoot.Chmod(".", 0o700); err != nil {
		return nil, fmt.Errorf("securing broker log directory: %w", err)
	}
	name := sessionID + ".log"
	if info, err := logRoot.Lstat(name); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("broker log file must be a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("checking broker log file: %w", err)
	}
	file, err := logRoot.OpenFile(name, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening broker log file: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("securing broker log file: %w", err)
	}
	return file, nil
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
		logger.Error(localize.Format("log.broker.mcp_working_directory_failed", []any{err}), "EID", "McPWrk01")
		return
	}
	servers, err := mcp.ResolveRemoteServers(home, cwd, os.ReadFile)
	if err != nil {
		logger.Error(localize.Format("log.broker.mcp_registrations_failed", []any{err}), "EID", "McPWrk02")
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

func handleBrokerConn(ctx context.Context, conn net.Conn, token string, client *http.Client, allowedHost string, toolCapabilities *toolCapabilityCache, mcpManagers ...*mcp.RemoteManager) {
	handleBrokerConnWithRestart(ctx, conn, token, client, allowedHost, toolCapabilities, nil, "", mcpManagers...)
}

func handleBrokerConnWithRestart(ctx context.Context, conn net.Conn, token string, client *http.Client, allowedHost string, toolCapabilities *toolCapabilityCache, restartRequests chan<- string, instanceID string, mcpManagers ...*mcp.RemoteManager) {
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
	if request.BrokerAction != "" {
		response := brokerproto.Response{Version: brokerproto.Version}
		switch request.BrokerAction {
		case "status":
			response.BrokerVersion = version
			response.BrokerInstance = instanceID
		case "restart":
			if restartRequests == nil {
				response.Error = "broker restart is unavailable"
			} else if request.BrokerInstance != instanceID {
				response.Error = "broker instance changed before restart"
			} else if err := validateRestartBinary(request.BrokerBinary); err != nil {
				response.Error = err.Error()
			} else if candidateVersion, err := brokerBinaryVersion(request.BrokerBinary); err != nil {
				response.Error = err.Error()
			} else if candidateVersion != request.BrokerVersion {
				response.Error = "requested broker version does not match the binary"
			} else {
				select {
				case restartRequests <- request.BrokerBinary:
				default:
					response.Error = "broker restart is already in progress"
				}
			}
		default:
			response.Error = "unknown broker action"
		}
		_ = brokerproto.WriteFrame(conn, mustBrokerJSON(response))
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
	if request.ToolCacheAction != "" {
		response := handleToolCapabilityRequest(request, allowedHost, toolCapabilities)
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

func requestBrokerRestart(binary string) error {
	socket := strings.TrimSpace(os.Getenv("ASH_BROKER_SOCKET"))
	token := strings.TrimSpace(os.Getenv("ASH_BROKER_TOKEN"))
	if socket == "" || token == "" {
		return errors.New("this shell has no active broker to restart")
	}
	absoluteBinary, err := filepath.Abs(binary)
	if err != nil {
		return fmt.Errorf("resolving broker binary path: %w", err)
	}
	if err := validateRestartBinary(absoluteBinary); err != nil {
		return err
	}
	candidateVersion, err := brokerBinaryVersion(absoluteBinary)
	if err != nil {
		return err
	}
	current, err := brokerControlRequest(socket, token, brokerproto.Request{BrokerAction: "status"})
	if err != nil || current.Error != "" || current.BrokerInstance == "" {
		return errors.New("the running broker does not support hot restart; restart this shell once, then rebuild")
	}
	response, err := brokerControlRequest(socket, token, brokerproto.Request{
		BrokerAction:   "restart",
		BrokerBinary:   absoluteBinary,
		BrokerVersion:  candidateVersion,
		BrokerInstance: current.BrokerInstance,
	})
	if err != nil {
		return fmt.Errorf("requesting broker restart: %w", err)
	}
	if response.Error != "" {
		if response.Error == "broker request rejected" {
			return errors.New("the running broker does not support hot restart; restart this shell once, then rebuild")
		}
		return fmt.Errorf("broker rejected restart: %s", response.Error)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response, err = brokerControlRequest(socket, token, brokerproto.Request{BrokerAction: "status"})
		if err == nil && response.Error == "" && response.BrokerVersion == candidateVersion && response.BrokerInstance != current.BrokerInstance {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("broker did not report restarted version %q within 10 seconds", candidateVersion)
}

func brokerControlRequest(socket, token string, request brokerproto.Request) (brokerproto.Response, error) {
	request.Version = brokerproto.Version
	request.Token = token
	payload, err := json.Marshal(request)
	if err != nil {
		return brokerproto.Response{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	if err != nil {
		return brokerproto.Response{}, err
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		return brokerproto.Response{}, err
	}
	if err := brokerproto.WriteFrame(conn, payload); err != nil {
		return brokerproto.Response{}, err
	}
	responsePayload, err := brokerproto.ReadFrame(conn)
	if err != nil {
		return brokerproto.Response{}, err
	}
	var response brokerproto.Response
	if err := json.Unmarshal(responsePayload, &response); err != nil {
		return brokerproto.Response{}, err
	}
	if response.Version != brokerproto.Version {
		return brokerproto.Response{}, errors.New("broker protocol version mismatch")
	}
	return response, nil
}

func validateRestartBinary(binary string) error {
	if !filepath.IsAbs(binary) || filepath.Base(binary) != "ash-broker" {
		return errors.New("broker restart requires an absolute ash-broker binary path")
	}
	// #nosec G703 -- the absolute path and exact basename are checked above; the final file is checked for type, ownership, and write permissions below.
	info, err := os.Lstat(binary)
	if err != nil {
		return fmt.Errorf("checking restart binary: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 || info.Mode().Perm()&0o022 != 0 {
		return errors.New("restart binary must be a non-writable executable regular file")
	}
	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("determining current user: %w", err)
	}
	fileOwner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || strconv.FormatUint(uint64(fileOwner.Uid), 10) != currentUser.Uid {
		return errors.New("restart binary must be owned by the current user")
	}
	return nil
}

func brokerBinaryVersion(binary string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// #nosec G702 -- binary has been validated as an owner-only ash-broker executable before this version probe.
	output, err := exec.CommandContext(ctx, binary, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("reading restart binary version: %w", err)
	}
	candidateVersion := strings.TrimSpace(string(output))
	if candidateVersion == "" {
		return "", errors.New("restart binary reported an empty version")
	}
	return candidateVersion, nil
}

func handleToolCapabilityRequest(request brokerproto.Request, allowedHost string, cache *toolCapabilityCache) brokerproto.Response {
	response := brokerproto.Response{Version: brokerproto.Version}
	endpoint, err := url.Parse(strings.TrimSpace(request.ToolCacheEndpoint))
	if err != nil || endpoint.User != nil || endpoint.Host == "" ||
		(endpoint.Scheme != "http" && endpoint.Scheme != "https") ||
		!strings.EqualFold(endpoint.Host, allowedHost) {
		response.Error = "tool capability endpoint rejected"
		return response
	}
	if cache == nil {
		response.Error = "tool capability cache unavailable"
		return response
	}
	switch request.ToolCacheAction {
	case "lookup":
		response.ToolUnsupported, err = cache.unsupported(request.ToolCacheEndpoint, request.ToolCacheModel)
	case "mark_unsupported":
		err = cache.markUnsupported(request.ToolCacheEndpoint, request.ToolCacheModel)
	default:
		response.Error = "unknown tool capability action"
		return response
	}
	if err != nil {
		response.Error = err.Error()
	}
	return response
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
