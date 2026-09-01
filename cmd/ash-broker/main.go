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
)

const (
	brokerTokenEnv       = "ASH_BROKER_TOKEN"
	aiEndpointEnv        = "AI_ENDPOINT"
	aiTimeoutEnv         = "AI_TIMEOUT"
	defaultAITimeout     = 3 * time.Minute
	brokerParentPollTick = 5 * time.Second
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
	endpointURL, err := url.Parse(endpoint)
	if err != nil || endpointURL.Host == "" {
		logger.Error("ash-broker requires a valid AI_ENDPOINT host", "EID", "Ft6BwX2n")
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
	var active sync.WaitGroup
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	go func() {
		ticker := time.NewTicker(brokerParentPollTick)
		defer ticker.Stop()
		for range ticker.C {
			if !brokerParentAlive(parentPID) {
				_ = listener.Close()
				return
			}
		}
	}()
	for {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			break
		}
		active.Add(1)
		go func() { defer active.Done(); handleBrokerConn(ctx, conn, token, client, allowedHost) }()
	}
	active.Wait()
	return 0
}

func brokerParentAlive(parentPID int) bool {
	if parentPID <= 0 {
		return false
	}
	err := syscall.Kill(parentPID, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func handleBrokerConn(ctx context.Context, conn net.Conn, token string, client *http.Client, allowedHost string) {
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
	if json.Unmarshal(payload, &request) != nil || request.Version != brokerproto.Version || subtle.ConstantTimeCompare([]byte(request.Token), []byte(token)) != 1 || !brokerproto.URLAllowed(request.URL, allowedHost) || len(request.Body) > brokerproto.MaxBody {
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
