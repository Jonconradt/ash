package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"ash/internal/brokerproto"
	mcpclient "ash/internal/mcp"
)

type mcpAddOptions struct {
	url       string
	noBrowser bool
}

func runMCP(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		printMCPUsage(stderr)
		return 0
	}
	if args[0] != "add" {
		_, _ = fmt.Fprintf(stderr, "unsupported MCP command %q\n", args[0])
		printMCPUsage(stderr)
		return 2
	}
	if len(args) > 1 && (args[1] == "--help" || args[1] == "-h") {
		printMCPUsage(stderr)
		return 0
	}
	return runMCPAdd(args[1:], stdout, stderr)
}

func printMCPUsage(writer io.Writer) {
	_, _ = fmt.Fprintln(writer, "usage: ash mcp add <url> [--no-browser]")
	_, _ = fmt.Fprintln(writer, "       ash mcp --help")
}

func parseMCPAddArgs(args []string) (mcpAddOptions, error) {
	var options mcpAddOptions
	for _, arg := range args {
		switch arg {
		case "--no-browser":
			if options.noBrowser {
				return mcpAddOptions{}, errors.New("--no-browser may only be specified once")
			}
			options.noBrowser = true
		case "--help", "-h":
			return mcpAddOptions{}, errors.New("usage: ash mcp add <url> [--no-browser]")
		default:
			if strings.HasPrefix(arg, "-") {
				return mcpAddOptions{}, fmt.Errorf("unknown MCP add option %q", arg)
			}
			if options.url != "" {
				return mcpAddOptions{}, errors.New("ash mcp add accepts exactly one server URL")
			}
			options.url = arg
		}
	}
	if options.url == "" {
		return mcpAddOptions{}, errors.New("MCP server URL is required")
	}
	return options, nil
}

func runMCPAdd(args []string, stdout, stderr io.Writer) int {
	options, err := parseMCPAddArgs(args)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		printMCPUsage(stderr)
		return 2
	}
	if !stdinIsInteractive() {
		_, _ = fmt.Fprintln(stderr, "ash mcp add requires an interactive terminal")
		return 1
	}

	servers, err := mcpclient.RemoteServersFromAllowlist(options.url)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "invalid MCP server URL: %v\n", err)
		return 1
	}
	if len(servers) != 1 {
		_, _ = fmt.Fprintln(stderr, "MCP server URL must begin with http:// or https://")
		return 1
	}
	server := servers[0]
	registrationURL := strings.TrimSpace(options.url)
	allowlistPath, err := resolveMCPAllowlistPath()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "resolving .ash_allow: %v\n", err)
		return 1
	}
	if err := checkMCPRegistration(allowlistPath, server); err != nil {
		_, _ = fmt.Fprintf(stderr, "checking .ash_allow: %v\n", err)
		return 1
	}
	if !brokerConfigured() {
		_, _ = fmt.Fprintln(stderr, "the Ash MCP broker is unavailable; run this command from a supported interactive Ash shell")
		return 1
	}

	ctx, stop := signalNotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if _, err := fmt.Fprintf(stdout, "Connecting to MCP server %s\n", server.URL); err != nil {
		_, _ = fmt.Fprintf(stderr, "writing setup status: %v\n", err)
		return 1
	}
	if err := connectMCPForSetup(ctx, server, options.noBrowser, stdout); err != nil {
		if errors.Is(err, context.Canceled) {
			_, _ = fmt.Fprintln(stderr, "MCP setup canceled; no server registration was written")
			return 130
		}
		if errors.Is(err, context.DeadlineExceeded) {
			_, _ = fmt.Fprintln(stderr, "MCP setup timed out; no server registration was written")
			return 1
		}
		_, _ = fmt.Fprintf(stderr, "MCP setup failed: %v\n", err)
		return 1
	}
	if err := addMCPRegistration(allowlistPath, server, registrationURL); err != nil {
		_, _ = fmt.Fprintf(stderr, "MCP server connected, but registration failed: %v\n", err)
		return 1
	}
	if _, err := fmt.Fprintf(stdout, "MCP server connected and registered in %s\n", allowlistPath); err != nil {
		_, _ = fmt.Fprintf(stderr, "MCP server is connected and registered in %s, but writing setup status failed: %v\n", allowlistPath, err)
		return 1
	}
	return 0
}

