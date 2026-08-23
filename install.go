package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	installStartMarker = "# >>> ash install >>>"
	installEndMarker   = "# <<< ash install <<<"
)

type endpointPreset struct {
	Name string
	URL  string
}

var installEndpointPresets = []endpointPreset{
	{Name: "Ollama (local)", URL: "http://localhost:11434"},
	{Name: "Ollama (cloud)", URL: "https://ollama.com"},
	{Name: "OpenAI", URL: "https://api.openai.com/v1"},
	{Name: "Anthropic", URL: "https://api.anthropic.com/v1"},
	{Name: "Google Gemini (OpenAI-compatible)", URL: "https://generativelanguage.googleapis.com/v1beta/openai/"},
	{Name: "HuggingFace Router (OpenAI-compatible)", URL: "https://router.huggingface.co/v1"},
}

// runInstall runs the requested operation.
func runInstall(args []string, stdout, stderr io.Writer) int {
	configureDebugLogging(stderr)

	shellName, dryRun, overwrite, err := parseInstallArgs(args)
	if err != nil {
		slog.Error(fmt.Sprintf("install error: %v", err), "EID", "EpbG2YtZ")
		printUsage(stderr)
		return 1
	}

	if shellName == "" {
		shellName = defaultInstallShell(os.Getenv("SHELL"), activeGOOS())
	}

	rcPath, err := rcPathForShell(shellName)
	if err != nil {
		slog.Error(fmt.Sprintf("install error: %v", err), "EID", "n0lHsTQp")
		return 1
	}

	block := installSourceBlockForShell(shellName)
	if block == "" {
		slog.Error("install error: unsupported shell", "shell", shellName, "EID", "ZIw1nK74")
		return 1
	}
	if err := ensureInstallShellWrapper(shellName, dryRun, stdout); err != nil {
		slog.Error(fmt.Sprintf("install error: %v", err), "EID", "RtRyQwEX")
		return 1
	}
	if err := ensureShellPostInstall(shellName, dryRun, stdout); err != nil {
		slog.Error(fmt.Sprintf("install error: %v", err), "EID", "bnjrQttE")
		return 1
	}
	if err := installEmbeddedBootstrapAssets(overwrite, stdout); err != nil {
		slog.Error(fmt.Sprintf("install error: %v", err), "EID", "Hrs2Jw5A")
		return 1
	}

	existing, err := readFileIfExists(rcPath)
	if err != nil {
		slog.Error("install error: failed to read rc file", "path", rcPath, "error", err, "EID", "uUVX5Blo")
		return 1
	}

	existingBlock, hasManagedBlock := extractManagedInstallBlock(existing)
	if hasManagedBlock {
		if strings.TrimSpace(existingBlock) == strings.TrimSpace(block) {
			if err := finalizeInstallWorkspace(); err != nil {
				slog.Error(fmt.Sprintf("install error: %v", err), "EID", "Ez8nV4zY")
				return 1
			}
			if err := maybeConfigureInstallEnv(stdout, stderr, dryRun); err != nil {
				slog.Error(fmt.Sprintf("install error: %v", err), "EID", "j6SE1V4c")
				return 1
			}
			_, _ = fmt.Fprintf(stdout, "ash install already present in %s\n", rcPath)
			_, _ = fmt.Fprintln(stdout, "synced .ash_system/.ash_tools to ~/.ash when present")
			return 0
		}

		updated, replaced := replaceManagedInstallBlock(existing, block)
		if !replaced {
			slog.Error("install error: failed to update managed block", "path", rcPath, "EID", "qNtX7PSU")
			return 1
		}

		if dryRun {
			_, _ = fmt.Fprintf(stdout, "[dry-run] would update install block in %s\n", rcPath)
			_, _ = fmt.Fprint(stdout, block)
			if !strings.HasSuffix(block, "\n") {
				_, _ = fmt.Fprintln(stdout)
			}
			return 0
		}

		if err := osMkdirAll(filepath.Dir(rcPath), 0o700); err != nil {
			slog.Error("install error: failed to create rc parent directory", "path", rcPath, "error", err, "EID", "AB5qbz5c")
			return 1
		}

		if err := osWriteFile(rcPath, []byte(updated), 0o600); err != nil {
			slog.Error("install error: failed to write rc file", "path", rcPath, "error", err, "EID", "J3crjWvv")
			return 1
		}
		if err := finalizeInstallWorkspace(); err != nil {
			slog.Error(fmt.Sprintf("install error: %v", err), "EID", "J6UlMz4P")
			return 1
		}
		if err := maybeConfigureInstallEnv(stdout, stderr, dryRun); err != nil {
			slog.Error(fmt.Sprintf("install error: %v", err), "EID", "55cquv9b")
			return 1
		}

		_, _ = fmt.Fprintf(stdout, "ash install updated wrappers in %s\n", rcPath)
		_, _ = fmt.Fprintln(stdout, "synced .ash_system/.ash_tools to ~/.ash when present")
		_, _ = fmt.Fprintln(stdout, "restart your shell or source your rc file to activate wrappers")
		return 0
	}

	updated := appendInstallBlock(existing, block)
	if dryRun {
		_, _ = fmt.Fprintf(stdout, "[dry-run] would append install block to %s\n", rcPath)
		_, _ = fmt.Fprint(stdout, block)
		if !strings.HasSuffix(block, "\n") {
			_, _ = fmt.Fprintln(stdout)
		}
		return 0
	}

	if err := osMkdirAll(filepath.Dir(rcPath), 0o700); err != nil {
		slog.Error("install error: failed to create rc parent directory", "path", rcPath, "error", err, "EID", "G2nscOa7")
		return 1
	}

	if err := osWriteFile(rcPath, []byte(updated), 0o600); err != nil {
		slog.Error("install error: failed to write rc file", "path", rcPath, "error", err, "EID", "juoFEIwD")
		return 1
	}
	if err := finalizeInstallWorkspace(); err != nil {
		slog.Error(fmt.Sprintf("install error: %v", err), "EID", "SyhbVcO3")
		return 1
	}
	if err := maybeConfigureInstallEnv(stdout, stderr, dryRun); err != nil {
		slog.Error(fmt.Sprintf("install error: %v", err), "EID", "BwUzNiql")
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "ash install appended wrappers to %s\n", rcPath)
	_, _ = fmt.Fprintln(stdout, "synced .ash_system/.ash_tools to ~/.ash when present")
	_, _ = fmt.Fprintln(stdout, "restart your shell or source your rc file to activate wrappers")
	return 0
}

