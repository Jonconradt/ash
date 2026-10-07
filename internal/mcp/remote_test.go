package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

type memoryCredentialStorage struct {
	mu      sync.Mutex
	records map[string][]byte
}

func (s *memoryCredentialStorage) Get(server string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.records[server]
	if !ok {
		return nil, errCredentialNotFound
	}
	return append([]byte(nil), value...), nil
}

func (s *memoryCredentialStorage) Set(server string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.records == nil {
		s.records = make(map[string][]byte)
	}
	s.records[server] = append([]byte(nil), value...)
	return nil
}

type unavailableKeyring struct{}

func (unavailableKeyring) Get(string, string) (string, error) {
	return "", errors.New("credential store unavailable")
}

func (unavailableKeyring) Set(string, string, string) error {
	return errors.New("credential store unavailable")
}

func TestEncryptedCredentialStorageRoundTrip(t *testing.T) {
	directory := t.TempDir()
	store := &systemCredentialStorage{directory: directory, key: make([]byte, 32), native: unavailableKeyring{}}
	want := []byte(`{"refresh_token":"secret"}`)
	if err := store.Set("example", want); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	got, err := store.Get("example")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("Get() = %q, want %q", got, want)
	}
	path := filepath.Join(directory, "mcp-credentials.enc")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("credential file permissions = %o, want 600", got)
	}
	ciphertext, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if bytes.Contains(ciphertext, []byte("refresh_token")) {
		t.Fatal("encrypted credential file contains plaintext token data")
	}

	wrongKey := &systemCredentialStorage{directory: directory, key: make([]byte, 32), native: unavailableKeyring{}}
	wrongKey.key[0] = 1
	if _, err := wrongKey.Get("example"); err == nil {
		t.Fatal("Get() with the wrong key succeeded")
	}
}

func TestCredentialStorageRejectsWeakKey(t *testing.T) {
	for _, key := range []string{"short", "0123456789"} {
		if _, err := NewCredentialStorage(t.TempDir(), key); err == nil {
			t.Fatalf("NewCredentialStorage(%q) succeeded", key)
		}
	}
}

func TestValidateRemoteServer(t *testing.T) {
	tests := []struct {
		name    string
		server  RemoteServer
		wantErr bool
	}{
		{name: "https", server: RemoteServer{Name: "remote", URL: "https://example.com/mcp"}},
		{name: "localhost http", server: RemoteServer{Name: "local", URL: "http://localhost:8080/mcp"}},
		{name: "external http", server: RemoteServer{Name: "remote", URL: "http://example.com/mcp"}, wantErr: true},
		{name: "credentials in URL", server: RemoteServer{Name: "remote", URL: "https://user:password@example.com/mcp"}, wantErr: true},
		{name: "query in URL", server: RemoteServer{Name: "remote", URL: "https://example.com/mcp?key=value"}, wantErr: true},
		{name: "invalid name", server: RemoteServer{Name: "../remote", URL: "https://example.com/mcp"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateRemoteServer(test.server)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateRemoteServer() error = %v, wantErr %t", err, test.wantErr)
			}
		})
	}
}

func TestToolFunctionNameIsStableAndQualified(t *testing.T) {
	first := ToolFunctionName("server one", "tool/a")
	if first != ToolFunctionName("server one", "tool/a") {
		t.Fatalf("tool name changed between calls: %q", first)
	}
	if first == ToolFunctionName("server", "one_tool/a") {
		t.Fatalf("different server/tool pairs collided: %q", first)
	}
	if len(first) > 64 {
		t.Fatalf("tool function name length = %d, want at most 64", len(first))
	}
}

func TestRemoteManagerHTTPToolRoundTrip(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "echo",
		Description: "Returns the input string",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}},
	}, func(_ context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: args["value"].(string)}}}, nil, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	manager := NewRemoteManager(ctx, &systemCredentialStorage{directory: t.TempDir(), key: make([]byte, 32), native: unavailableKeyring{}})
	defer manager.Close()
	status, err := manager.Start(ctx, RemoteServer{Name: "test", URL: httpServer.URL})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	for status.State != "connected" && status.Error == "" {
		status, err = manager.Status(ctx, "test")
		if err != nil {
			t.Fatalf("Status() error = %v", err)
		}
	}
	if status.State != "connected" {
		t.Fatalf("session status = %+v, want connected", status)
	}
	tools, err := manager.ListTools("test")
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("ListTools() = %+v, want echo", tools)
	}
	result, err := manager.CallTool(ctx, "test", "echo", map[string]any{"value": "hello"})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].(*mcp.TextContent).Text != "hello" {
		t.Fatalf("CallTool() result = %+v, want hello", result)
	}
}

