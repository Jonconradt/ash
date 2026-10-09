//go:build darwin || freebsd || linux

package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"ash/internal/brokerproto"
	mcpclient "ash/internal/mcp"
)

func TestParseMCPAddArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantURL string
		noGUI   bool
		wantErr string
	}{
		{name: "url", args: []string{"https://example.com/mcp"}, wantURL: "https://example.com/mcp"},
		{name: "no browser", args: []string{"https://example.com/mcp", "--no-browser"}, wantURL: "https://example.com/mcp", noGUI: true},
		{name: "no url", wantErr: "MCP server URL is required"},
		{name: "two urls", args: []string{"https://one.example/mcp", "https://two.example/mcp"}, wantErr: "exactly one"},
		{name: "unknown flag", args: []string{"--open"}, wantErr: "unknown MCP add option"},
		{name: "duplicate no browser", args: []string{"https://example.com/mcp", "--no-browser", "--no-browser"}, wantErr: "only be specified once"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseMCPAddArgs(test.args)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("parseMCPAddArgs() error = %v, want substring %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseMCPAddArgs() error = %v", err)
			}
			if got.url != test.wantURL || got.noBrowser != test.noGUI {
				t.Fatalf("parseMCPAddArgs() = %+v, want url=%q noBrowser=%t", got, test.wantURL, test.noGUI)
			}
		})
	}
}

func TestResolveMCPAllowlistPathMatchesRuntimePriority(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalCwd) })
	t.Setenv("HOME", home)
	root := filepath.Join(home, ashWorkspaceDirName)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	workspacePath := filepath.Join(root, allowFileName)
	cwdPath := filepath.Join(cwd, allowFileName)
	if err := os.WriteFile(workspacePath, []byte("# workspace\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cwdPath, []byte("# cwd\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := resolveMCPAllowlistPath()
	if err != nil {
		t.Fatalf("resolveMCPAllowlistPath() error = %v", err)
	}
	if got != workspacePath {
		t.Fatalf("resolveMCPAllowlistPath() = %q, want %q", got, workspacePath)
	}

	if err := os.Remove(workspacePath); err != nil {
		t.Fatal(err)
	}
	expectedCWDPath, err := filepath.EvalSymlinks(cwdPath)
	if err != nil {
		t.Fatal(err)
	}
	got, err = resolveMCPAllowlistPath()
	if err != nil {
		t.Fatalf("resolveMCPAllowlistPath() after removing workspace file: %v", err)
	}
	if got != expectedCWDPath {
		t.Fatalf("resolveMCPAllowlistPath() = %q, want cwd file %q", got, expectedCWDPath)
	}
}

func TestAddMCPRegistrationPreservesFileAndSecuresPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), ashWorkspaceDirName, allowFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	original := "# keep this policy\nls"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	servers, err := mcpclient.RemoteServersFromAllowlist("https://example.com/mcp")
	if err != nil {
		t.Fatal(err)
	}
	server := servers[0]
	if err := addMCPRegistration(path, server, server.URL); err != nil {
		t.Fatalf("addMCPRegistration() error = %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(content), original+"\n"+server.URL+"\n"; got != want {
		t.Fatalf(".ash_allow = %q, want %q", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf(".ash_allow mode = %04o, want 0600", got)
	}
	if err := addMCPRegistration(path, server, server.URL); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("duplicate add error = %v, want already registered", err)
	}
}