// maybeConfigureInstallEnv writes managed environment settings to the user workspace when the install flow is interactive and needs configuration.
func maybeConfigureInstallEnv(stdout, stderr io.Writer, dryRun bool) error {
	if dryRun {
		return nil
	}

	shouldConfigure, err := shouldConfigureInstallEnv()
	if err != nil {
		return err
	}
	if !shouldConfigure || !shouldPromptInstallEnv() {
		return nil
	}

	reader := bufio.NewReader(os.Stdin)
	values, err := promptInstallEnvValues(reader, stdout)
	if err != nil {
		return err
	}
	if len(values) == 0 {
		return nil
	}

	path, err := ashEnvFilePath()
	if err != nil {
		return err
	}
	if err := osMkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	if err := osWriteFile(path, []byte(buildManagedAshEnv(values)), 0o600); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "updated %s\n", path)
	return nil
}

// shouldConfigureInstallEnv reports whether the condition is true.
func shouldConfigureInstallEnv() (bool, error) {
	if hasRequiredInstallEnvValues() {
		return false, nil
	}

	path, err := ashEnvFilePath()
	if err != nil {
		return false, err
	}

	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if errors.Is(err, os.ErrNotExist) {
		return true, nil
	} else {
		return false, err
	}
}

// hasRequiredInstallEnvValues reports whether the condition is true.
func hasRequiredInstallEnvValues() bool {
	endpoint := strings.TrimSpace(os.Getenv(aiEnvEndpoint))
	if endpoint == "" {
		return false
	}

	model := strings.TrimSpace(os.Getenv(aiEnvModel))
	if model == "" {
		return false
	}

	_, host, _, err := parseAIEndpoint(endpoint)
	if err != nil {
		return false
	}

	if !isCloudAIHost(host) {
		return true
	}

	authToken := strings.TrimSpace(os.Getenv(aiEnvAuthToken))
	return authToken != ""
}

// shouldPromptInstallEnv reports whether the condition is true.
func shouldPromptInstallEnv() bool {
	if runningInCI() {
		return false
	}
	stdinInfo, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	stdoutInfo, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (stdinInfo.Mode()&os.ModeCharDevice != 0) && (stdoutInfo.Mode()&os.ModeCharDevice != 0)
}

