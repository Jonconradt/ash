//go:build darwin || freebsd || linux

package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ash/internal/brokerproto"
)

func TestRunBrokerRequiresParentPID(t *testing.T) {
	t.Setenv(brokerTokenEnv, "test-token")
	var stdout, stderr strings.Builder
	socket := "/tmp/ash-broker-test-" + strconv.Itoa(os.Getpid()) + "-parentpid.sock"
	if code := runBroker(context.Background(), []string{"--socket", socket}, &stdout, &stderr); code != 2 {
		t.Fatalf("runBroker without parent PID = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "--parent-pid") {
		t.Fatalf("expected parent PID error, got %q", stderr.String())
	}
}

func TestBrokerSocketReadyWhileMCPStartupIsBlocked(t *testing.T) {
	home := t.TempDir()
	registrationPath := filepath.Join(home, ".ash", ".ash_allow")
	if err := os.MkdirAll(filepath.Dir(registrationPath), 0o700); err != nil {
		t.Fatal(err)
	}
	mcpStarted := make(chan struct{}, 1)
	releaseMCP := make(chan struct{})
	var releaseOnce sync.Once
	mcpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		select {
		case mcpStarted <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-releaseMCP:
		}
	}))
	defer func() {
		releaseOnce.Do(func() { close(releaseMCP) })
		mcpServer.Close()
	}()
	if err := os.WriteFile(registrationPath, []byte(mcpServer.URL+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	aiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("AI request method = %s, want POST", r.Method)
		}
		_, _ = w.Write([]byte("ready"))
	}))
	defer aiServer.Close()

	t.Setenv("HOME", home)
	t.Setenv(brokerTokenEnv, "startup-test-token")
	t.Setenv(aiEndpointEnv, aiServer.URL)
	t.Setenv(aiWarmupEnv, "0")
	t.Setenv("ASH_MCP_CREDENTIAL_KEY", strings.Repeat("0", 64))
	socket := filepath.Join(os.TempDir(), "ash-startup-"+strconv.Itoa(os.Getpid())+".sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runResult := make(chan int, 1)
	var stderr strings.Builder
	go func() {
		runResult <- runBroker(ctx, []string{
			"--socket", socket, "--session-id", "startup-test", "--parent-pid", strconv.Itoa(os.Getpid()),
		}, io.Discard, &stderr)
	}()

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer waitCancel()
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		select {
		case <-waitCtx.Done():
			t.Fatal("broker socket did not become ready")
		case result := <-runResult:
			t.Fatalf("broker exited before socket readiness with code %d: %s", result, stderr.String())
		case <-time.After(10 * time.Millisecond):
		}
	}
	sessionLock := filepath.Join(home, ".ash", "scratch", "startup-test", ".ash_scratch_lock")
	if _, err := os.Stat(sessionLock); err != nil {
		t.Fatalf("broker socket became ready without its scratch lock: %v", err)
	}
	select {
	case <-mcpStarted:
	case <-waitCtx.Done():
		t.Fatal("configured MCP startup did not reach the blocked server")
	}

	response := dialBroker(t, socket, brokerproto.Request{
		Version: brokerproto.Version, Token: "startup-test-token",
		URL: aiServer.URL, Body: []byte("request"),
	})
	if response.Error != "" || response.Status != http.StatusOK || string(response.Body) != "ready" {
		t.Fatalf("AI request while MCP startup was blocked = %+v", response)
	}

	cancel()
	select {
	case code := <-runResult:
		if code != 0 {
			t.Fatalf("runBroker() = %d; want clean shutdown: %s", code, stderr.String())
		}
	case <-time.After(8 * time.Second):
		t.Fatal("broker did not stop after cancellation with MCP startup blocked")
	}
	if _, err := os.Stat(sessionLock); !os.IsNotExist(err) {
		t.Fatalf("broker left its last-holder scratch lock behind: %v", err)
	}
}

func TestBrokerParentAlive(t *testing.T) {
	if !brokerParentAlive(os.Getpid()) {
		t.Fatal("expected current process to be alive")
	}
	if brokerParentAlive(0) {
		t.Fatal("expected zero PID to be rejected")
	}
}

func TestBrokerHTTPClientRetainsBoundedIdleConnections(t *testing.T) {
	client := newBrokerHTTPClient()
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", client.Transport)
	}
	if transport.IdleConnTimeout != 0 {
		t.Fatalf("IdleConnTimeout = %s, want 0", transport.IdleConnTimeout)
	}
	if transport.MaxIdleConns != 32 || transport.MaxIdleConnsPerHost != 8 || transport.MaxConnsPerHost != 16 {
		t.Fatalf("unexpected broker connection limits: idle=%d idle_per_host=%d per_host=%d", transport.MaxIdleConns, transport.MaxIdleConnsPerHost, transport.MaxConnsPerHost)
	}
}