func connectMCPForSetup(ctx context.Context, server mcpclient.RemoteServer, noBrowser bool, stdout io.Writer) error {
	response, err := brokerMCPDo(ctx, brokerproto.Request{
		MCPAction: "start",
		MCPServer: server.Name,
		MCPURL:    server.URL,
	})
	if err != nil {
		return fmt.Errorf("starting server: %w", err)
	}
	status, err := remoteStatus(response)
	if err != nil {
		return err
	}
	authURL := ""
	for {
		if status.Error != "" {
			return errors.New(status.Error)
		}
		if status.State == "connected" {
			return nil
		}
		if status.AuthURL != "" && status.AuthURL != authURL {
			authURL = status.AuthURL
			if err := presentMCPAuthorization(ctx, authURL, noBrowser, stdout); err != nil {
				return err
			}
			if _, err := brokerMCPDo(ctx, brokerproto.Request{MCPAction: "auth_ack", MCPServer: server.Name}); err != nil {
				return fmt.Errorf("acknowledging authorization URL: %w", err)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
		response, err = brokerMCPDo(ctx, brokerproto.Request{MCPAction: "status_now", MCPServer: server.Name})
		if err != nil {
			return fmt.Errorf("waiting for server connection: %w", err)
		}
		status, err = remoteStatus(response)
		if err != nil {
			return err
		}
	}
}

func presentMCPAuthorization(ctx context.Context, authorizationURL string, noBrowser bool, stdout io.Writer) error {
	if err := validateMCPAuthorizationURL(authorizationURL); err != nil {
		return err
	}
	if !noBrowser && guiSessionAvailable() {
		if err := launchMCPAuthorization(ctx, authorizationURL); err == nil {
			if _, writeErr := fmt.Fprintln(stdout, "Authorization page opened in your browser."); writeErr != nil {
				return fmt.Errorf("writing setup status: %w", writeErr)
			}
		} else if ctx.Err() != nil {
			return ctx.Err()
		} else if err := printMCPAuthorizationURL(stdout, authorizationURL, err); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintln(stdout, "No browser was opened. Authorize this server in a browser on this host:"); err != nil {
			return fmt.Errorf("writing authorization instructions: %w", err)
		}
		if _, err := fmt.Fprintln(stdout, authorizationURL); err != nil {
			return fmt.Errorf("writing authorization URL: %w", err)
		}
	}
	_, err := fmt.Fprintln(stdout, "Waiting for OAuth authorization to complete; press Ctrl-C to stop waiting.")
	if err != nil {
		return fmt.Errorf("writing setup status: %w", err)
	}
	return nil
}

func resolveMCPAllowlistPath() (string, error) {
	root, err := ashWorkspaceDir()
	if err != nil {
		return "", err
	}
	cwd, err := osGetwd()
	if err != nil {
		return "", err
	}
	home, err := osUserHomeDir()
	if err != nil {
		return "", err
	}
	for _, path := range []string{
		filepath.Join(root, allowFileName),
		filepath.Join(cwd, allowFileName),
		filepath.Join(home, allowFileName),
	} {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return "", fmt.Errorf("%s is not a regular file", path)
		}
		return path, nil
	}
	return filepath.Join(root, allowFileName), nil
}

func checkMCPRegistration(path string, server mcpclient.RemoteServer) error {
	info, err := os.Lstat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
		return fmt.Errorf("%s is not a regular file", path)
	}
	content, err := readMCPAllowlist(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	servers, err := mcpclient.RemoteServersFromAllowlist(string(content))
	if err != nil {
		return err
	}
	for _, existing := range servers {
		switch {
		case existing.URL == server.URL:
			return errors.New("this MCP server URL is already registered")
		case existing.Name == server.Name:
			return errors.New("MCP server name hash collision with an existing registration")
		}
	}
	return nil
}

func addMCPRegistration(path string, server mcpclient.RemoteServer, registrationURL string) error {
	if err := checkMCPRegistration(path, server); err != nil {
		return err
	}
	registered, err := mcpclient.RemoteServersFromAllowlist(registrationURL)
	if err != nil || len(registered) != 1 || registered[0].Name != server.Name || registered[0].URL != server.URL {
		return errors.New("MCP registration URL does not match the connected server")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating .ash workspace: %w", err)
	}
	content, err := readMCPAllowlist(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reading .ash_allow: %w", err)
	}
	if len(content) > 0 && content[len(content)-1] != '\n' {
		content = append(content, '\n')
	}
	content = append(content, strings.TrimSpace(registrationURL)...)
	content = append(content, '\n')

	temp, err := os.CreateTemp(filepath.Dir(path), ".ash_allow-*")
	if err != nil {
		return fmt.Errorf("creating temporary .ash_allow: %w", err)
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("setting .ash_allow permissions: %w", err)
	}
	if _, err := temp.Write(content); err != nil {
		_ = temp.Close()
		return fmt.Errorf("writing .ash_allow: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("syncing .ash_allow: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("closing .ash_allow: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replacing .ash_allow: %w", err)
	}
	return nil
}

func readMCPAllowlist(path string) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()

	file, err := root.OpenFile(filepath.Base(path), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(file)
}
