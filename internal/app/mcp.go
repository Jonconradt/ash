package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"ash/internal/brokerproto"
	mcpclient "ash/internal/mcp"
)

type remoteToolRef struct {
	server string
	name   string
	url    string
}

type remoteToolShim struct {
	local mcpToolShim
	tools []toolDefinition
	refs  map[string]remoteToolRef
	usage *remoteMCPUsage
}

type remoteMCPCallResult struct {
	IsError           bool                `json:"isError"`
	Content           []remoteMCPTextItem `json:"content"`
	StructuredContent json.RawMessage     `json:"structuredContent"`
}

type remoteMCPUsage struct {
	mu    sync.Mutex
	calls []remoteMCPUsageCall
}

type remoteMCPUsageCall struct {
	url  string
	tool string
}

type remoteMCPServerUsage struct {
	URL   string   `json:"url"`
	Tools []string `json:"tools"`
	Calls int      `json:"calls"`
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
		return remoteToolShim{local: local, refs: make(map[string]remoteToolRef), usage: &remoteMCPUsage{}}, nil
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
			shim.refs[name] = remoteToolRef{server: server.Name, name: remote.Name, url: server.URL}
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
	shim.usage = &remoteMCPUsage{}
	return shim, nil
}

func (u *remoteMCPUsage) record(url, tool string) {
	u.mu.Lock()
	u.calls = append(u.calls, remoteMCPUsageCall{url: url, tool: tool})
	u.mu.Unlock()
}

func (u *remoteMCPUsage) summary() []remoteMCPServerUsage {
	u.mu.Lock()
	defer u.mu.Unlock()

	byURL := make(map[string]*remoteMCPServerUsage)
	toolSets := make(map[string]map[string]struct{})
	for _, call := range u.calls {
		server, ok := byURL[call.url]
		if !ok {
			server = &remoteMCPServerUsage{URL: call.url, Tools: []string{}}
			byURL[call.url] = server
			toolSets[call.url] = make(map[string]struct{})
		}
		server.Calls++
		toolSets[call.url][call.tool] = struct{}{}
	}

	servers := make([]remoteMCPServerUsage, 0, len(byURL))
	for url, server := range byURL {
		for tool := range toolSets[url] {
			server.Tools = append(server.Tools, tool)
		}
		sort.Strings(server.Tools)
		servers = append(servers, *server)
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].URL < servers[j].URL })
	return servers
}

func (s remoteToolShim) logMCPUsage(ctx context.Context) {
	if s.usage == nil {
		return
	}
	slog.Debug("MCP servers used", "request_id", requestIDFromContext(ctx), "servers", s.usage.summary(), "EID", "u5McpV8Q")
}

func prepareTools(ctx context.Context, stderrWriter io.Writer, local mcpToolShim, interactive bool) mcpToolShim {
	tools, err := prepareRemoteMCP(ctx, stderrWriter, local)
	if err == nil {
		return tools
	}
	if interactive {
		slog.Warn("remote MCP tools unavailable; continuing without them", "error", err, "EID", "R7vQmA2c")
		_, _ = fmt.Fprintf(stderrWriter, "Warning: remote MCP tools unavailable; continuing without them: %v\n", err)
	}
	logRemoteMCPFallback(ctx)
	return local
}

func logRemoteMCPFallback(ctx context.Context) {
	slog.Debug("MCP servers used", "request_id", requestIDFromContext(ctx), "servers", []remoteMCPServerUsage{}, "EID", "u5McpV8Q")
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
	if s.usage != nil {
		s.usage.record(ref.url, ref.name)
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
	if err := validateMCPAuthorizationURL(authorizationURL); err != nil {
		return err
	}
	if !guiSessionAvailable() {
		return printMCPAuthorizationURL(stderrWriter, authorizationURL, nil)
	}
	if err := launchMCPAuthorization(ctx, authorizationURL); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return printMCPAuthorizationURL(stderrWriter, authorizationURL, err)
	}
	return nil
}

func launchMCPAuthorization(ctx context.Context, authorizationURL string) error {
	if err := validateMCPAuthorizationURL(authorizationURL); err != nil {
		return err
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
		return fmt.Errorf("opening authorization page: %w", err)
	}
	return nil
}

func validateMCPAuthorizationURL(authorizationURL string) error {
	parsed, err := url.Parse(authorizationURL)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("broker returned an invalid OAuth authorization URL")
	}
	return nil
}

func printMCPAuthorizationURL(writer io.Writer, authorizationURL string, launchErr error) error {
	if launchErr != nil {
		if _, err := fmt.Fprintf(writer, "Could not open the browser: %v\n", launchErr); err != nil {
			return errors.Join(launchErr, err)
		}
	}
	if _, err := fmt.Fprintf(writer, "Open this URL to authorize the MCP server:\n%s\n", authorizationURL); err != nil {
		if launchErr != nil {
			return errors.Join(launchErr, err)
		}
		return err
	}
	return nil
}

func guiSessionAvailable() bool {
	switch runtime.GOOS {
	case "darwin", "windows":
		return true
	default:
		return strings.TrimSpace(os.Getenv("DISPLAY")) != "" ||
			strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")) != ""
	}
}