func TestRemoteManagerOAuthAuthorizationCodeFlow(t *testing.T) {
	var mu sync.Mutex
	var codeChallenge string
	var tokenRefreshes int
	oauthMux := http.NewServeMux()
	oauthMux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		base := "http://" + r.Host
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                base,
			"authorization_endpoint":                base + "/authorize",
			"token_endpoint":                        base + "/token",
			"registration_endpoint":                 base + "/register",
			"response_types_supported":              []string{"code"},
			"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
			"code_challenge_methods_supported":      []string{"S256"},
			"token_endpoint_auth_methods_supported": []string{"none"},
		})
	})
	oauthMux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var registration struct {
			RedirectURIs []string `json:"redirect_uris"`
		}
		if err := json.NewDecoder(r.Body).Decode(&registration); err != nil {
			http.Error(w, "bad registration", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id":                  "test-client",
			"redirect_uris":              registration.RedirectURIs,
			"token_endpoint_auth_method": "none",
		})
	})
	oauthMux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		codeChallenge = r.URL.Query().Get("code_challenge")
		mu.Unlock()
	})
	oauthMux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if r.Form.Get("grant_type") == "authorization_code" {
			verifier := r.Form.Get("code_verifier")
			if verifier == "" || oauth2.S256ChallengeFromVerifier(verifier) != codeChallenge {
				http.Error(w, "PKCE verification failed", http.StatusBadRequest)
				return
			}
		} else if r.Form.Get("grant_type") == "refresh_token" {
			tokenRefreshes++
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  fmt.Sprintf("token-%d", tokenRefreshes+1),
			"token_type":    "Bearer",
			"refresh_token": "refresh-token",
			"expires_in":    1,
		})
	})
	oauthServer := httptest.NewServer(oauthMux)
	defer oauthServer.Close()

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "oauth-test", Version: "1"}, nil)
	mcp.AddTool(mcpServer, &mcp.Tool{Name: "secure_echo"}, func(_ context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: args["value"].(string)}}}, nil, nil
	})
	var remoteURL string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-protected-resource/mcp" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resource":              remoteURL,
				"authorization_servers": []string{oauthServer.URL},
				"scopes_supported":      []string{"mcp:read"},
			})
			return
		}
		if r.URL.Path == "/mcp" && !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer token-") {
			origin := strings.TrimSuffix(remoteURL, "/mcp")
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/mcp"`, origin))
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		http.Handler(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
			return mcpServer
		}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})).ServeHTTP(w, r)
	})
	remoteHTTPServer := httptest.NewServer(handler)
	defer remoteHTTPServer.Close()
	remoteURL = remoteHTTPServer.URL + "/mcp"

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	get := func(target string) (*http.Response, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return nil, err
		}
		return http.DefaultClient.Do(request)
	}
	origin := strings.TrimSuffix(remoteURL, "/mcp")
	metadata, err := oauthex.GetProtectedResourceMetadata(ctx, origin+"/.well-known/oauth-protected-resource/mcp", remoteURL, http.DefaultClient)
	if err != nil {
		t.Fatalf("GetProtectedResourceMetadata() error = %v", err)
	}
	if len(metadata.AuthorizationServers) != 1 || metadata.AuthorizationServers[0] != oauthServer.URL {
		t.Fatalf("authorization servers = %+v, want %s", metadata.AuthorizationServers, oauthServer.URL)
	}
	store := &memoryCredentialStorage{}
	manager := NewRemoteManager(ctx, store)
	defer manager.Close()
	status, err := manager.Start(ctx, RemoteServer{Name: "oauth-test", URL: remoteURL})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if status.AuthURL == "" {
		t.Fatalf("Start() status = %+v, want authorization URL", status)
	}
	authURL, err := url.Parse(status.AuthURL)
	if err != nil {
		t.Fatalf("parsing authorization URL: %v", err)
	}
	state := authURL.Query().Get("state")
	if state == "" || authURL.Query().Get("code_challenge") == "" || authURL.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("authorization URL missing PKCE/state parameters: %s", status.AuthURL)
	}
	redirectURL, err := url.Parse(authURL.Query().Get("redirect_uri"))
	if err != nil {
		t.Fatalf("parsing redirect URI: %v", err)
	}
	callbackURL := *redirectURL
	authorizationResponse, err := get(status.AuthURL)
	if err != nil {
		t.Fatalf("opening authorization endpoint: %v", err)
	}
	_ = authorizationResponse.Body.Close()
	mu.Lock()
	wantChallenge := codeChallenge
	mu.Unlock()
	if wantChallenge != authURL.Query().Get("code_challenge") {
		t.Fatalf("authorization endpoint code challenge = %q, want %q", wantChallenge, authURL.Query().Get("code_challenge"))
	}
	query := callbackURL.Query()
	query.Set("code", "invalid-state-code")
	query.Set("state", "wrong-state")
	callbackURL.RawQuery = query.Encode()
	invalidCallback, err := get(callbackURL.String())
	if err != nil {
		t.Fatalf("calling OAuth callback with invalid state: %v", err)
	}
	_ = invalidCallback.Body.Close()
	if invalidCallback.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid-state callback status = %d, want 400", invalidCallback.StatusCode)
	}
	query.Set("code", "test-code")
	query.Set("state", state)
	callbackURL.RawQuery = query.Encode()
	callbackResponse, err := get(callbackURL.String())
	if err != nil {
		t.Fatalf("calling OAuth callback: %v", err)
	}
	_ = callbackResponse.Body.Close()
	if callbackResponse.StatusCode != http.StatusOK {
		t.Fatalf("OAuth callback status = %d, want 200", callbackResponse.StatusCode)
	}
	replayResponse, err := get(callbackURL.String())
	if err != nil {
		t.Fatalf("replaying OAuth callback: %v", err)
	}
	_ = replayResponse.Body.Close()
	if replayResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("OAuth callback replay status = %d, want 400", replayResponse.StatusCode)
	}
	if err := manager.AcknowledgeAuth("oauth-test"); err != nil {
		t.Fatalf("AcknowledgeAuth() error = %v", err)
	}
	status, err = manager.Status(ctx, "oauth-test")
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.State != "connected" {
		t.Fatalf("session status = %+v, want connected", status)
	}
	storageID := credentialID(RemoteServer{Name: "oauth-test", URL: remoteURL})
	stored, err := store.Get(storageID)
	if err != nil || !strings.Contains(string(stored), `"refresh_token":"refresh-token"`) {
		t.Fatalf("stored OAuth credentials = %s, err = %v", stored, err)
	}
	tools, err := manager.ListTools("oauth-test")
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "secure_echo" {
		t.Fatalf("ListTools() = %+v, want secure_echo", tools)
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := manager.CallTool(ctx, "oauth-test", "secure_echo", map[string]any{"value": "refreshed"}); err != nil {
		t.Fatalf("CallTool() after token expiry error = %v", err)
	}
	mu.Lock()
	refreshes := tokenRefreshes
	mu.Unlock()
	if refreshes == 0 {
		t.Fatal("OAuth token was not refreshed after expiry")
	}
	stored, err = store.Get(storageID)
	if err != nil || !strings.Contains(string(stored), `"access_token":"token-`) {
		t.Fatalf("refreshed OAuth credentials = %s, err = %v", stored, err)
	}
}

func TestRemoteMCPPlaygroundSmoke(t *testing.T) {
	if os.Getenv("ASH_MCP_PLAYGROUND_SMOKE") != "1" {
		t.Skip("set ASH_MCP_PLAYGROUND_SMOKE=1 to test the public remote MCP endpoint")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	manager := NewRemoteManager(ctx, &systemCredentialStorage{directory: t.TempDir(), key: make([]byte, 32), native: unavailableKeyring{}})
	defer manager.Close()
	status, err := manager.Start(ctx, RemoteServer{Name: "playground", URL: "https://mcpplaygroundonline.com/mcp-complex-server"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	for status.State != "connected" && status.Error == "" && status.AuthURL == "" {
		status, err = manager.Status(ctx, "playground")
		if err != nil {
			t.Fatalf("Status() error = %v", err)
		}
	}
	if status.State != "connected" {
		t.Fatalf("public smoke server did not connect without OAuth: %+v", status)
	}
	tools, err := manager.ListTools("playground")
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("public smoke server returned no tools")
	}
}
