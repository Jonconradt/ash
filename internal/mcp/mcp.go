package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// type containing the lifecycle of MCP services
type MCP struct {
	Servers []DiscoveredServer
}

// DiscoveredServer represents an abstracted definition of an MCP server found on disk
type DiscoveredServer struct {
	HostApp string
	Name    string
	Command string
	Args    []string
	Env     map[string]string
}

// Struct definitions to match different AI app configuration schemas
type StandardJsonConfig struct {
	McpServers map[string]struct {
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	} `json:"mcpServers"`
}

type ZedConfig struct {
	ContextServers map[string]struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	} `json:"context_servers"`
}

type VsCodeSettings struct {
	ClineMcpServers map[string]struct {
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	} `json:"cline.mcpServers"`
}

func New() {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	slog.Debug("🔍 Scanning system for MCP configurations...", "EID", "aNRQGn90")
	servers := scanAllConfigs()

	if len(servers) == 0 {
		slog.Error("❌ No MCP server configurations were found on your machine.", "EID", "zkze9Uqo")
		return
	}

	slog.Debug(fmt.Sprintf("🎉 Found %d configured server definitions. Testing connectivity...\n\n", len(servers)), "EID",

		// Instantiate the MCP universal discovery client
		"znWgBefQ")

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "go-universal-discoverer",
		Version: "1.1.0",
	}, nil)

	for _, srv := range servers {
		slog.Info(fmt.Sprintf("🔌 [%s] Testing server '%s'...\n", strings.ToUpper(srv.HostApp), srv.Name), "EID", "cMCcN08x")
		slog.Info(fmt.Sprintf("   Command: %s %v\n", srv.Command, srv.Args), "EID",

			// Prepare standard OS subprocess command using CommandContext
			// #nosec G204 -- This explicit discovery runs commands from the user's configured MCP servers.
			"vVGZQSBF")

		cmd := exec.CommandContext(ctx, srv.Command, srv.Args...)
		if len(srv.Env) > 0 {
			cmd.Env = os.Environ()
			for k, v := range srv.Env {
				cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
			}
		}

		// Initialize transport channel layer
		transport := &mcp.CommandTransport{Command: cmd} // Satisfies Transport interface implicitly
		session, err := client.Connect(ctx, transport, nil)
		if err != nil {
			slog.Error(fmt.Sprintf("   ❌ Connection failed: %v\n\n", err), "EID", "Q4mT8xKB")
			continue
		}

		// Discover capabilities via the session lifecycle
		toolsResult, err := session.ListTools(ctx, nil)
		if err != nil {
			slog.Warn(fmt.Sprintf("   ⚠️ Managed to connect, but couldn't fetch tools: %v\n", err), "EID", "hYc3M2zm")
		} else {
			slog.Info(fmt.Sprintf("   ✅ Successfully connected! Discovered %d tools:\n", len(toolsResult.Tools)), "EID", "29wqqfFk")
			for _, tool := range toolsResult.Tools {
				slog.Info(fmt.Sprintf("      - %s: %s\n", tool.Name, tool.Description), "EID", "68RUZe2K")
			}
		}

		if err := session.Close(); err != nil {
			slog.Warn("Failed to close MCP session", "server", srv.Name, "error", err, "EID", "MKy70mb5")
		}
		slog.Debug(fmt.Sprintf("Init() - Closed session for server '%s'", srv.Name), "EID", "oLFOWK3i")
	}
}

func scanAllConfigs() []DiscoveredServer {
	var list []DiscoveredServer
	home, _ := os.UserHomeDir()
	appData := os.Getenv("APPDATA")

	// 1. Claude Desktop (JSON format)
	var claudePath string
	switch runtime.GOOS {
	case "windows":
		claudePath = filepath.Join(appData, "Claude", "claude_desktop_config.json")
	case "darwin":
		claudePath = filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	default:
		claudePath = filepath.Join(home, ".config", "Claude", "claude_desktop_config.json")
	}
	list = append(list, parseStandardJson(claudePath, "Claude Desktop")...)

	// 2. Claude Code (JSON format)
	claudeCodePath := filepath.Join(home, ".claude.json")
	if runtime.GOOS == "windows" {
		claudeCodePath = filepath.Join(os.Getenv("USERPROFILE"), ".claude.json")
	}
	list = append(list, parseStandardJson(claudeCodePath, "Claude Code")...)

	// 3. Cursor IDE (JSON format)
	cursorPath := filepath.Join(home, ".cursor", "mcp.json")
	list = append(list, parseStandardJson(cursorPath, "Cursor")...)

	// 4. Zed Editor (Nested context_servers key)
	zedPath := filepath.Join(home, ".config", "zed", "settings.json")
	if runtime.GOOS == "windows" {
		zedPath = filepath.Join(appData, "Zed", "settings.json")
	}
	list = append(list, parseZedJson(zedPath)...)

	// 5. VS Code (Parsing extension keys inside User settings)
	var vscodePath string
	switch runtime.GOOS {
	case "windows":
		vscodePath = filepath.Join(appData, "Code", "User", "settings.json")
	case "darwin":
		vscodePath = filepath.Join(home, "Library", "Application Support", "Code", "User", "settings.json")
	default:
		vscodePath = filepath.Join(home, ".config", "Code", "User", "settings.json")
	}
	list = append(list, parseVsCodeJson(vscodePath)...)

	// 6. OpenAI Codex / Agent CLI (TOML format parser)
	codexPath := filepath.Join(home, ".codex", "config.toml")
	list = append(list, parseCodexToml(codexPath)...)

	// 7. Google Gemini CLI Configuration
	geminiPath := filepath.Join(home, ".gemini", "config", "mcp_config.json")
	list = append(list, parseStandardJson(geminiPath, "Gemini CLI")...)

	return list
}

