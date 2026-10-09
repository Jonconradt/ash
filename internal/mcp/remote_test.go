package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ash/internal/brokerproto"
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

func TestRemoteManagerStatusNowReturnsQueuedAuthorizationURL(t *testing.T) {
	manager := NewRemoteManager(context.Background(), nil)
	session := &remoteSession{
		status:   RemoteStatus{State: "connecting"},
		authURL:  make(chan string, 1),
		callback: make(chan authorizationCallback, 1),
		done:     make(chan struct{}),
	}
	session.authURL <- "https://auth.example/authorize"
	manager.sessions["test"] = session

	status, err := manager.StatusNow("test")
	if err != nil {
		t.Fatalf("StatusNow() error = %v", err)
	}
	if status.State != "authorizing" || status.AuthURL != "https://auth.example/authorize" {
		t.Fatalf("StatusNow() = %+v, want queued authorization URL", status)
	}
	if err := manager.AcknowledgeAuth("test"); err != nil {
		t.Fatalf("AcknowledgeAuth() error = %v", err)
	}
	status, err = manager.StatusNow("test")
	if err != nil {
		t.Fatalf("StatusNow() after acknowledgment error = %v", err)
	}
	if status.State != "authorizing" || status.AuthURL != "" {
		t.Fatalf("StatusNow() after acknowledgment = %+v, want authorizing without URL", status)
	}
	if _, err := manager.StatusNow("missing"); err == nil {
		t.Fatal("StatusNow() accepted an unknown session")
	}
}