// dialBroker sends one brokerproto.Request to the listener and returns the decoded response.
func dialBroker(t *testing.T, socket string, req brokerproto.Request) brokerproto.Response {
	t.Helper()
	conn, err := (&net.Dialer{}).DialContext(context.Background(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := brokerproto.WriteFrame(conn, payload); err != nil {
		t.Fatal(err)
	}
	responsePayload, err := brokerproto.ReadFrame(conn)
	if err != nil {
		t.Fatal(err)
	}
	var response brokerproto.Response
	if err := json.Unmarshal(responsePayload, &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestHandleBrokerConnRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test" {
			t.Fatalf("unexpected headers: %v", request.Header)
		}
		_, _ = writer.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	socket := "/tmp/ash-broker-test-" + strconv.Itoa(os.Getpid()) + "-roundtrip.sock"
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(socket) }()
	defer func() { _ = listener.Close() }()
	client := newBrokerHTTPClient()
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go handleBrokerConn(context.Background(), conn, "test-token", client, serverURL.Host)
		}
	}()

	response := dialBroker(t, socket, brokerproto.Request{
		Version: brokerproto.Version,
		Token:   "test-token",
		URL:     server.URL,
		Headers: map[string]string{"Authorization": "Bearer test"},
	})
	if response.Error != "" {
		t.Fatalf("unexpected broker error: %s", response.Error)
	}
	if response.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Status)
	}
}

func TestHandleBrokerConnRejectsUnconfiguredHost(t *testing.T) {
	socket := "/tmp/ash-broker-test-" + strconv.Itoa(os.Getpid()) + "-badhost.sock"
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(socket) }()
	defer func() { _ = listener.Close() }()
	client := newBrokerHTTPClient()
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go handleBrokerConn(context.Background(), conn, "test-token", client, "api.example.com")
		}
	}()

	response := dialBroker(t, socket, brokerproto.Request{
		Version: brokerproto.Version,
		Token:   "test-token",
		URL:     "https://attacker.example.com/v1/chat",
	})
	if response.Error == "" {
		t.Fatal("expected request to unconfigured host to be rejected")
	}
}

func TestHandleBrokerConnRejectsBadToken(t *testing.T) {
	socket := "/tmp/ash-broker-test-" + strconv.Itoa(os.Getpid()) + "-badtoken.sock"
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(socket) }()
	defer func() { _ = listener.Close() }()
	client := newBrokerHTTPClient()
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go handleBrokerConn(context.Background(), conn, "real-token", client, "api.example.com")
		}
	}()

	response := dialBroker(t, socket, brokerproto.Request{
		Version: brokerproto.Version,
		Token:   "wrong-token",
		URL:     "https://api.example.com/v1/chat",
	})
	if response.Error == "" {
		t.Fatal("expected mismatched token to be rejected")
	}
}

func TestAITimeoutDefaultAndOverride(t *testing.T) {
	t.Setenv(aiTimeoutEnv, "")
	if got := aiTimeout(); got != defaultAITimeout {
		t.Fatalf("aiTimeout() = %v, want default %v", got, defaultAITimeout)
	}
	t.Setenv(aiTimeoutEnv, "5s")
	if got := aiTimeout(); got.String() != "5s" {
		t.Fatalf("aiTimeout() = %v, want 5s", got)
	}
}

func TestAIWarmupSetting(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
		want  bool
		err   bool
	}{
		{name: "unset default", value: "", want: true},
		{name: "whitespace default", value: "  ", want: true},
		{name: "explicitly disabled", value: "0", want: false},
		{name: "explicitly enabled", value: "1", want: true},
		{name: "invalid", value: "true", err: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(aiWarmupEnv, test.value)
			got, err := aiWarmupEnabled()
			if (err != nil) != test.err || got != test.want {
				t.Fatalf("aiWarmupEnabled() = %t, %v; want %t, err=%t", got, err, test.want, test.err)
			}
		})
	}
}

