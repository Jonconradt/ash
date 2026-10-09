//go:build darwin || freebsd || linux

package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ash/internal/brokerproto"
)

func TestRemoteToolShimCallsBroker(t *testing.T) {
	t.Setenv("ASH_VERBOSE", "1")
	var debugLog strings.Builder
	previousLogger := slog.Default()
	configureDebugLogging(&debugLog)
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	socket := filepath.Join(t.TempDir(), "mcp.sock")
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", socket)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer func() { _ = listener.Close() }()
	t.Setenv(brokerSocketEnv, socket)
	t.Setenv(brokerTokenEnv, "test-token")
	requestDone := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			requestDone <- err
			return
		}
		defer func() { _ = conn.Close() }()
		payload, err := brokerproto.ReadFrame(conn)
		if err != nil {
			requestDone <- err
			return
		}
		var request brokerproto.Request
		if err := json.Unmarshal(payload, &request); err != nil {
			requestDone <- err
			return
		}
		if request.MCPAction != "call" || request.MCPServer != "remote" || request.MCPTool != "echo" || request.MCPArgs["value"] != "hello" {
			requestDone <- os.ErrInvalid
			return
		}
		response, err := json.Marshal(brokerproto.Response{
			Version:   brokerproto.Version,
			MCPResult: json.RawMessage(`{"content":[{"type":"text","text":"hello"}]}`),
		})
		if err == nil {
			err = brokerproto.WriteFrame(conn, response)
		}
		requestDone <- err
	}()

	toolName := "mcp_remote_echo_01234567"
	shim := remoteToolShim{
		local: localToolShim{},
		refs:  map[string]remoteToolRef{toolName: {server: "remote", name: "echo", url: "https://example.com/mcp"}},
		usage: &remoteMCPUsage{},
	}
	result := shim.CallTool(context.Background(), toolName, map[string]any{"value": "hello"})
	if want := "MCP tool call completed without a protocol-level error.\nResult:\nhello"; result != want {
		t.Fatalf("CallTool() = %q, want %q", result, want)
	}
	if err := <-requestDone; err != nil {
		t.Fatalf("fake broker request: %v", err)
	}
	shim.logMCPUsage(context.Background())
	if !strings.Contains(debugLog.String(), `"message":"MCP servers used"`) ||
		!strings.Contains(debugLog.String(), `"url":"https://example.com/mcp"`) ||
		!strings.Contains(debugLog.String(), `"tools":["echo"]`) ||
		!strings.Contains(debugLog.String(), `"calls":1`) {
		t.Fatalf("debug log = %q, want used server and tool summary", debugLog.String())
	}
}

func TestFormatRemoteMCPResultForModel(t *testing.T) {
	t.Run("unwraps successful text content", func(t *testing.T) {
		raw := json.RawMessage(`{"_meta":{"server":"test"},"content":[{"type":"text","text":"{\"results\":[1,2,3],\"resultType\":\"complete\"}"}],"resultType":"complete"}`)
		got := formatRemoteMCPResult(raw)
		if !strings.HasPrefix(got, "MCP tool call completed without a protocol-level error.\nResult:\n") {
			t.Fatalf("formatted result lacks success status: %q", got)
		}
		if !strings.Contains(got, `"results":[1,2,3]`) || strings.Contains(got, `"_meta"`) || strings.Contains(got, `"content"`) {
			t.Fatalf("formatted result retained the MCP wrapper or lost tool data: %q", got)
		}
	})

	t.Run("marks MCP tool errors", func(t *testing.T) {
		raw := json.RawMessage(`{"isError":true,"content":[{"type":"text","text":"invalid data source"}]}`)
		got := formatRemoteMCPResult(raw)
		if !strings.HasPrefix(got, "The MCP server reports a tool error.\nResult:\n") || !strings.Contains(got, "invalid data source") {
			t.Fatalf("formatted error result = %q", got)
		}
	})
}