func TestRemoteManagerRejectsURLChangeBeforeRetryingFailedSession(t *testing.T) {
	manager := NewRemoteManager(context.Background(), nil)
	sessionCtx, cancel := context.WithCancel(manager.ctx)
	session := &remoteSession{
		config:   RemoteServer{Name: "remote", URL: "https://old.example/mcp"},
		ctx:      sessionCtx,
		cancel:   cancel,
		status:   RemoteStatus{State: "error", Error: "temporary failure"},
		retryAt:  time.Now().Add(-time.Second),
		authURL:  make(chan string, 1),
		callback: make(chan authorizationCallback, 1),
		done:     make(chan struct{}),
	}
	manager.sessions[session.config.Name] = session

	_, err := manager.Start(context.Background(), RemoteServer{Name: "remote", URL: "https://new.example/mcp"})
	if err == nil || !strings.Contains(err.Error(), "different URL") {
		t.Fatalf("Start() error = %v, want server URL mismatch", err)
	}
	if manager.sessions[session.config.Name] != session {
		t.Fatal("URL mismatch replaced the retryable session")
	}
	manager.Close()
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

func TestPersistedTokenSourceCoordinatesConcurrentRefresh(t *testing.T) {
	var refreshes atomic.Int32
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "old-refresh-token" {
			http.Error(w, "unexpected refresh request", http.StatusBadRequest)
			return
		}
		refreshes.Add(1)
		time.Sleep(25 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"new-access-token","refresh_token":"new-refresh-token","token_type":"Bearer","expires_in":3600}`)
	}))
	defer tokenServer.Close()

	store := &systemCredentialStorage{directory: t.TempDir(), key: make([]byte, 32), native: unavailableKeyring{}}
	key := "mcp-concurrent-refresh"
	record := persistedOAuth{
		Config: oauth2.Config{
			ClientID: "test-client",
			Endpoint: oauth2.Endpoint{TokenURL: tokenServer.URL},
		},
		Token: oauth2.Token{
			AccessToken:  "expired-access-token",
			RefreshToken: "old-refresh-token",
			TokenType:    "Bearer",
			Expiry:       time.Now().Add(-time.Minute),
		},
	}
	if err := savePersistedOAuth(store, key, record); err != nil {
		t.Fatalf("saving expired credentials: %v", err)
	}

	newSource := func() oauth2.TokenSource {
		tokenSource := record.Config.TokenSource(context.Background(), &record.Token)
		return newPersistedTokenSource(tokenSource, record, store, key, context.Background())
	}
	sources := []oauth2.TokenSource{newSource(), newSource()}
	results := make(chan *oauth2.Token, len(sources))
	errors := make(chan error, len(sources))
	var workers sync.WaitGroup
	for _, source := range sources {
		workers.Add(1)
		go func(source oauth2.TokenSource) {
			defer workers.Done()
			token, err := source.Token()
			if err != nil {
				errors <- err
				return
			}
			results <- token
		}(source)
	}
	workers.Wait()
	close(results)
	close(errors)
	for err := range errors {
		t.Errorf("Token() error = %v", err)
	}
	for token := range results {
		if token.AccessToken != "new-access-token" || token.RefreshToken != "new-refresh-token" {
			t.Errorf("Token() = %+v, want the shared refreshed credentials", token)
		}
	}
	if got := refreshes.Load(); got != 1 {
		t.Fatalf("refresh requests = %d, want exactly one", got)
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

func TestRemoteServersFromAllowlist(t *testing.T) {
	urlText := "HTTPS://mcpplaygroundonline.com/mcp-complex-server"
	servers, err := RemoteServersFromAllowlist("# commands\nls\n  " + urlText + "  \nHTTP://localhost:8080/mcp\n")
	if err != nil {
		t.Fatalf("RemoteServersFromAllowlist() error = %v", err)
	}
	if len(servers) != 2 {
		t.Fatalf("server count = %d, want 2", len(servers))
	}
	hash := sha256.Sum256([]byte(urlText))
	wantName := hex.EncodeToString(hash[:4])
	if servers[0].Name != wantName {
		t.Fatalf("server name = %q, want first 8 SHA-256 hex characters %q", servers[0].Name, wantName)
	}
	if len(servers[0].Name) != 8 || servers[0].URL != "https://mcpplaygroundonline.com/mcp-complex-server" {
		t.Fatalf("server parsed as %+v; want normalized scheme and 8-character name", servers[0])
	}
	if servers[1].URL != "http://localhost:8080/mcp" {
		t.Fatalf("localhost server URL = %q, want normalized http URL", servers[1].URL)
	}
}

func TestRemoteServersFromAllowlistRejectsInvalidAndDuplicateURLs(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  string
	}{
		{name: "external http", raw: "HTTP://example.com/mcp"},
		{name: "duplicate URL", raw: "https://example.com/mcp\nhttps://example.com/mcp"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := RemoteServersFromAllowlist(test.raw); err == nil {
				t.Fatal("RemoteServersFromAllowlist() succeeded, want an error")
			}
		})
	}
}

func TestResolveRemoteServersUsesFirstExistingFile(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	workspacePath := filepath.Join(home, ".ash", ".ash_allow")
	if err := os.MkdirAll(filepath.Dir(workspacePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workspacePath, []byte("https://workspace.example/mcp\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".ash_allow"), []byte("https://cwd.example/mcp\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	servers, err := ResolveRemoteServers(home, cwd, os.ReadFile)
	if err != nil {
		t.Fatalf("ResolveRemoteServers() error = %v", err)
	}
	if len(servers) != 1 || servers[0].URL != "https://workspace.example/mcp" {
		t.Fatalf("ResolveRemoteServers() = %+v, want workspace registration only", servers)
	}
	if err := os.WriteFile(workspacePath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	servers, err = ResolveRemoteServers(home, cwd, os.ReadFile)
	if err != nil {
		t.Fatalf("ResolveRemoteServers() with empty first file error = %v", err)
	}
	if len(servers) != 0 {
		t.Fatalf("ResolveRemoteServers() with empty first file = %+v, want no registrations", servers)
	}
}

func TestRemoteManagerCachesCompleteLegacyToolCatalog(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "legacy-cache-test", Version: "1"}, &mcp.ServerOptions{
		PageSize:     1,
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
	})
	for _, name := range []string{"echo", "zeta"} {
		mcp.AddTool(server, &mcp.Tool{
			Name:        name,
			Description: "Tool " + name,
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}},
		}, func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{}, nil, nil
		})
	}
	streamableHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	var listRequests atomic.Int32
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading request body: %v", err)
			return
		}
		if len(body) == 0 {
			r.Body = io.NopCloser(bytes.NewReader(body))
			streamableHandler.ServeHTTP(w, r)
			return
		}
		var request struct {
			Method string `json:"method"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Errorf("decoding request body: %v", err)
			return
		}
		if request.Method == "server/discover" {
			http.Error(w, "legacy server", http.StatusNotFound)
			return
		}
		if request.Method == "tools/list" {
			listRequests.Add(1)
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		streamableHandler.ServeHTTP(w, r)
	}))
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	manager := NewRemoteManager(ctx, &systemCredentialStorage{directory: t.TempDir(), key: make([]byte, 32), native: unavailableKeyring{}})
	defer manager.Close()
	status, err := manager.Start(ctx, RemoteServer{Name: "legacy", URL: httpServer.URL})
	if err != nil || status.State != "connected" {
		t.Fatalf("Start() = %+v, %v; want connected", status, err)
	}
	session := manager.sessions["legacy"]
	if got := session.client.InitializeResult().ProtocolVersion; got >= "2026-07-28" {
		t.Fatalf("protocol = %q, want negotiated legacy protocol", got)
	}
	tools, err := manager.ListToolsContext(ctx, "legacy")
	if err != nil {
		t.Fatalf("ListToolsContext() error = %v", err)
	}
	if len(tools) != 2 || tools[0].Name != "echo" || tools[1].Name != "zeta" {
		t.Fatalf("ListToolsContext() = %+v, want both paginated tools in name order", tools)
	}
	wantRequests := int32(2)
	if got := listRequests.Load(); got != wantRequests {
		t.Fatalf("tools/list requests = %d, want %d (both pages)", got, wantRequests)
	}
	properties := tools[0].InputSchema["properties"].(map[string]any)
	properties["value"].(map[string]any)["type"] = "mutated"
	tools, err = manager.ListToolsContext(ctx, "legacy")
	if err != nil {
		t.Fatalf("cached ListToolsContext() error = %v", err)
	}
	if got := tools[0].InputSchema["properties"].(map[string]any)["value"].(map[string]any)["type"]; got != "string" {
		t.Fatalf("cached schema was mutated through returned catalog: type = %v", got)
	}
	if got := listRequests.Load(); got != wantRequests {
		t.Fatalf("cached tools/list requests = %d, want %d", got, wantRequests)
	}
	session.mu.Lock()
	session.toolsExpiry = time.Now().Add(-time.Second)
	session.mu.Unlock()
	if _, err := manager.ListToolsContext(ctx, "legacy"); err != nil {
		t.Fatalf("expired ListToolsContext() error = %v", err)
	}
	if got := listRequests.Load(); got != wantRequests*2 {
		t.Fatalf("refreshed tools/list requests = %d, want %d", got, wantRequests*2)
	}
}

