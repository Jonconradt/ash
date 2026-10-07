package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"ash/internal/brokerproto"
	mcpclient "ash/internal/mcp"
)

type remoteToolRef struct {
	server string
	name   string
}

type remoteToolShim struct {
	local mcpToolShim
	tools []toolDefinition
	refs  map[string]remoteToolRef
}

type remoteMCPCallResult struct {
	IsError           bool                `json:"isError"`
	Content           []remoteMCPTextItem `json:"content"`
	StructuredContent json.RawMessage     `json:"structuredContent"`
}

type remoteMCPTextItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

var openBrowserCommand = exec.CommandContext

func prepareRemoteMCP(ctx context.Context, stderrWriter io.Writer, local mcpToolShim) (mcpToolShim, error) {
	servers, err := loadRemoteMCPServers()
	if err != nil {
		return nil, err
	}
	if len(servers) == 0 {
		return local, nil
	}
	if !brokerConfigured() {
		return nil, errors.New("remote MCP servers are configured but ash-broker is unavailable; open a supported interactive shell and retry")
	}
	shim := remoteToolShim{refs: make(map[string]remoteToolRef)}
	usedNames := make(map[string]struct{})
	for _, tool := range local.ListTools() {
		usedNames[tool.Function.Name] = struct{}{}
	}
	for _, server := range servers {
		response, err := brokerMCPDo(ctx, brokerproto.Request{
			MCPAction: "start",
			MCPServer: server.Name,
			MCPURL:    server.URL,
		})
		if err != nil {
			return nil, fmt.Errorf("starting MCP server %q: %w", server.Name, err)
		}
		status, err := remoteStatus(response)
		if err != nil {
			return nil, fmt.Errorf("starting MCP server %q: %w", server.Name, err)
		}
		authURL := status.AuthURL
		if authURL != "" {
			if err := openMCPAuthorization(ctx, authURL, stderrWriter); err != nil {
				return nil, fmt.Errorf("opening OAuth authorization for MCP server %q: %w", server.Name, err)
			}
			_, ackErr := brokerMCPDo(ctx, brokerproto.Request{MCPAction: "auth_ack", MCPServer: server.Name})
			if ackErr != nil {
				return nil, fmt.Errorf("acknowledging OAuth authorization for MCP server %q: %w", server.Name, ackErr)
			}
		}
		for status.State != "connected" && status.Error == "" {
			if authURL != "" && status.AuthURL != "" && status.AuthURL != authURL {
				authURL = status.AuthURL
				if err := openMCPAuthorization(ctx, authURL, stderrWriter); err != nil {
					return nil, fmt.Errorf("opening OAuth authorization for MCP server %q: %w", server.Name, err)
				}
				_, ackErr := brokerMCPDo(ctx, brokerproto.Request{MCPAction: "auth_ack", MCPServer: server.Name})
				if ackErr != nil {
					return nil, fmt.Errorf("acknowledging OAuth authorization for MCP server %q: %w", server.Name, ackErr)
				}
			}
			if status.State == "authorizing" && status.AuthURL == "" {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(200 * time.Millisecond):
				}
			}
			response, err = brokerMCPDo(ctx, brokerproto.Request{MCPAction: "status", MCPServer: server.Name})
			if err != nil {
				return nil, fmt.Errorf("waiting for MCP server %q: %w", server.Name, err)
			}
			status, err = remoteStatus(response)
			if err != nil {
				return nil, fmt.Errorf("waiting for MCP server %q: %w", server.Name, err)
			}
		}
		if status.Error != "" {
			return nil, fmt.Errorf("MCP server %q: %s", server.Name, status.Error)
		}
		response, err = brokerMCPDo(ctx, brokerproto.Request{MCPAction: "list", MCPServer: server.Name})
		if err != nil {
			return nil, fmt.Errorf("listing tools from MCP server %q: %w", server.Name, err)
		}
		for _, remote := range response.MCPTools {
			name := mcpclient.ToolFunctionName(server.Name, remote.Name)
			if _, exists := usedNames[name]; exists {
				return nil, fmt.Errorf("remote MCP tool name collision for %q", name)
			}
			usedNames[name] = struct{}{}
			shim.refs[name] = remoteToolRef{server: server.Name, name: remote.Name}
			schema := remote.InputSchema
			if schema == nil {
				schema = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			shim.tools = append(shim.tools, toolDefinition{
				Type: "function",
				Function: toolFunctionDefinition{
					Name:        name,
					Description: remote.Description,
					Parameters:  schema,
				},
			})
		}
	}
	shim.local = local
	return shim, nil
}