// runningInCI runs the requested operation.
func runningInCI() bool {
	for _, key := range []string{"CI", "GITHUB_ACTIONS", "BUILD_BUILDID", "JENKINS_URL", "BUILDKITE"} {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			return true
		}
	}
	return false
}

// ashEnvFilePath returns the path to the managed ash environment file inside the workspace.
func ashEnvFilePath() (string, error) {
	root, err := ashWorkspaceDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, ".ash_env"), nil
}

// promptInstallEnvValues collects the AI endpoint and authentication values needed to create a managed ash environment file.
func promptInstallEnvValues(reader *bufio.Reader, stdout io.Writer) (map[string]string, error) {
	_, _ = fmt.Fprintln(stdout, "Configure ash environment values")
	endpoint, err := promptEndpointWithPresets(reader, stdout)
	if err != nil {
		return nil, err
	}
	model, err := promptNonEmpty(reader, stdout, aiEnvModel)
	if err != nil {
		return nil, err
	}

	_, host, _, err := parseAIEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	cloud := isCloudAIHost(host)

	authToken := ""
	if cloud {
		authToken, err = promptNonEmpty(reader, stdout, aiEnvAuthToken)
		if err != nil {
			return nil, err
		}
	} else {
		optionalToken, promptErr := promptOptional(reader, stdout, aiEnvAuthToken+" (optional for localhost)")
		if promptErr != nil {
			return nil, promptErr
		}
		if optionalToken != "" {
			authToken = optionalToken
		}
	}

	values := map[string]string{
		aiEnvEndpoint: endpoint,
		aiEnvModel:    model,
	}
	if authToken != "" {
		values[aiEnvAuthToken] = authToken
	}
	return values, nil
}

// promptEndpointWithPresets prompts for an AI endpoint, accepting either a preset choice or a custom URL.
func promptEndpointWithPresets(reader *bufio.Reader, stdout io.Writer) (string, error) {
	_, _ = fmt.Fprintln(stdout, "Select AI endpoint preset or enter a custom URL:")
	for i, preset := range installEndpointPresets {
		_, _ = fmt.Fprintf(stdout, "  %d) %s - %s\n", i+1, preset.Name, preset.URL)
	}

	for {
		_, _ = fmt.Fprintf(stdout, "%s:  ", aiEnvEndpoint)
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		input := strings.TrimSpace(line)
		if input == "" {
			continue
		}
		if idx, convErr := strconv.Atoi(input); convErr == nil {
			if idx >= 1 && idx <= len(installEndpointPresets) {
				return installEndpointPresets[idx-1].URL, nil
			}
		}
		if _, _, _, parseErr := parseAIEndpoint(input); parseErr == nil {
			return strings.TrimRight(input, "/"), nil
		}
		_, _ = fmt.Fprintln(stdout, "invalid endpoint, enter a preset number or full http(s) URL")
	}
}

// promptNonEmpty reads a non-empty value from the user for the provided prompt key.
func promptNonEmpty(reader *bufio.Reader, stdout io.Writer, key string) (string, error) {
	for {
		_, _ = fmt.Fprintf(stdout, "%s:  ", key)
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		value := strings.TrimSpace(line)
		if value != "" {
			return value, nil
		}
	}
}

// promptOptional reads an optional value from the user for the provided prompt key.
func promptOptional(reader *bufio.Reader, stdout io.Writer, key string) (string, error) {
	_, _ = fmt.Fprintf(stdout, "%s:  ", key)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// buildManagedAshEnv renders the managed ash environment file content from the supplied key/value settings.
func buildManagedAshEnv(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("# managed by ash install\n")
	b.WriteString("export SESSION_ID=`head -c 100 /dev/urandom | LC_ALL=C tr -dc 'a-zA-Z0-9' | fold -w 16 | head -n 1`\n")
	b.WriteString("export PATH=\"$HOME/.ash/tools:$PATH\"\n")
	for _, key := range keys {
		fmt.Fprintf(&b, "export %s=%s\n", key, shellQuote(values[key]))
	}
	return b.String()
}

// finalizeInstallWorkspace syncs canonical config files into the workspace and hardens workspace permissions.
func finalizeInstallWorkspace() error {
	if err := syncCanonicalConfigFilesFromCWD(); err != nil {
		return err
	}
	if err := hardenAshWorkspacePermissions(); err != nil {
		return err
	}
	return nil
}

// syncCanonicalConfigFilesFromCWD copies the current directory's ash config files into the canonical workspace directory when present.
func syncCanonicalConfigFilesFromCWD() error {
	root, err := ashWorkspaceDir()
	if err != nil {
		return err
	}
	if err := osMkdirAll(root, 0o700); err != nil {
		return err
	}

	cwd, err := osGetwd()
	if err != nil {
		return err
	}

	for _, name := range []string{systemFileName, toolsFileName} {
		srcPath := filepath.Join(cwd, name)
		content, readErr := osReadFile(srcPath)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return fmt.Errorf("failed to read %s: %w", srcPath, readErr)
		}
		dstPath := filepath.Join(root, name)
		if writeErr := osWriteFile(dstPath, content, 0o600); writeErr != nil {
			return fmt.Errorf("failed to write %s: %w", dstPath, writeErr)
		}
	}

	return nil
}