func TestRemoteManagerRefreshesCatalogAfterLegacyNotification(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "legacy-notification-test", Version: "1"}, &mcp.ServerOptions{
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{ListChanged: true}},
	})
	addTestTool := func(name string) {
		mcp.AddTool(server, &mcp.Tool{
			Name: name, InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		}, func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{}, nil, nil
		})
	}
	addTestTool("before")
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	var listRequests atomic.Int32
	refreshed := make(chan struct{}, 1)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			handler.ServeHTTP(w, r)
			return
		}
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading request body: %v", err)
			return
		}
		if len(payload) > 0 {
			var request struct {
				Method string `json:"method"`
			}
			if err := json.Unmarshal(payload, &request); err != nil {
				t.Errorf("decoding request body: %v", err)
				return
			}
			if request.Method == "tools/list" && listRequests.Add(1) > 1 {
				select {
				case refreshed <- struct{}{}:
				default:
				}
			}
		}
		r.Body = io.NopCloser(bytes.NewReader(payload))
		handler.ServeHTTP(w, r)
	}))
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	manager := NewRemoteManager(ctx, &systemCredentialStorage{directory: t.TempDir(), key: make([]byte, 32), native: unavailableKeyring{}})
	defer manager.Close()
	status, err := manager.Start(ctx, RemoteServer{Name: "legacy-notify", URL: httpServer.URL})
	if err != nil || status.State != "connected" {
		t.Fatalf("Start() = %+v, %v; want connected", status, err)
	}
	if tools, err := manager.ListToolsContext(ctx, "legacy-notify"); err != nil || len(tools) != 1 {
		t.Fatalf("initial ListToolsContext() = %d tools, %v; want one tool", len(tools), err)
	}

	addTestTool("after")
	select {
	case <-refreshed:
	case <-ctx.Done():
		t.Fatal("tool-list notification did not trigger a catalog refresh")
	}
	tools, err := manager.ListToolsContext(ctx, "legacy-notify")
	if err != nil {
		t.Fatalf("refreshed ListToolsContext() error = %v", err)
	}
	if len(tools) != 2 || tools[0].Name != "after" || tools[1].Name != "before" {
		t.Fatalf("refreshed tools = %+v, want sorted after/before catalog", tools)
	}
}