/* Helper Parsers to Handle Specific App Ecosystems */

func parseStandardJson(path, hostApp string) []DiscoveredServer {
	var found []DiscoveredServer
	data, err := readConfigFile(path)
	if err != nil {
		return found
	}
	var conf StandardJsonConfig
	if err := json.Unmarshal(data, &conf); err == nil {
		for name, s := range conf.McpServers {
			found = append(found, DiscoveredServer{HostApp: hostApp, Name: name, Command: s.Command, Args: s.Args, Env: s.Env})
		}
	}
	return found
}

func parseZedJson(path string) []DiscoveredServer {
	var found []DiscoveredServer
	data, err := readConfigFile(path)
	if err != nil {
		return found
	}
	var conf ZedConfig
	if err := json.Unmarshal(data, &conf); err == nil {
		for name, s := range conf.ContextServers {
			found = append(found, DiscoveredServer{HostApp: "Zed", Name: name, Command: s.Command, Args: s.Args})
		}
	}
	return found
}

func parseVsCodeJson(path string) []DiscoveredServer {
	var found []DiscoveredServer
	data, err := readConfigFile(path)
	if err != nil {
		return found
	}
	var conf VsCodeSettings
	if err := json.Unmarshal(data, &conf); err == nil {
		for name, s := range conf.ClineMcpServers {
			found = append(found, DiscoveredServer{HostApp: "VS Code (Cline)", Name: name, Command: s.Command, Args: s.Args, Env: s.Env})
		}
	}
	return found
}

// Lightweight manual TOML reader to eliminate extra third-party module imports
func parseCodexToml(path string) []DiscoveredServer {
	var found []DiscoveredServer
	data, err := readConfigFile(path)
	if err != nil {
		return found
	}

	var currentServer *DiscoveredServer
	scanner := bufio.NewScanner(strings.NewReader(string(data)))

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Match section header like [mcp_servers.weather]
		if strings.HasPrefix(line, "[mcp_servers.") && strings.HasSuffix(line, "]") {
			if currentServer != nil {
				found = append(found, *currentServer)
			}
			serverName := line[13 : len(line)-1]
			currentServer = &DiscoveredServer{HostApp: "OpenAI Codex", Name: serverName}
			continue
		}
		if currentServer != nil {
			if strings.HasPrefix(line, "command =") {
				currentServer.Command = strings.Trim(line[9:], " \"'")
			} else if strings.HasPrefix(line, "args =") {
				// Naive array processing strip brackets and quotes
				arrStr := strings.Trim(line[6:], " []")
				for _, arg := range strings.Split(arrStr, ",") {
					trimmed := strings.Trim(arg, " \"'")
					if trimmed != "" {
						currentServer.Args = append(currentServer.Args, trimmed)
					}
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		slog.Warn("Failed to read Codex MCP configuration", "path", path, "error", err, "EID", "zmPEPEnp")
	}
	if currentServer != nil {
		found = append(found, *currentServer)
	}
	return found
}

func readConfigFile(path string) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := root.Close(); err != nil {
			slog.Warn("Failed to close MCP config directory", "path", filepath.Dir(path), "error", err, "EID", "Utuc52TR")
		}
	}()
	file, err := root.Open(filepath.Base(path))
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := file.Close(); err != nil {
			slog.Warn("Failed to close MCP configuration", "path", path, "error", err, "EID", "w8xphfTb")
		}
	}()
	return io.ReadAll(file)
}