func TestWarmAIConnectionUsesSharedTransportWithoutCredentials(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method == http.MethodHead {
			if r.URL.RequestURI() != "/" {
				t.Errorf("warm-up request URI = %q, want root path without endpoint credentials", r.URL.RequestURI())
			}
			if r.Header.Get("Authorization") != "" || r.Header.Get("x-api-key") != "" {
				t.Errorf("warm-up sent credentials: %v", r.Header)
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("request method = %s, want POST", r.Method)
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	client := newBrokerHTTPClient()
	defer client.Transport.(*http.Transport).CloseIdleConnections()
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	endpoint.Path = "/v1/chat"
	endpoint.RawQuery = "api_key=secret"
	status, reused, err := warmAIConnection(context.Background(), client, endpoint.String())
	if err != nil {
		t.Fatalf("warmAIConnection() error = %v", err)
	}
	if status != http.StatusMethodNotAllowed || reused {
		t.Fatalf("warmAIConnection() = status %d, reused %t; want 405 and a new connection", status, reused)
	}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	var requestReused bool
	trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { requestReused = info.Reused }}
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), trace))
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("POST after warm-up: %v", err)
	}
	_ = response.Body.Close()
	if !requestReused {
		t.Fatal("request after warm-up did not reuse the broker's HTTP connection")
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("server request count = %d, want one HEAD and one POST", got)
	}
}

func TestWarmAIConnectionDoesNotFollowRedirects(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirected.Add(1)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("request method = %s, want HEAD", r.Method)
		}
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	client := newBrokerHTTPClient()
	defer client.Transport.(*http.Transport).CloseIdleConnections()
	status, _, err := warmAIConnection(context.Background(), client, redirect.URL)
	if err != nil {
		t.Fatalf("warmAIConnection() error = %v", err)
	}
	if status != http.StatusTemporaryRedirect {
		t.Fatalf("warm-up status = %d, want 307", status)
	}
	if got := redirected.Load(); got != 0 {
		t.Fatalf("redirect target received %d requests, want none", got)
	}
}

func TestWarmAIConnectionReusesHTTP2Transport(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	client := newBrokerHTTPClient()
	defer client.Transport.(*http.Transport).CloseIdleConnections()
	transport := client.Transport.(*http.Transport)
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	}
	testTransport := server.Client().Transport.(*http.Transport)
	transport.TLSClientConfig.RootCAs = testTransport.TLSClientConfig.RootCAs
	transport.ForceAttemptHTTP2 = true

	status, _, err := warmAIConnection(context.Background(), client, server.URL)
	if err != nil || status != http.StatusMethodNotAllowed {
		t.Fatalf("warmAIConnection() = %d, %v; want 405", status, err)
	}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	var reused bool
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused },
	}))
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("POST after HTTP/2 warm-up: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.ProtoMajor != 2 || !reused {
		t.Fatalf("POST protocol/reuse = HTTP/%d, %t; want HTTP/2 reused", response.ProtoMajor, reused)
	}
}

func TestWarmAIConnectionRecoversAfterUpstreamClosesIdleConnection(t *testing.T) {
	idleClosed := make(chan struct{})
	var closeFirstIdle atomic.Bool
	var accepted atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	server.Config.ConnState = func(conn net.Conn, state http.ConnState) {
		if state == http.StateNew {
			accepted.Add(1)
		}
		if state == http.StateIdle && closeFirstIdle.CompareAndSwap(false, true) {
			close(idleClosed)
			time.AfterFunc(20*time.Millisecond, func() { _ = conn.Close() })
		}
	}
	server.Start()
	defer server.Close()

	client := newBrokerHTTPClient()
	defer client.Transport.(*http.Transport).CloseIdleConnections()
	if _, _, err := warmAIConnection(context.Background(), client, server.URL); err != nil {
		t.Fatalf("warmAIConnection() error = %v", err)
	}
	select {
	case <-idleClosed:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not close the warmed connection while idle")
	}
	time.Sleep(40 * time.Millisecond)
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("POST after upstream closed idle connection: %v", err)
	}
	_ = response.Body.Close()
	if got := accepted.Load(); got < 2 {
		t.Fatalf("accepted connections = %d, want a replacement connection", got)
	}
}

func BenchmarkBrokerAITransportReuse(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client := newBrokerHTTPClient()
	defer client.Transport.(*http.Transport).CloseIdleConnections()
	if _, _, err := warmAIConnection(context.Background(), client, server.URL); err != nil {
		b.Fatalf("warmAIConnection(): %v", err)
	}

	var reused int64
	b.ResetTimer()
	for range b.N {
		request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL, strings.NewReader("{}"))
		if err != nil {
			b.Fatalf("creating POST request: %v", err)
		}
		trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
			if info.Reused {
				reused++
			}
		}}
		request = request.WithContext(httptrace.WithClientTrace(request.Context(), trace))
		response, err := client.Do(request)
		if err != nil {
			b.Fatalf("sending POST request: %v", err)
		}
		if _, err := io.Copy(io.Discard, response.Body); err != nil {
			b.Fatalf("draining POST response: %v", err)
		}
		if err := response.Body.Close(); err != nil {
			b.Fatalf("closing POST response: %v", err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(reused)/float64(b.N), "reuse/op")
}