func TestRemoteManagerRechecksCatalogAfterNotificationDuringRefresh(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "notification-race-test", Version: "1"}, &mcp.ServerOptions{
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{ListChanged: true}},
	})
	addCatalogTestTools(server, "initial")
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	var listRequests atomic.Int32
	refreshEntered := make(chan struct{}, 1)
	releaseRefresh := make(chan struct{})
	thirdListRequest := make(chan struct{}, 1)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			handler.ServeHTTP(w, r)
			return
		}
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading request body: %v", err)
			return
		}
		var request struct {
			Method string `json:"method"`
		}
		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &request); err != nil {
				t.Errorf("decoding request: %v", err)
				return
			}
		}
		if request.Method == "server/discover" {
			http.Error(w, "legacy server", http.StatusNotFound)
			return
		}
		if request.Method == "tools/list" {
			switch count := listRequests.Add(1); count {
			case 2:
				refreshEntered <- struct{}{}
				select {
				case <-releaseRefresh:
				case <-r.Context().Done():
					return
				}
			case 3:
				thirdListRequest <- struct{}{}
			}
		}
		r.Body = io.NopCloser(bytes.NewReader(payload))
		handler.ServeHTTP(w, r)
	}))
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	manager := NewRemoteManager(ctx, &systemCredentialStorage{directory: t.TempDir(), key: make([]byte, 32), native: unavailableKeyring{}})
	defer manager.Close()
	status, err := manager.Start(ctx, RemoteServer{Name: "notification-race", URL: httpServer.URL})
	if err != nil || status.State != "connected" {
		t.Fatalf("Start() = %+v, %v; want connected", status, err)
	}
	if tools, err := manager.ListToolsContext(ctx, "notification-race"); err != nil || len(tools) != 1 {
		t.Fatalf("initial ListToolsContext() = %d tools, %v; want one tool", len(tools), err)
	}

	addCatalogTestTools(server, "trigger")
	select {
	case <-refreshEntered:
	case <-ctx.Done():
		t.Fatal("first notification did not start a refresh")
	}
	addCatalogTestTools(server, "during")
	close(releaseRefresh)
	select {
	case <-thirdListRequest:
	case <-ctx.Done():
		t.Fatal("notification during refresh did not trigger another discovery")
	}

	tools, err := manager.ListToolsContext(ctx, "notification-race")
	if err != nil {
		t.Fatalf("ListToolsContext() after refresh: %v", err)
	}
	if len(tools) != 3 || tools[0].Name != "during" || tools[1].Name != "initial" || tools[2].Name != "trigger" {
		t.Fatalf("refreshed catalog = %+v, want during/initial/trigger", tools)
	}
}

func TestRemoteManagerCoalescesConcurrentCatalogReaders(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "coalescing-test", Version: "1"}, &mcp.ServerOptions{
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "echo", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{}, nil, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	var listRequests atomic.Int32
	listEntered := make(chan struct{}, 1)
	releaseList := make(chan struct{})
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		payload, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading request body: %v", err)
			return
		}
		var request struct {
			Method string `json:"method"`
		}
		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &request); err != nil {
				t.Errorf("decoding request body: %v", err)
				return
			}
		}
		if request.Method == "tools/list" {
			if listRequests.Add(1) == 1 {
				listEntered <- struct{}{}
			}
			select {
			case <-releaseList:
			case <-r.Context().Done():
				return
			}
		}
		r.Body = io.NopCloser(bytes.NewReader(payload))
		handler.ServeHTTP(w, r)
	}))
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	manager := NewRemoteManager(ctx, &systemCredentialStorage{directory: t.TempDir(), key: make([]byte, 32), native: unavailableKeyring{}})
	defer manager.Close()
	status, err := manager.Start(ctx, RemoteServer{Name: "coalescing", URL: httpServer.URL})
	if err != nil || status.State != "connected" {
		t.Fatalf("Start() = %+v, %v; want connected", status, err)
	}
	const readers = 8
	results := make(chan error, readers)
	for range readers {
		go func() {
			tools, err := manager.ListToolsContext(ctx, "coalescing")
			if err == nil && len(tools) != 1 {
				err = fmt.Errorf("tool count = %d, want 1", len(tools))
			}
			results <- err
		}()
	}
	select {
	case <-listEntered:
	case <-ctx.Done():
		t.Fatal("no tools/list request reached the server")
	}
	close(releaseList)
	for range readers {
		if err := <-results; err != nil {
			t.Fatalf("concurrent ListToolsContext(): %v", err)
		}
	}
	if got := listRequests.Load(); got != 1 {
		t.Fatalf("tools/list request count = %d, want one coalesced request", got)
	}
}