func TestPrepareRemoteMCPWithNoConfigPreservesLocalShim(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	local := localToolShim{allowlist: map[string]struct{}{"ls": {}}}
	got, err := prepareRemoteMCP(context.Background(), os.Stderr, local)
	if err != nil {
		t.Fatalf("prepareRemoteMCP() error = %v", err)
	}
	if len(got.ListTools()) != len(local.ListTools()) {
		t.Fatalf("tool count = %d, want %d", len(got.ListTools()), len(local.ListTools()))
	}
}

func TestPrepareRemoteMCPDoesNotReportUnusedServersAsUsed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ASH_VERBOSE", "1")
	ashRoot := filepath.Join(home, ashWorkspaceDirName)
	if err := os.MkdirAll(ashRoot, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(ashRoot, allowFileName), []byte("https://example.com/mcp\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	socket := filepath.Join(os.TempDir(), "ash-"+requestIDGenerator()+".sock")
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", socket)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
		_ = os.Remove(socket)
	})
	t.Setenv(brokerSocketEnv, socket)
	t.Setenv(brokerTokenEnv, "test-token")

	requestDone := make(chan error, 1)
	go func() {
		for range 2 {
			conn, err := listener.Accept()
			if err != nil {
				requestDone <- err
				return
			}
			payload, err := brokerproto.ReadFrame(conn)
			if err != nil {
				_ = conn.Close()
				requestDone <- err
				return
			}
			var request brokerproto.Request
			if err := json.Unmarshal(payload, &request); err != nil {
				_ = conn.Close()
				requestDone <- err
				return
			}
			response := brokerproto.Response{Version: brokerproto.Version}
			switch request.MCPAction {
			case "start":
				response.MCPStatus = &brokerproto.MCPStatus{State: "connected"}
			case "list":
				response.MCPTools = []brokerproto.MCPTool{{Name: "search_snippets"}}
			default:
				_ = conn.Close()
				requestDone <- os.ErrInvalid
				return
			}
			encoded, err := json.Marshal(response)
			if err == nil {
				err = brokerproto.WriteFrame(conn, encoded)
			}
			_ = conn.Close()
			if err != nil {
				requestDone <- err
				return
			}
		}
		requestDone <- nil
	}()

	var debugLog strings.Builder
	previousLogger := slog.Default()
	configureDebugLogging(&debugLog)
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	if _, err := prepareRemoteMCP(context.Background(), os.Stderr, localToolShim{}); err != nil {
		t.Fatalf("prepareRemoteMCP() error = %v", err)
	}
	if err := <-requestDone; err != nil {
		t.Fatalf("fake broker request: %v", err)
	}
	if strings.Contains(debugLog.String(), `"message":"MCP servers used"`) {
		t.Fatalf("debug log = %q, discovery alone must not report a server as used", debugLog.String())
	}
}

func TestPrepareToolsMCPInitializationFailureFallsBackToLocalTools(t *testing.T) {
	for _, test := range []struct {
		name        string
		interactive bool
		wantWarning bool
	}{
		{name: "interactive", interactive: true, wantWarning: true},
		{name: "non-interactive"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv(brokerSocketEnv, "")
			t.Setenv(brokerTokenEnv, "")
			root, err := ashWorkspaceDir()
			if err != nil {
				t.Fatalf("ashWorkspaceDir() error = %v", err)
			}
			if err := os.MkdirAll(root, 0o700); err != nil {
				t.Fatalf("MkdirAll() error = %v", err)
			}
			if err := os.WriteFile(filepath.Join(root, allowFileName), []byte("https://example.com/mcp\n"), 0o600); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}

			var stderr strings.Builder
			previousLogger := slog.Default()
			configureDebugLogging(&stderr)
			t.Cleanup(func() { slog.SetDefault(previousLogger) })

			local := localToolShim{allowlist: map[string]struct{}{"ls": {}}}
			got := prepareTools(context.Background(), &stderr, local, test.interactive)
			if len(got.ListTools()) != len(local.ListTools()) {
				t.Fatalf("tool count = %d, want local tool count %d", len(got.ListTools()), len(local.ListTools()))
			}
			if strings.Contains(stderr.String(), "remote MCP tools unavailable") != test.wantWarning {
				t.Fatalf("stderr = %q, want warning=%t", stderr.String(), test.wantWarning)
			}
		})
	}
}

