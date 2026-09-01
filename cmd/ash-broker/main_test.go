//go:build darwin || freebsd || linux

package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

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