func TestRemoteManagerRejectsInvalidToolCatalogPages(t *testing.T) {
	var repeatedCursor string
	for _, test := range []struct {
		name      string
		configure func(*mcp.Server)
		intercept func(http.ResponseWriter, *http.Request, http.Handler, int) bool
		wantError string
	}{
		{
			name: "repeated cursor",
			configure: func(server *mcp.Server) {
				addCatalogTestTools(server, "alpha", "beta")
			},
			intercept: func(w http.ResponseWriter, r *http.Request, handler http.Handler, listPage int) bool {
				if listPage == 0 {
					return false
				}
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, r)
				var response map[string]any
				data := strings.TrimPrefix(recorder.Body.String(), "event: message\ndata: ")
				data = strings.TrimSuffix(data, "\n\n")
				if err := json.Unmarshal([]byte(data), &response); err != nil {
					t.Errorf("decoding tools/list response: %v", err)
					return true
				}
				result, ok := response["result"].(map[string]any)
				if !ok {
					t.Errorf("tools/list response has no result object: %s", recorder.Body.String())
					return true
				}
				if listPage == 1 {
					cursor, ok := result["nextCursor"].(string)
					if !ok || cursor == "" {
						t.Errorf("first tools/list page has no next cursor: %s", recorder.Body.String())
						return true
					}
					repeatedCursor = cursor
				} else {
					result["nextCursor"] = repeatedCursor
				}
				payload, err := json.Marshal(response)
				if err != nil {
					t.Errorf("encoding tools/list response: %v", err)
					return true
				}
				copyMCPResponse(w, recorder, append(append([]byte("event: message\ndata: "), payload...), []byte("\n\n")...))
				return true
			},
			wantError: "repeated a tools/list pagination cursor",
		},
		{
			name: "page failure",
			configure: func(server *mcp.Server) {
				addCatalogTestTools(server, "alpha", "beta")
			},
			intercept: func(w http.ResponseWriter, _ *http.Request, _ http.Handler, listPage int) bool {
				if listPage > 1 && listPage%2 == 0 {
					http.Error(w, "page unavailable", http.StatusInternalServerError)
					return true
				}
				return false
			},
			wantError: "listing tools",
		},
		{
			name: "catalog exceeds broker budget",
			configure: func(server *mcp.Server) {
				mcp.AddTool(server, &mcp.Tool{
					Name: "oversized", Description: strings.Repeat("x", brokerproto.MaxBody),
					InputSchema: map[string]any{"type": "object"},
				}, func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
					return &mcp.CallToolResult{}, nil, nil
				})
			},
			wantError: "catalog exceeds broker response limit",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			repeatedCursor = ""
			server := mcp.NewServer(&mcp.Implementation{Name: "invalid-catalog-test", Version: "1"}, &mcp.ServerOptions{
				PageSize:     1,
				Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
			})
			test.configure(server)
			handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
			var listPages atomic.Int32
			httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				payload, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("reading request body: %v", err)
					return
				}
				var request struct {
					Method string `json:"method"`
				}
				if len(payload) > 0 {
					if err := json.Unmarshal(payload, &request); err != nil {
						t.Errorf("decoding request body: %v", err)
						return
					}
				}
				if request.Method == "server/discover" {
					http.Error(w, "legacy server", http.StatusNotFound)
					return
				}
				listPage := 0
				if request.Method == "tools/list" {
					listPage = int(listPages.Add(1))
				}
				r.Body = io.NopCloser(bytes.NewReader(payload))
				if test.intercept != nil && test.intercept(w, r, handler, listPage) {
					return
				}
				handler.ServeHTTP(w, r)
			}))
			defer httpServer.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			manager := NewRemoteManager(ctx, &systemCredentialStorage{directory: t.TempDir(), key: make([]byte, 32), native: unavailableKeyring{}})
			defer manager.Close()
			status, err := manager.Start(ctx, RemoteServer{Name: "invalid-catalog", URL: httpServer.URL})
			if err != nil || status.State != "connected" {
				t.Fatalf("Start() = %+v, %v; want connected", status, err)
			}
			_, err = manager.ListToolsContext(ctx, "invalid-catalog")
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("ListToolsContext() error = %v, want error containing %q", err, test.wantError)
			}
		})
	}
}

func TestRemoteManagerHandlesEmptyAndUnsupportedToolCatalogs(t *testing.T) {
	for _, test := range []struct {
		name         string
		capabilities *mcp.ServerCapabilities
		wantRequests int32
	}{
		{
			name: "empty catalog",
			capabilities: &mcp.ServerCapabilities{
				Tools: &mcp.ToolCapabilities{},
			},
			wantRequests: 1,
		},
		{
			name:         "tools unsupported",
			capabilities: &mcp.ServerCapabilities{},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := mcp.NewServer(&mcp.Implementation{Name: "empty-catalog-test", Version: "1"}, &mcp.ServerOptions{
				Capabilities: test.capabilities,
			})
			handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true})
			var listRequests atomic.Int32
			httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				payload, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("reading request body: %v", err)
					return
				}
				var request struct {
					Method string `json:"method"`
				}
				if len(payload) > 0 {
					if err := json.Unmarshal(payload, &request); err != nil {
						t.Errorf("decoding request: %v", err)
						return
					}
				}
				if request.Method == "server/discover" {
					http.Error(w, "legacy server", http.StatusNotFound)
					return
				}
				if request.Method == "tools/list" {
					listRequests.Add(1)
				}
				r.Body = io.NopCloser(bytes.NewReader(payload))
				handler.ServeHTTP(w, r)
			}))
			defer httpServer.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			manager := NewRemoteManager(ctx, &systemCredentialStorage{directory: t.TempDir(), key: make([]byte, 32), native: unavailableKeyring{}})
			defer manager.Close()
			status, err := manager.Start(ctx, RemoteServer{Name: "empty-catalog", URL: httpServer.URL})
			if err != nil || status.State != "connected" {
				t.Fatalf("Start() = %+v, %v; want connected", status, err)
			}
			tools, err := manager.ListToolsContext(ctx, "empty-catalog")
			if err != nil {
				t.Fatalf("ListToolsContext() error = %v", err)
			}
			if len(tools) != 0 {
				t.Fatalf("ListToolsContext() returned %d tools, want an empty catalog", len(tools))
			}
			if got := listRequests.Load(); got != test.wantRequests {
				t.Fatalf("tools/list requests = %d, want %d", got, test.wantRequests)
			}
		})
	}
}