func TestLoadRemoteMCPServersFromAllowlistIgnoresStrictMode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ASH_STRICT", "1")
	t.Setenv("ASH_ALLOW", "ls")
	ashRoot := filepath.Join(home, ashWorkspaceDirName)
	if err := os.MkdirAll(ashRoot, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(ashRoot, allowFileName), []byte("https://example.com/mcp\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	servers, err := loadRemoteMCPServers()
	if err != nil {
		t.Fatalf("loadRemoteMCPServers() error = %v", err)
	}
	if len(servers) != 1 || servers[0].URL != "https://example.com/mcp" || len(servers[0].Name) != 8 {
		t.Fatalf("loadRemoteMCPServers() = %+v, want one registered server despite strict mode", servers)
	}
}

func TestLoadRemoteMCPServersIgnoresLegacyMCPJSON(t *testing.T) {
	home := t.TempDir()
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalCwd) })
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Setenv("HOME", home)
	ashRoot := filepath.Join(home, ashWorkspaceDirName)
	if err := os.MkdirAll(ashRoot, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	legacyConfig := `{"servers":[{"name":"legacy","url":"https://example.com/mcp"}]}`
	if err := os.WriteFile(filepath.Join(ashRoot, "mcp.json"), []byte(legacyConfig), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	servers, err := loadRemoteMCPServers()
	if err != nil {
		t.Fatalf("loadRemoteMCPServers() error = %v", err)
	}
	if len(servers) != 0 {
		t.Fatalf("loadRemoteMCPServers() = %+v, want no servers without .ash_allow registrations", servers)
	}
}

func TestShellWrappersScopeMCPCredentialKeyToBroker(t *testing.T) {
	tests := []struct {
		asset string
		want  []string
	}{
		{asset: "ash_bootstrap/.ash_bashrc", want: []string{"local mcp_credential_key=", "unset ASH_MCP_CREDENTIAL_KEY", `ASH_MCP_CREDENTIAL_KEY="$mcp_credential_key" command ash-broker`, "unset mcp_credential_key", `if [[ "${1:-}" == "mcp" && "${2:-}" == "add" ]]`, `command ash "$@"`}},
		{asset: "ash_bootstrap/.ash_zshrc", want: []string{"local mcp_credential_key=", "unset ASH_MCP_CREDENTIAL_KEY", `ASH_MCP_CREDENTIAL_KEY="$mcp_credential_key" command ash-broker`, "unset mcp_credential_key", `if [[ "${1:-}" == "mcp" && "${2:-}" == "add" ]]`, `command ash "$@"`}},
		{asset: "ash_bootstrap/.ash_fish.fish", want: []string{"set -l mcp_credential_key", "set -e ASH_MCP_CREDENTIAL_KEY", `set -lx ASH_MCP_CREDENTIAL_KEY "$mcp_credential_key"`, `if test (count $argv) -ge 2; and test "$argv[1]" = mcp; and test "$argv[2]" = add`, "command ash $argv"}},
	}
	for _, test := range tests {
		content, err := readEmbeddedBootstrapAsset(test.asset)
		if err != nil {
			t.Fatalf("readEmbeddedBootstrapAsset(%q) error = %v", test.asset, err)
		}
		for _, want := range test.want {
			if !strings.Contains(string(content), want) {
				t.Errorf("%s does not contain scoped credential handoff %q", test.asset, want)
			}
		}
	}
}