func loadRemoteMCPServers() ([]mcpclient.RemoteServer, error) {
	root, err := ashWorkspaceDir()
	if err != nil {
		return nil, err
	}
	cwd, err := osGetwd()
	if err != nil {
		return nil, err
	}
	home, err := osUserHomeDir()
	if err != nil {
		return nil, err
	}
	for _, path := range []string{
		filepath.Join(root, allowFileName),
		filepath.Join(cwd, allowFileName),
		filepath.Join(home, allowFileName),
	} {
		content, err := osReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading MCP registrations from %s: %w", path, err)
		}
		return mcpclient.RemoteServersFromAllowlist(string(content))
	}
	return nil, nil
}

func (s remoteToolShim) ListTools() []toolDefinition {
	tools := s.local.ListTools()
	return append(tools, s.tools...)
}

func (s remoteToolShim) CallTool(ctx context.Context, name string, args map[string]any) string {
	ref, ok := s.refs[name]
	if !ok {
		return s.local.CallTool(ctx, name, args)
	}
	response, err := brokerMCPDo(ctx, brokerproto.Request{
		MCPAction: "call",
		MCPServer: ref.server,
		MCPTool:   ref.name,
		MCPArgs:   args,
	})
	if err != nil {
		return fmt.Sprintf("MCP tool call failed: %v", err)
	}
	if len(response.MCPResult) == 0 {
		return "MCP tool call returned no result"
	}
	return formatRemoteMCPResult(response.MCPResult)
}

func formatRemoteMCPResult(raw json.RawMessage) string {
	var result remoteMCPCallResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return indentRemoteMCPJSON(raw)
	}

	output := ""
	if len(result.Content) > 0 {
		text := make([]string, 0, len(result.Content))
		for _, item := range result.Content {
			if item.Type != "text" {
				text = nil
				break
			}
			text = append(text, item.Text)
		}
		if text != nil {
			output = strings.Join(text, "\n")
		}
	}
	if output == "" && len(result.StructuredContent) > 0 {
		output = indentRemoteMCPJSON(result.StructuredContent)
	}
	if output == "" {
		output = indentRemoteMCPJSON(raw)
	}

	status := "MCP tool call completed without a protocol-level error."
	if result.IsError {
		status = "The MCP server reports a tool error."
	}
	return status + "\nResult:\n" + output
}

func indentRemoteMCPJSON(raw json.RawMessage) string {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		return string(raw)
	}
	return pretty.String()
}

func remoteStatus(response brokerproto.Response) (brokerproto.MCPStatus, error) {
	if response.Error != "" {
		return brokerproto.MCPStatus{}, errors.New(response.Error)
	}
	if response.MCPStatus == nil {
		return brokerproto.MCPStatus{}, errors.New("broker returned no MCP session status")
	}
	return *response.MCPStatus, nil
}

func openMCPAuthorization(ctx context.Context, authorizationURL string, stderrWriter io.Writer) error {
	parsed, err := url.Parse(authorizationURL)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("broker returned an invalid OAuth authorization URL")
	}
	var command string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		command, args = "open", []string{authorizationURL}
	case "windows":
		command, args = "rundll32", []string{"url.dll,FileProtocolHandler", authorizationURL}
	default:
		command, args = "xdg-open", []string{authorizationURL}
	}
	if err := openBrowserCommand(ctx, command, args...).Run(); err != nil {
		if _, writeErr := fmt.Fprintf(stderrWriter, "Open this URL to authorize the MCP server:\n%s\n", authorizationURL); writeErr != nil {
			return errors.Join(err, writeErr)
		}
	}
	return nil
}