func addCatalogTestTools(server *mcp.Server, names ...string) {
	for _, name := range names {
		mcp.AddTool(server, &mcp.Tool{
			Name: name, InputSchema: map[string]any{"type": "object"},
		}, func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{}, nil, nil
		})
	}
}

func copyMCPResponse(w http.ResponseWriter, recorder *httptest.ResponseRecorder, payload []byte) {
	for name, values := range recorder.Header() {
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}
	w.Header().Del("Content-Length")
	w.WriteHeader(recorder.Code)
	_, _ = w.Write(payload)
}

func TestRemoteManagerDoesNotDowngradeAfterDiscoveryServerFailure(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "protocol-failure-test", Version: "1"}, &mcp.ServerOptions{
		Capabilities:              &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
		SupportedProtocolVersions: mcp.SupportedProtocolVersions(),
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true})
	var discoveryRequests, initializeRequests atomic.Int32
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading request body: %v", err)
			return
		}
		var request struct {
			Method string `json:"method"`
		}
		if err := json.Unmarshal(payload, &request); err != nil {
			t.Errorf("decoding request: %v", err)
			return
		}
		switch request.Method {
		case "server/discover":
			discoveryRequests.Add(1)
			http.Error(w, "temporary server failure", http.StatusInternalServerError)
			return
		case "initialize":
			initializeRequests.Add(1)
		}
		r.Body = io.NopCloser(bytes.NewReader(payload))
		handler.ServeHTTP(w, r)
	}))
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	manager := NewRemoteManager(ctx, &systemCredentialStorage{directory: t.TempDir(), key: make([]byte, 32), native: unavailableKeyring{}})
	defer manager.Close()
	status, err := manager.Start(ctx, RemoteServer{Name: "protocol-failure", URL: httpServer.URL})
	if err != nil {
		t.Fatalf("Start() error = %v, want status-based failure", err)
	}
	if status.State != "error" || !strings.Contains(status.Error, "newest MCP protocol discovery failed") {
		t.Fatalf("Start() status = %+v, want discovery failure without downgrade", status)
	}
	if got := discoveryRequests.Load(); got != 1 {
		t.Fatalf("server/discover requests = %d, want 1", got)
	}
	if got := initializeRequests.Load(); got != 0 {
		t.Fatalf("legacy initialize requests = %d, want 0 after server failure", got)
	}
}

func TestProtocolNegotiationProbePreservesLongLivedSSE(t *testing.T) {
	firstEvent := "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"supportedVersions\":[\"2026-07-28\"]}}\n\n"
	laterEvent := "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/tools/list_changed\"}\n\n"
	reader, writer := io.Pipe()
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       reader,
	}
	releaseWriter := make(chan struct{})
	writerDone := make(chan struct{})
	go func() {
		_, _ = io.WriteString(writer, firstEvent)
		close(writerDone)
		<-releaseWriter
		_, _ = io.WriteString(writer, laterEvent)
		_ = writer.Close()
	}()

	_, valid, modern := responseDiscoveryProtocol(response)
	if !valid || !modern {
		t.Fatalf("responseDiscoveryProtocol() = valid %t, modern %t; want a modern discovery result", valid, modern)
	}
	<-writerDone
	close(releaseWriter)
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading preserved SSE stream: %v", err)
	}
	if string(payload) != firstEvent+laterEvent {
		t.Fatalf("preserved SSE stream = %q, want both initial response and later event", payload)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatalf("closing SSE body: %v", err)
	}
}