// hardenAshWorkspacePermissions restricts workspace directories and files to the current user.
func hardenAshWorkspacePermissions() error {
	root, err := ashWorkspaceDir()
	if err != nil {
		return err
	}
	if err := osMkdirAll(root, 0o700); err != nil {
		return err
	}
	// #nosec G302 -- the workspace root must remain accessible only to the current user.
	if err := os.Chmod(root, 0o700); err != nil {
		return err
	}
	rootDir, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer func() { _ = rootDir.Close() }()

	return filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := info.Mode()
		if mode&os.ModeSymlink != 0 {
			return nil
		}

		if d.IsDir() {
			// #nosec G302 -- directories in the workspace need restricted access for the current user only.
			relativePath, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			return rootDir.Chmod(relativePath, 0o700)
		}
		if mode.IsRegular() {
			relativePath, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			return rootDir.Chmod(relativePath, 0o600)
		}
		return nil
	})
}

// parseInstallArgs validates command-line arguments for the install subcommand and returns the parsed options.
func parseInstallArgs(args []string) (shellName string, dryRun bool, overwrite bool, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dry-run":
			dryRun = true
		case "--overwrite":
			overwrite = true
		case "--shell":
			i++
			if i >= len(args) {
				return "", false, false, errors.New("--shell requires a value")
			}
			shellName = strings.TrimSpace(strings.ToLower(args[i]))
		default:
			return "", false, false, fmt.Errorf("unknown install argument: %s", args[i])
		}
	}
	return shellName, dryRun, overwrite, nil
}

// detectShellName returns the canonical shell name for a shell executable path, or empty if unsupported.
func detectShellName(shellPath string) string {
	base := strings.TrimSpace(shellPath)
	base = strings.TrimRight(base, "\\/")
	if idx := strings.LastIndexAny(base, "\\/"); idx >= 0 {
		base = base[idx+1:]
	}
	canonical := normalizeShellName(base)
	if _, ok := installShellTargets[canonical]; ok {
		return canonical
	}
	return ""
}

// rcPathForShell returns the user rc file path for the supplied shell name.
func rcPathForShell(shellName string) (string, error) {
	target, err := resolveInstallShellTarget(shellName, activeGOOS())
	if err != nil {
		return "", err
	}

	home, err := osUserHomeDir()
	if err != nil {
		return "", err
	}

	return target.RCPath(home), nil
}

// readFileIfExists returns the contents of path when it exists, or an empty string when the file is absent.
func readFileIfExists(path string) (string, error) {
	content, err := osReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(content), nil
}

// appendInstallBlock appends a managed install block to existing shell config content.
func appendInstallBlock(existing, block string) string {
	if existing == "" {
		return block + "\n"
	}

	updated := existing
	if !strings.HasSuffix(updated, "\n") {
		updated += "\n"
	}
	updated += "\n" + block + "\n"
	return updated
}

// extractManagedInstallBlock returns the managed ash install block from content when present.
func extractManagedInstallBlock(content string) (string, bool) {
	start := strings.Index(content, installStartMarker)
	if start < 0 {
		return "", false
	}
	endRel := strings.Index(content[start:], installEndMarker)
	if endRel < 0 {
		return "", false
	}
	end := start + endRel + len(installEndMarker)
	return content[start:end], true
}