func TestAddMCPRegistrationPreservesURLSpellingUsedForStableName(t *testing.T) {
	const rawURL = "HTTPS://example.com/mcp"
	servers, err := mcpclient.RemoteServersFromAllowlist(rawURL)
	if err != nil || len(servers) != 1 {
		t.Fatalf("RemoteServersFromAllowlist() = %v, %v", servers, err)
	}
	path := filepath.Join(t.TempDir(), allowFileName)
	if err := addMCPRegistration(path, servers[0], rawURL); err != nil {
		t.Fatalf("addMCPRegistration() error = %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	registered, err := mcpclient.RemoteServersFromAllowlist(string(content))
	if err != nil {
		t.Fatalf("RemoteServersFromAllowlist(registered file) error = %v", err)
	}
	if len(registered) != 1 || registered[0].Name != servers[0].Name || registered[0].URL != servers[0].URL {
		t.Fatalf("registered server = %+v, want %+v", registered, servers[0])
	}
}

func TestCheckMCPRegistrationRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	path := filepath.Join(dir, allowFileName)
	if err := os.WriteFile(target, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	err := checkMCPRegistration(path, mcpclient.RemoteServer{Name: "test", URL: "https://example.com/mcp"})
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("checkMCPRegistration() error = %v, want regular-file rejection", err)
	}
}

func TestPresentMCPAuthorizationHonorsBrowserChoiceAndFallsBack(t *testing.T) {
	originalOpen := openBrowserCommand
	t.Cleanup(func() { openBrowserCommand = originalOpen })
	called := false
	openBrowserCommand = func(_ context.Context, name string, args ...string) *exec.Cmd {
		called = true
		if len(args) == 0 || args[len(args)-1] != "https://auth.example/authorize" {
			t.Errorf("browser command %q args = %q, missing authorization URL", name, args)
		}
		return exec.CommandContext(context.Background(), "true")
	}
	t.Setenv("DISPLAY", ":test")
	t.Setenv("WAYLAND_DISPLAY", "")
	var output strings.Builder
	if err := presentMCPAuthorization(context.Background(), "https://auth.example/authorize", false, &output); err != nil {
		t.Fatalf("presentMCPAuthorization() error = %v", err)
	}
	if !called || !strings.Contains(output.String(), "opened in your browser") {
		t.Fatalf("browser launch called=%t output=%q", called, output.String())
	}

	called = false
	output.Reset()
	if err := presentMCPAuthorization(context.Background(), "https://auth.example/authorize", true, &output); err != nil {
		t.Fatalf("presentMCPAuthorization(--no-browser) error = %v", err)
	}
	if called || !strings.Contains(output.String(), "https://auth.example/authorize") {
		t.Fatalf("--no-browser called browser=%t output=%q", called, output.String())
	}

	if runtime.GOOS != "darwin" {
		called = false
		t.Setenv("DISPLAY", "")
		t.Setenv("WAYLAND_DISPLAY", "")
		output.Reset()
		if err := presentMCPAuthorization(context.Background(), "https://auth.example/authorize", false, &output); err != nil {
			t.Fatalf("presentMCPAuthorization() without GUI error = %v", err)
		}
		if called || !strings.Contains(output.String(), "https://auth.example/authorize") {
			t.Fatalf("headless fallback called browser=%t output=%q", called, output.String())
		}
		t.Setenv("DISPLAY", ":test")
	}

	openBrowserCommand = func(context.Context, string, ...string) *exec.Cmd {
		return exec.CommandContext(context.Background(), "false")
	}
	output.Reset()
	if err := presentMCPAuthorization(context.Background(), "https://auth.example/authorize", false, &output); err != nil {
		t.Fatalf("presentMCPAuthorization() fallback error = %v", err)
	}
	if !strings.Contains(output.String(), "Could not open the browser") || !strings.Contains(output.String(), "https://auth.example/authorize") {
		t.Fatalf("browser failure did not fall back to URL: %q", output.String())
	}
}

func TestPresentMCPAuthorizationRejectsInvalidURL(t *testing.T) {
	var output strings.Builder
	err := presentMCPAuthorization(context.Background(), "javascript:alert(1)", true, &output)
	if err == nil || !strings.Contains(err.Error(), "invalid OAuth authorization URL") {
		t.Fatalf("presentMCPAuthorization() error = %v, want invalid URL rejection", err)
	}
	if output.Len() != 0 {
		t.Fatalf("invalid authorization URL was displayed: %q", output.String())
	}
}

func TestGUISessionAvailableUsesPlatformSignals(t *testing.T) {
	if runtime.GOOS == "darwin" {
		if !guiSessionAvailable() {
			t.Fatal("guiSessionAvailable() = false on a platform that probes the native URL opener")
		}
		return
	}
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	if guiSessionAvailable() {
		t.Fatal("guiSessionAvailable() = true without a graphical session")
	}
	t.Setenv("WAYLAND_DISPLAY", "wayland-test")
	if !guiSessionAvailable() {
		t.Fatal("guiSessionAvailable() = false with WAYLAND_DISPLAY")
	}
}

func TestRunMCPAddConnectsBeforeRegistering(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalCwd) })
	t.Setenv("HOME", home)
	socket := startMCPSetupBroker(t, 1, func(request brokerproto.Request) brokerproto.Response {
		if request.MCPAction != "start" || request.MCPURL != "https://example.com/mcp" {
			return brokerproto.Response{Error: "unexpected setup request"}
		}
		return brokerproto.Response{MCPStatus: &brokerproto.MCPStatus{State: "connected"}}
	})
	t.Setenv(brokerSocketEnv, socket)
	t.Setenv(brokerTokenEnv, "test-token")
	originalInteractive := stdinIsInteractive
	stdinIsInteractive = func() bool { return true }
	t.Cleanup(func() { stdinIsInteractive = originalInteractive })

	var stdout, stderr strings.Builder
	code := runMCPAdd([]string{"https://example.com/mcp"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runMCPAdd() = %d, stderr=%q", code, stderr.String())
	}
	path := filepath.Join(home, ashWorkspaceDirName, allowFileName)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("registration file was not created: %v", err)
	}
	if !strings.Contains(string(content), "https://example.com/mcp\n") {
		t.Fatalf(".ash_allow = %q, missing server URL", content)
	}
	if !strings.Contains(stdout.String(), "connected and registered") {
		t.Fatalf("stdout = %q, missing success status", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestConnectMCPForSetupFallsBackToTerminalAuthorization(t *testing.T) {
	actions := make(chan string, 3)
	socket := startMCPSetupBroker(t, 3, func(request brokerproto.Request) brokerproto.Response {
		actions <- request.MCPAction
		switch request.MCPAction {
		case "start":
			return brokerproto.Response{MCPStatus: &brokerproto.MCPStatus{
				State:   "authorizing",
				AuthURL: "https://auth.example/authorize",
			}}
		case "auth_ack":
			return brokerproto.Response{}
		case "status_now":
			return brokerproto.Response{MCPStatus: &brokerproto.MCPStatus{State: "connected"}}
		default:
			return brokerproto.Response{Error: "unexpected broker action"}
		}
	})
	t.Setenv(brokerSocketEnv, socket)
	t.Setenv(brokerTokenEnv, "test-token")
	var output strings.Builder
	err := connectMCPForSetup(
		context.Background(),
		mcpclient.RemoteServer{Name: "test", URL: "https://example.com/mcp"},
		true,
		&output,
	)
	if err != nil {
		t.Fatalf("connectMCPForSetup() error = %v", err)
	}
	gotActions := []string{<-actions, <-actions, <-actions}
	if got, want := strings.Join(gotActions, ","), "start,auth_ack,status_now"; got != want {
		t.Fatalf("MCP actions = %q, want %q", got, want)
	}
	if !strings.Contains(output.String(), "https://auth.example/authorize") ||
		!strings.Contains(output.String(), "Waiting for OAuth authorization") {
		t.Fatalf("terminal authorization output = %q", output.String())
	}
}

func startMCPSetupBroker(t *testing.T, requests int, respond func(brokerproto.Request) brokerproto.Response) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ash-mcp-")
	if err != nil {
		t.Fatalf("MkdirTemp() error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "mcp.sock")
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", socket)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for range requests {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer func() { _ = conn.Close() }()
				payload, err := brokerproto.ReadFrame(conn)
				if err != nil {
					return
				}
				var request brokerproto.Request
				if json.Unmarshal(payload, &request) != nil {
					return
				}
				response := respond(request)
				response.Version = brokerproto.Version
				encoded, err := json.Marshal(response)
				if err == nil {
					_ = brokerproto.WriteFrame(conn, encoded)
				}
			}()
		}
	}()
	return socket
}

func TestRunMCPAddDoesNotRegisterWhenBrokerFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(brokerSocketEnv, "")
	t.Setenv(brokerTokenEnv, "")
	originalInteractive := stdinIsInteractive
	stdinIsInteractive = func() bool { return true }
	t.Cleanup(func() { stdinIsInteractive = originalInteractive })

	var stdout, stderr strings.Builder
	code := runMCPAdd([]string{"https://example.com/mcp"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("runMCPAdd() = %d, want 1; stderr=%q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, ashWorkspaceDirName, allowFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(".ash_allow exists after unavailable broker, stat error = %v", err)
	}
}