func TestRemoteManagerHonorsModernToolCatalogTTL(t *testing.T) {
	for _, test := range []struct {
		name           string
		ttlMillis      int
		wantSecondCall bool
	}{
		{name: "positive TTL", ttlMillis: 60_000},
		{name: "zero TTL", ttlMillis: 0, wantSecondCall: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var cacheableResponses atomic.Int32
			server := mcp.NewServer(&mcp.Implementation{Name: "modern-cache-test", Version: "1"}, &mcp.ServerOptions{
				Capabilities:              &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
				SupportedProtocolVersions: mcp.SupportedProtocolVersions(),
				SetCacheable: func(_ context.Context, _ mcp.Request, cacheable *mcp.Cacheable) {
					cacheable.TTLMs = test.ttlMillis
					cacheableResponses.Add(1)
				},
			})
			mcp.AddTool(server, &mcp.Tool{
				Name: "echo", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			}, func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
				return &mcp.CallToolResult{}, nil, nil
			})
			handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true})
			httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				handler.ServeHTTP(w, r)
			}))
			defer httpServer.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			manager := NewRemoteManager(ctx, &systemCredentialStorage{directory: t.TempDir(), key: make([]byte, 32), native: unavailableKeyring{}})
			defer manager.Close()
			status, err := manager.Start(ctx, RemoteServer{Name: "modern", URL: httpServer.URL})
			if err != nil || status.State != "connected" {
				t.Fatalf("Start() = %+v, %v; want connected", status, err)
			}
			session := manager.sessions["modern"]
			if got := session.client.InitializeResult().ProtocolVersion; got < "2026-07-28" {
				t.Fatalf("protocol = %q, want modern protocol", got)
			}
			if _, err := manager.ListToolsContext(ctx, "modern"); err != nil {
				t.Fatalf("first ListToolsContext() error = %v", err)
			}
			responsesAfterFirstList := cacheableResponses.Load()
			if _, err := manager.ListToolsContext(ctx, "modern"); err != nil {
				t.Fatalf("second ListToolsContext() error = %v", err)
			}
			responsesAfterSecondList := cacheableResponses.Load()
			if test.wantSecondCall && responsesAfterSecondList <= responsesAfterFirstList {
				t.Fatalf("cacheable responses after second list = %d, want an additional response after zero TTL", responsesAfterSecondList)
			}
			if !test.wantSecondCall && responsesAfterSecondList != responsesAfterFirstList {
				t.Fatalf("cacheable responses after second list = %d, want cached response count %d", responsesAfterSecondList, responsesAfterFirstList)
			}
		})
	}
}

func BenchmarkRemoteManagerCatalog(b *testing.B) {
	for _, cold := range []bool{true, false} {
		name := "Warm"
		if cold {
			name = "ColdDiscovery"
		}
		b.Run(name, func(b *testing.B) {
			server := mcp.NewServer(&mcp.Implementation{Name: "catalog-benchmark", Version: "1"}, &mcp.ServerOptions{
				PageSize:     1,
				Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
			})
			mcp.AddTool(server, &mcp.Tool{
				Name: "echo", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			}, func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
				return &mcp.CallToolResult{}, nil, nil
			})
			var listRequests atomic.Int64
			handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
			httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						b.Errorf("reading request: %v", err)
						return
					}
					var request struct {
						Method string `json:"method"`
					}
					if json.Unmarshal(body, &request) == nil && request.Method == "tools/list" {
						listRequests.Add(1)
					}
					r.Body = io.NopCloser(bytes.NewReader(body))
				}
				handler.ServeHTTP(w, r)
			}))
			defer httpServer.Close()

			ctx, cancel := context.WithCancel(context.Background())
			manager := NewRemoteManager(ctx, &systemCredentialStorage{directory: b.TempDir(), key: make([]byte, 32), native: unavailableKeyring{}})
			defer cancel()
			defer manager.Close()
			status, err := manager.Start(ctx, RemoteServer{Name: "benchmark", URL: httpServer.URL})
			if err != nil || status.State != "connected" {
				b.Fatalf("Start() = %+v, %v; want connected", status, err)
			}
			if _, err := manager.ListToolsContext(ctx, "benchmark"); err != nil {
				b.Fatalf("initial ListToolsContext(): %v", err)
			}
			session := manager.sessions["benchmark"]
			b.ResetTimer()
			for range b.N {
				if cold {
					session.mu.Lock()
					session.toolsExpiry = time.Time{}
					session.mu.Unlock()
				}
				if _, err := manager.ListToolsContext(ctx, "benchmark"); err != nil {
					b.Fatalf("ListToolsContext(): %v", err)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(listRequests.Load())/float64(b.N), "list-requests/op")
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
	var getRequests atomic.Int32
	var listRequests atomic.Int32
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "echo",
		Description: "Returns the input string",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}},
	}, func(_ context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: args["value"].(string)}}}, nil, nil
	})
	streamableHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{Stateless: true})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			getRequests.Add(1)
		}
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading MCP request: %v", err)
			return
		}
		var request struct {
			Method string `json:"method"`
		}
		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &request); err == nil && request.Method == "tools/list" {
				listRequests.Add(1)
			}
		}
		r.Body = io.NopCloser(bytes.NewReader(payload))
		streamableHandler.ServeHTTP(w, r)
	})
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
	listRequestsAfterDiscovery := listRequests.Load()
	result, err := manager.CallTool(ctx, "test", "echo", map[string]any{"value": "hello"})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].(*mcp.TextContent).Text != "hello" {
		t.Fatalf("CallTool() result = %+v, want hello", result)
	}
	if got := getRequests.Load(); got != 0 {
		t.Fatalf("client opened %d standalone SSE streams to a stateless server, want none", got)
	}
	if got := listRequests.Load(); got != listRequestsAfterDiscovery {
		t.Fatalf("tools/list requests after tools/call = %d, want unchanged at %d", got, listRequestsAfterDiscovery)
	}
}

