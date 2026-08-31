//go:build darwin || freebsd || linux

package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestBrokerFrameLimit(t *testing.T) {
	if _, err := readBrokerFrame(strings.NewReader("\xff\xff\xff\xff")); err == nil {
		t.Fatal("expected oversized frame to be rejected")
	}
}

func TestRunBrokerRequiresParentPID(t *testing.T) {
	t.Setenv(brokerTokenEnv, "test-token")
	var stdout, stderr strings.Builder
	if code := runBroker([]string{"--socket", filepath.Join(t.TempDir(), "broker.sock")}, &stdout, &stderr); code != 2 {
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

func TestBrokerRoundTripReusesConfiguredTransport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.Header.Get("Authorization") != "Bearer test" {
			t.Fatalf("unexpected request: %s %q", request.Method, request.Header)
		}
		_, _ = writer.Write([]byte(`{"message":{"role":"assistant","content":"ok"}}`))
	}))
	defer server.Close()

	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	allowedHost := serverURL.Host

	socket := "/tmp/ash-broker-test-" + strconv.Itoa(os.Getpid()) + ".sock"
	_ = os.Remove(socket)
	t.Cleanup(func() { _ = os.Remove(socket) })
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	token := "test-token"
	client := newBrokerHTTPClient()
	go func() {
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go handleBrokerConn(context.Background(), connection, token, client, allowedHost)
		}
	}()

	t.Setenv(brokerSocketEnv, socket)
	t.Setenv(brokerTokenEnv, token)
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer test")
	response, _, err := brokerDo(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	if _, err := os.Stat(socket); err != nil {
		t.Fatal(err)
	}
}

func TestBrokerURLAllowedPinsToConfiguredHost(t *testing.T) {
	if brokerURLAllowed("https://api.example.com/v1/chat", "api.example.com") != true {
		t.Fatal("expected matching host to be allowed")
	}
	if brokerURLAllowed("https://attacker.example.com/v1/chat", "api.example.com") {
		t.Fatal("expected mismatched host to be rejected")
	}
	if brokerURLAllowed("https://user@api.example.com/v1/chat", "api.example.com") {
		t.Fatal("expected embedded userinfo to be rejected")
	}
}

func TestHandleBrokerConnRejectsUnconfiguredHost(t *testing.T) {
	socket := "/tmp/ash-broker-test-host-" + strconv.Itoa(os.Getpid()) + ".sock"
	_ = os.Remove(socket)
	t.Cleanup(func() { _ = os.Remove(socket) })
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	token := "test-token"
	client := newBrokerHTTPClient()
	go func() {
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go handleBrokerConn(context.Background(), connection, token, client, "api.example.com")
		}
	}()

	t.Setenv(brokerSocketEnv, socket)
	t.Setenv(brokerTokenEnv, token)
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://attacker.example.com/v1/chat", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := brokerDo(context.Background(), request); err == nil {
		t.Fatal("expected request to unconfigured host to be rejected")
	}
}

func TestChatFallsBackWhenBrokerUnavailable(t *testing.T) {
	t.Setenv(brokerSocketEnv, filepath.Join(t.TempDir(), "missing.sock"))
	t.Setenv(brokerTokenEnv, "test-token")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(`{"message":{"role":"assistant","content":"fallback"}}`))
	}))
	defer server.Close()

	response, err := chat(context.Background(), testAIConfig(server.URL, "model"), []message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.Message.Content != "fallback" {
		t.Fatalf("content = %q, want fallback", response.Message.Content)
	}
}
