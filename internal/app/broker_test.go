//go:build darwin || freebsd || linux

package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"ash/internal/brokerproto"
)

// fakeBrokerListener starts a minimal test-double broker that decodes one brokerproto.Request
// per connection, performs the real HTTP call, and encodes the brokerproto.Response back. It
// exists so this package's tests can exercise brokerDo's wire behavior without importing
// cmd/ash-broker (a separate, non-importable package main).
func fakeBrokerListener(t *testing.T, socket string) net.Listener {
	t.Helper()
	_ = os.Remove(socket)
	t.Cleanup(func() { _ = os.Remove(socket) })
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				payload, readErr := brokerproto.ReadFrame(conn)
				if readErr != nil {
					return
				}
				var request brokerproto.Request
				if json.Unmarshal(payload, &request) != nil {
					return
				}
				httpRequest, reqErr := http.NewRequestWithContext(context.Background(), http.MethodPost, request.URL, strings.NewReader(string(request.Body)))
				if reqErr != nil {
					return
				}
				for name, value := range request.Headers {
					httpRequest.Header.Set(name, value)
				}
				httpResponse, doErr := http.DefaultClient.Do(httpRequest)
				if doErr != nil {
					return
				}
				defer func() { _ = httpResponse.Body.Close() }()
				body := make([]byte, 0, 256)
				buf := make([]byte, 256)
				for {
					n, readErr := httpResponse.Body.Read(buf)
					body = append(body, buf[:n]...)
					if readErr != nil {
						break
					}
				}
				responsePayload, _ := json.Marshal(brokerproto.Response{Version: brokerproto.Version, Status: httpResponse.StatusCode, Body: body})
				_ = brokerproto.WriteFrame(conn, responsePayload)
			}()
		}
	}()
	return listener
}

func TestBrokerRoundTripReusesConfiguredTransport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.Header.Get("Authorization") != "Bearer test" {
			t.Fatalf("unexpected request: %s %q", request.Method, request.Header)
		}
		_, _ = writer.Write([]byte(`{"message":{"role":"assistant","content":"ok"}}`))
	}))
	defer server.Close()

	socket := "/tmp/ash-broker-test-" + strconv.Itoa(os.Getpid()) + ".sock"
	fakeBrokerListener(t, socket)

	t.Setenv(brokerSocketEnv, socket)
	t.Setenv(brokerTokenEnv, "test-token")
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
}

func TestBrokerDoDropsSDKTelemetryHeadersInsteadOfFailing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test" {
			t.Errorf("authorization header = %q, want forwarded", request.Header.Get("Authorization"))
		}
		if got := request.Header.Get("X-Stainless-Lang"); got != "" {
			t.Errorf("X-Stainless-Lang = %q, want dropped", got)
		}
		_, _ = writer.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	socket := "/tmp/ash-broker-test-headers-" + strconv.Itoa(os.Getpid()) + ".sock"
	fakeBrokerListener(t, socket)

	t.Setenv(brokerSocketEnv, socket)
	t.Setenv(brokerTokenEnv, "test-token")
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer test")
	request.Header.Set("X-Stainless-Lang", "go")
	request.Header.Set("X-Stainless-Retry-Count", "0")
	response, _, err := brokerDo(context.Background(), request)
	if err != nil {
		t.Fatalf("brokerDo returned %v, want telemetry headers dropped and request forwarded", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
}

func TestBrokerDoReturnsPromptlyOnContextCancel(t *testing.T) {
	socket := "/tmp/ash-broker-test-cancel-" + strconv.Itoa(os.Getpid()) + ".sock"
	_ = os.Remove(socket)
	t.Cleanup(func() { _ = os.Remove(socket) })
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()

	serverReadRequest := make(chan struct{})
	releaseServer := make(chan struct{})
	defer close(releaseServer)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = connection.Close() }()
		_, _ = brokerproto.ReadFrame(connection)
		close(serverReadRequest)
		<-releaseServer
	}()

	t.Setenv(brokerSocketEnv, socket)
	t.Setenv(brokerTokenEnv, "test-token")
	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.example.com/v1/chat", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer test")

	result := make(chan error, 1)
	go func() {
		_, _, err := brokerDo(ctx, request)
		result <- err
	}()

	select {
	case <-serverReadRequest:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for broker request frame")
	}
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("brokerDo did not return promptly after context cancellation")
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