func TestRemoteOAuthTransportAllowsPrivateAddressesOnlyForRegisteredOrigin(t *testing.T) {
	tests := []struct {
		name             string
		host             string
		trustedAuthority string
		resolvedIP       string
		wantDial         bool
	}{
		{
			name:             "registered private origin",
			host:             "mcp.example",
			trustedAuthority: "mcp.example:443",
			resolvedIP:       "192.168.1.2",
			wantDial:         true,
		},
		{
			name:             "unregistered private origin",
			host:             "auth.example",
			trustedAuthority: "mcp.example:443",
			resolvedIP:       "192.168.1.2",
			wantDial:         false,
		},
		{
			name:             "unregistered public origin",
			host:             "auth.example",
			trustedAuthority: "mcp.example:443",
			resolvedIP:       "8.8.8.8",
			wantDial:         true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolvedIP, err := netip.ParseAddr(test.resolvedIP)
			if err != nil {
				t.Fatal(err)
			}
			dialed := ""
			transport := &remoteOAuthTransport{
				base:             &http.Transport{},
				trustedAuthority: test.trustedAuthority,
				lookupNetIP: func(context.Context, string, string) ([]netip.Addr, error) {
					return []netip.Addr{resolvedIP}, nil
				},
				dial: func(_ context.Context, _, address string) (net.Conn, error) {
					dialed = address
					return nil, errors.New("stop after dial check")
				},
			}
			transport.base.DialContext = transport.dialContext
			request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://"+test.host+"/metadata", nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = transport.RoundTrip(request)
			if test.wantDial {
				if dialed != net.JoinHostPort(test.resolvedIP, "443") {
					t.Fatalf("dialed address = %q, want %q; error = %v", dialed, net.JoinHostPort(test.resolvedIP, "443"), err)
				}
				if err == nil || !strings.Contains(err.Error(), "stop after dial check") {
					t.Fatalf("RoundTrip() error = %v, want test dial error", err)
				}
				return
			}
			if dialed != "" {
				t.Fatalf("dialed %q despite an untrusted private address", dialed)
			}
			if err == nil || !strings.Contains(err.Error(), "non-public IP address") {
				t.Fatalf("RoundTrip() error = %v, want non-public address rejection", err)
			}
		})
	}
}

func TestRemoteOAuthTransportFallsBackAcrossResolvedAddresses(t *testing.T) {
	firstIP := netip.MustParseAddr("2001:4860:4860::8888")
	secondIP := netip.MustParseAddr("8.8.8.8")
	firstAddress := net.JoinHostPort(firstIP.String(), "443")
	secondAddress := net.JoinHostPort(secondIP.String(), "443")
	attempts := make(chan string, 2)
	transport := &remoteOAuthTransport{
		base:             &http.Transport{},
		trustedAuthority: "mcp.example:443",
		lookupNetIP: func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{firstIP, secondIP}, nil
		},
		dial: func(ctx context.Context, _, address string) (net.Conn, error) {
			attempts <- address
			if address == firstAddress {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			if address != secondAddress {
				return nil, fmt.Errorf("unexpected dial address %q", address)
			}
			conn, peer := net.Pipe()
			go func() { _ = peer.Close() }()
			return conn, nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	conn, err := transport.dialContext(ctx, "tcp", "mcp.example:443")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("dialContext() error = %v", err)
	}
	_ = conn.Close()
	if elapsed >= 2*time.Second {
		t.Fatalf("dialContext() took %v, want fallback without waiting for the first address to time out", elapsed)
	}
	for _, want := range []string{firstAddress, secondAddress} {
		select {
		case got := <-attempts:
			if got != want {
				t.Fatalf("dial attempt = %q, want %q", got, want)
			}
		default:
			t.Fatalf("dial attempt for %q was not started", want)
		}
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
		}, &mcp.StreamableHTTPOptions{Stateless: true})).ServeHTTP(w, r)
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