// replaceManagedInstallBlock replaces the existing managed ash install block in the supplied content.
func replaceManagedInstallBlock(existing, block string) (string, bool) {
	start := strings.Index(existing, installStartMarker)
	if start < 0 {
		return "", false
	}
	endRel := strings.Index(existing[start:], installEndMarker)
	if endRel < 0 {
		return "", false
	}
	end := start + endRel + len(installEndMarker)

	prefix := existing[:start]
	suffix := existing[end:]
	suffix = strings.TrimPrefix(suffix, "\n")

	var b strings.Builder
	b.WriteString(prefix)
	if prefix != "" && !strings.HasSuffix(prefix, "\n") {
		b.WriteString("\n")
	}
	b.WriteString(block)
	b.WriteString("\n")
	if strings.TrimSpace(suffix) != "" {
		b.WriteString("\n")
		b.WriteString(suffix)
	}
	return b.String(), true
}

// installRecommendation returns guidance for installing or updating ash for the current shell, if needed.
func installRecommendation() (string, error) {
	shellName := detectShellName(os.Getenv("SHELL"))
	if shellName == "" {
		if activeGOOS() == "windows" {
			shellName = shellPwsh
		} else {
			return "", nil
		}
	}

	target, ok := installShellTargets[shellName]
	if !ok || !target.SupportedOnOS(activeGOOS()) {
		return "", nil
	}

	if shellName == "bash" {
		installedViaProfile, err := bashInstalledViaProfileSourcing()
		if err != nil {
			return "", err
		}
		if installedViaProfile {
			return "", nil
		}
	}

	rcPath, err := rcPathForShell(shellName)
	if err != nil {
		return "", err
	}

	content, err := readFileIfExists(rcPath)
	if err != nil {
		return "", err
	}

	expected := installSourceBlockForShell(shellName)
	if existing, ok := extractManagedInstallBlock(content); ok {
		if strings.TrimSpace(existing) == strings.TrimSpace(expected) {
			return "", nil
		}
		return fmt.Sprintf("ash install for %s is outdated. Run: ash install --shell %s", shellName, shellName), nil
	}

	return fmt.Sprintf("ash is not installed for %s. Run: ash install --shell %s", shellName, shellName), nil
}

// bashInstalledViaProfileSourcing reports whether bash is already configured to source ash through .bash_profile.
func bashInstalledViaProfileSourcing() (bool, error) {
	home, err := osUserHomeDir()
	if err != nil {
		return false, err
	}

	profilePath := filepath.Join(home, ".bash_profile")
	profileContent, err := readFileIfExists(profilePath)
	if err != nil {
		return false, err
	}
	if !strings.Contains(profileContent, ".ash/.ash_bashrc") {
		return false, nil
	}

	wrapperPath, err := installShellWrapperPath("bash")
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(wrapperPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

// installSourceBlockForShell returns the shell snippet that sources ash for the supplied shell name.
func installSourceBlockForShell(shellName string) string {
	target, err := resolveInstallShellTarget(shellName, activeGOOS())
	if err != nil {
		return ""
	}

	return target.SourceBlock()
}

// installShellWrapperPath returns the path to the shell wrapper file used by ash for the supplied shell.
func installShellWrapperPath(shellName string) (string, error) {
	target, err := resolveInstallShellTarget(shellName, activeGOOS())
	if err != nil {
		return "", err
	}

	root, err := ashWorkspaceDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(root, target.WrapperFile), nil
}

// ensureInstallShellWrapper ensures required state exists and is up to date.
func ensureInstallShellWrapper(shellName string, dryRun bool, stdout io.Writer) error {
	target, err := resolveInstallShellTarget(shellName, activeGOOS())
	if err != nil {
		return err
	}

	content := target.WrapperContent()

	path, err := installShellWrapperPath(target.Name)
	if err != nil {
		return err
	}
	if dryRun {
		_, _ = fmt.Fprintf(stdout, "[dry-run] would write shell wrapper file %s\n", path)
		return nil
	}

	if err := osMkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return osWriteFile(path, []byte(content+"\n"), 0o600)
}

// ensureShellPostInstall runs shell-specific post-install actions when needed.
func ensureShellPostInstall(shellName string, dryRun bool, stdout io.Writer) error {
	target, err := resolveInstallShellTarget(shellName, activeGOOS())
	if err != nil {
		return err
	}
	if target.PostInstall == nil {
		return nil
	}
	return target.PostInstall(dryRun, stdout)
}
