//go:build darwin || freebsd || linux

package app

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ash/internal/brokerproto"
)

func TestRemoteToolShimCallsBroker(t *testing.T) {
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
		refs:  map[string]remoteToolRef{toolName: {server: "remote", name: "echo"}},
	}
	result := shim.CallTool(context.Background(), toolName, map[string]any{"value": "hello"})
	if !strings.Contains(result, `"text": "hello"`) {
		t.Fatalf("CallTool() = %s, want remote broker result", result)
	}
	if err := <-requestDone; err != nil {
		t.Fatalf("fake broker request: %v", err)
	}
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

func TestShellWrappersScopeMCPCredentialKeyToBroker(t *testing.T) {
	tests := []struct {
		asset string
		want  []string
	}{
		{asset: "ash_bootstrap/.ash_bashrc", want: []string{"local mcp_credential_key=", "unset ASH_MCP_CREDENTIAL_KEY", `ASH_MCP_CREDENTIAL_KEY="$mcp_credential_key" command ash-broker`, "unset mcp_credential_key"}},
		{asset: "ash_bootstrap/.ash_zshrc", want: []string{"local mcp_credential_key=", "unset ASH_MCP_CREDENTIAL_KEY", `ASH_MCP_CREDENTIAL_KEY="$mcp_credential_key" command ash-broker`, "unset mcp_credential_key"}},
		{asset: "ash_bootstrap/.ash_fish.fish", want: []string{"set -l mcp_credential_key", "set -e ASH_MCP_CREDENTIAL_KEY", `set -lx ASH_MCP_CREDENTIAL_KEY "$mcp_credential_key"`}},
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
