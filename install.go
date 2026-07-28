package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
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
	shellName, dryRun, err := parseInstallArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "install error: %v\n", err)
		printUsage(stderr)
		return 1
	}

	if shellName == "" {
		shellName = detectShellName(os.Getenv("SHELL"))
		if shellName == "" {
			shellName = "bash"
		}
	}

	rcPath, err := rcPathForShell(shellName)
	if err != nil {
		fmt.Fprintf(stderr, "install error: %v\n", err)
		return 1
	}

	block := installSourceBlockForShell(shellName)
	if block == "" {
		fmt.Fprintf(stderr, "install error: unsupported shell %q\n", shellName)
		return 1
	}
	if err := ensureInstallShellWrapper(shellName, dryRun, stdout); err != nil {
		fmt.Fprintf(stderr, "install error: %v\n", err)
		return 1
	}
	if err := ensureBashProfileSourcing(shellName, dryRun, stdout); err != nil {
		fmt.Fprintf(stderr, "install error: %v\n", err)
		return 1
	}

	existing, err := readFileIfExists(rcPath)
	if err != nil {
		fmt.Fprintf(stderr, "install error: failed to read %s: %v\n", rcPath, err)
		return 1
	}

	existingBlock, hasManagedBlock := extractManagedInstallBlock(existing)
	if hasManagedBlock {
		if strings.TrimSpace(existingBlock) == strings.TrimSpace(block) {
			if err := finalizeInstallWorkspace(); err != nil {
				fmt.Fprintf(stderr, "install error: %v\n", err)
				return 1
			}
			if err := maybeConfigureInstallEnv(stdout, stderr, dryRun); err != nil {
				fmt.Fprintf(stderr, "install error: %v\n", err)
				return 1
			}
			fmt.Fprintf(stdout, "ash install already present in %s\n", rcPath)
			fmt.Fprintln(stdout, "synced .ash_system/.ash_tools to ~/.ash when present")
			return 0
		}

		updated, replaced := replaceManagedInstallBlock(existing, block)
		if !replaced {
			fmt.Fprintf(stderr, "install error: failed to update managed block in %s\n", rcPath)
			return 1
		}

		if dryRun {
			fmt.Fprintf(stdout, "[dry-run] would update install block in %s\n", rcPath)
			fmt.Fprint(stdout, block)
			if !strings.HasSuffix(block, "\n") {
				fmt.Fprintln(stdout)
			}
			return 0
		}

		if err := osWriteFile(rcPath, []byte(updated), 0o600); err != nil {
			fmt.Fprintf(stderr, "install error: failed to write %s: %v\n", rcPath, err)
			return 1
		}
		if err := finalizeInstallWorkspace(); err != nil {
			fmt.Fprintf(stderr, "install error: %v\n", err)
			return 1
		}
		if err := maybeConfigureInstallEnv(stdout, stderr, dryRun); err != nil {
			fmt.Fprintf(stderr, "install error: %v\n", err)
			return 1
		}

		fmt.Fprintf(stdout, "ash install updated wrappers in %s\n", rcPath)
		fmt.Fprintln(stdout, "synced .ash_system/.ash_tools to ~/.ash when present")
		fmt.Fprintln(stdout, "restart your shell or source your rc file to activate wrappers")
		return 0
	}

	updated := appendInstallBlock(existing, block)
	if dryRun {
		fmt.Fprintf(stdout, "[dry-run] would append install block to %s\n", rcPath)
		fmt.Fprint(stdout, block)
		if !strings.HasSuffix(block, "\n") {
			fmt.Fprintln(stdout)
		}
		return 0
	}

	if err := osWriteFile(rcPath, []byte(updated), 0o600); err != nil {
		fmt.Fprintf(stderr, "install error: failed to write %s: %v\n", rcPath, err)
		return 1
	}
	if err := finalizeInstallWorkspace(); err != nil {
		fmt.Fprintf(stderr, "install error: %v\n", err)
		return 1
	}
	if err := maybeConfigureInstallEnv(stdout, stderr, dryRun); err != nil {
		fmt.Fprintf(stderr, "install error: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "ash install appended wrappers to %s\n", rcPath)
	fmt.Fprintln(stdout, "synced .ash_system/.ash_tools to ~/.ash when present")
	fmt.Fprintln(stdout, "restart your shell or source your rc file to activate wrappers")
	return 0
}

// maybeConfigureInstallEnv returns the computed value for this helper.
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
	values, err := promptInstallEnvValues(reader, stdout, stderr)
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
	fmt.Fprintf(stdout, "updated %s\n", path)
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

	authType := strings.ToLower(strings.TrimSpace(os.Getenv(aiEnvAuthType)))
	authToken := strings.TrimSpace(os.Getenv(aiEnvAuthToken))
	return authType == "bearer" && authToken != ""
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

// ashEnvFilePath returns the computed value for this helper.
func ashEnvFilePath() (string, error) {
	root, err := ashWorkspaceDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, ".ash_env"), nil
}

// promptInstallEnvValues prompts for and returns user input.
func promptInstallEnvValues(reader *bufio.Reader, stdout, stderr io.Writer) (map[string]string, error) {
	fmt.Fprintln(stdout, "Configure ash environment values")
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

	authType := ""
	authToken := ""
	if cloud {
		authType = "bearer"
		authToken, err = promptNonEmpty(reader, stdout, aiEnvAuthToken)
		if err != nil {
			return nil, err
		}
		fmt.Fprintln(stderr, "selected cloud endpoint; using AI_AUTH_TYPE=bearer")
	} else {
		optionalToken, promptErr := promptOptional(reader, stdout, aiEnvAuthToken+" (optional for localhost)")
		if promptErr != nil {
			return nil, promptErr
		}
		if optionalToken != "" {
			authType = "bearer"
			authToken = optionalToken
		}
	}

	values := map[string]string{
		aiEnvEndpoint: endpoint,
		aiEnvModel:    model,
	}
	if authType != "" {
		values[aiEnvAuthType] = authType
	}
	if authToken != "" {
		values[aiEnvAuthToken] = authToken
	}
	return values, nil
}

// promptEndpointWithPresets prompts for and returns user input.
func promptEndpointWithPresets(reader *bufio.Reader, stdout io.Writer) (string, error) {
	fmt.Fprintln(stdout, "Select AI endpoint preset or enter a custom URL:")
	for i, preset := range installEndpointPresets {
		fmt.Fprintf(stdout, "  %d) %s - %s\n", i+1, preset.Name, preset.URL)
	}

	for {
		fmt.Fprintf(stdout, "%s: ", aiEnvEndpoint)
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
		fmt.Fprintln(stdout, "invalid endpoint, enter a preset number or full http(s) URL")
	}
}

// promptNonEmpty prompts for and returns user input.
func promptNonEmpty(reader *bufio.Reader, stdout io.Writer, key string) (string, error) {
	for {
		fmt.Fprintf(stdout, "%s: ", key)
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

// promptOptional prompts for and returns user input.
func promptOptional(reader *bufio.Reader, stdout io.Writer, key string) (string, error) {
	fmt.Fprintf(stdout, "%s: ", key)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// buildManagedAshEnv builds and returns a derived value.
func buildManagedAshEnv(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("# managed by ash install\n")
	for _, key := range keys {
		b.WriteString(fmt.Sprintf("export %s=%s\n", key, shellQuote(values[key])))
	}
	return b.String()
}

// finalizeInstallWorkspace returns the computed value for this helper.
func finalizeInstallWorkspace() error {
	if err := syncCanonicalConfigFilesFromCWD(); err != nil {
		return err
	}
	if err := hardenAshWorkspacePermissions(); err != nil {
		return err
	}
	return nil
}

// syncCanonicalConfigFilesFromCWD returns the computed value for this helper.
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

// hardenAshWorkspacePermissions returns the computed value for this helper.
func hardenAshWorkspacePermissions() error {
	root, err := ashWorkspaceDir()
	if err != nil {
		return err
	}
	if err := osMkdirAll(root, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return err
	}

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
			return os.Chmod(path, 0o700)
		}
		if mode.IsRegular() {
			return os.Chmod(path, 0o600)
		}
		return nil
	})
}

// parseInstallArgs parses and validates input values.
func parseInstallArgs(args []string) (shellName string, dryRun bool, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dry-run":
			dryRun = true
		case "--shell":
			i++
			if i >= len(args) {
				return "", false, errors.New("--shell requires a value")
			}
			shellName = strings.TrimSpace(strings.ToLower(args[i]))
		default:
			return "", false, fmt.Errorf("unknown install argument: %s", args[i])
		}
	}
	return shellName, dryRun, nil
}

// detectShellName detects and returns the matching shell name.
func detectShellName(shellPath string) string {
	base := strings.TrimSpace(filepath.Base(shellPath))
	switch base {
	case "bash", "zsh":
		return base
	default:
		return ""
	}
}

// rcPathForShell returns the computed value for this helper.
func rcPathForShell(shellName string) (string, error) {
	home, err := osUserHomeDir()
	if err != nil {
		return "", err
	}

	switch shellName {
	case "bash":
		return filepath.Join(home, ".bashrc"), nil
	case "zsh":
		return filepath.Join(home, ".zshrc"), nil
	default:
		return "", fmt.Errorf("unsupported shell %q (supported: bash, zsh)", shellName)
	}
}

// readFileIfExists reads data from the filesystem.
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

// appendInstallBlock appends content and returns the updated result.
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

// extractManagedInstallBlock extracts the managed block from the provided content.
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

// replaceManagedInstallBlock replaces content and returns the updated result.
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

// installRecommendation returns the computed value for this helper.
func installRecommendation() (string, error) {
	shellName := detectShellName(os.Getenv("SHELL"))
	if shellName == "" {
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

// bashInstalledViaProfileSourcing returns the computed value for this helper.
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

// installSourceBlockForShell returns the computed value for this helper.
func installSourceBlockForShell(shellName string) string {
	scriptName := ""
	switch shellName {
	case "bash":
		scriptName = ".ash_bashrc"
	case "zsh":
		scriptName = ".ash_zshrc"
	default:
		return ""
	}

	return strings.TrimSpace(`
` + installStartMarker + `
[ -f "$HOME/.ash/.ash_env" ] && . "$HOME/.ash/.ash_env"
[ -f "$HOME/.ash/` + scriptName + `" ] && . "$HOME/.ash/` + scriptName + `"
` + installEndMarker)
}

// installShellWrapperPath returns the computed value for this helper.
func installShellWrapperPath(shellName string) (string, error) {
	root, err := ashWorkspaceDir()
	if err != nil {
		return "", err
	}

	fileName := ""
	switch shellName {
	case "bash":
		fileName = ".ash_bashrc"
	case "zsh":
		fileName = ".ash_zshrc"
	default:
		return "", fmt.Errorf("unsupported shell %q", shellName)
	}
	return filepath.Join(root, fileName), nil
}

// ensureInstallShellWrapper ensures required state exists and is up to date.
func ensureInstallShellWrapper(shellName string, dryRun bool, stdout io.Writer) error {
	content := installBlockForShell(shellName)
	if content == "" {
		return fmt.Errorf("unsupported shell %q", shellName)
	}

	path, err := installShellWrapperPath(shellName)
	if err != nil {
		return err
	}
	if dryRun {
		fmt.Fprintf(stdout, "[dry-run] would write shell wrapper file %s\n", path)
		return nil
	}

	if err := osMkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return osWriteFile(path, []byte(content+"\n"), 0o600)
}

// ensureBashProfileSourcing ensures required state exists and is up to date.
func ensureBashProfileSourcing(shellName string, dryRun bool, stdout io.Writer) error {
	if shellName != "bash" {
		return nil
	}
	home, err := osUserHomeDir()
	if err != nil {
		return err
	}
	profilePath := filepath.Join(home, ".bash_profile")
	line := `[ -f "$HOME/.ash/.ash_bashrc" ] && . "$HOME/.ash/.ash_bashrc"`

	existing, err := readFileIfExists(profilePath)
	if err != nil {
		return err
	}
	if strings.Contains(existing, line) {
		return nil
	}

	if dryRun {
		fmt.Fprintf(stdout, "[dry-run] would append ash source line to %s\n", profilePath)
		return nil
	}

	updated := existing
	if updated != "" && !strings.HasSuffix(updated, "\n") {
		updated += "\n"
	}
	updated += line + "\n"
	return osWriteFile(profilePath, []byte(updated), 0o600)
}

// installBlockForShell returns the computed value for this helper.
func installBlockForShell(shellName string) string {
	switch shellName {
	case "bash":
		return strings.TrimSpace(`
` + installStartMarker + `
case "$-" in
	*i*) ;;
	*) return ;;
esac

command_not_found_handle() {
  ash "$@"
  return $?
}

_ash_should_route() {
  local cmd="$1"
  shift
  local args=("$@")
  local argc=${#args[@]}
	local cmd_lower
	cmd_lower="$(printf '%s' "$cmd" | tr '[:upper:]' '[:lower:]')"
	local natural_wrapper=0
	case "$cmd_lower" in
		what|which|who|where|at) natural_wrapper=1 ;;
	esac

  [[ $argc -eq 0 ]] && return 1

  local a
  for a in "${args[@]}"; do
    [[ "$a" == -* ]] && return 1
  done

	local has_path_like=0
	for a in "${args[@]}"; do
		if [[ "$a" == */* || "$a" == ./* || "$a" == ../* ]]; then
			has_path_like=1
			break
		fi
	done
	if [[ $has_path_like -eq 1 && ( $natural_wrapper -eq 0 || $argc -eq 1 ) ]]; then
		return 1
	fi

	if [[ "$cmd_lower" == "at" ]]; then
		local first_at
		first_at="$(printf '%s' "${args[0]}" | tr '[:upper:]' '[:lower:]')"
		first_at="${first_at%%[?!.,:;]}"
		if [[ "$first_at" =~ [0-9:] ]]; then
			return 1
		fi
		case "$first_at" in
			now|today|tomorrow|teatime|midnight|noon)
				return 1
				;;
			am|pm)
				return 1
				;;
		esac
	fi

  if [[ "$cmd" == "Time" || "$cmd" == "test" || "$cmd" == "Test" || "$cmd" == "type" || "$cmd" == "Type" ]]; then
    if [[ $argc -eq 1 && "${args[0]}" =~ ^[A-Za-z0-9_.-]+$ ]]; then
      return 1
    fi
  fi

  local full="$cmd"
  for a in "${args[@]}"; do
    full+=" $a"
  done

  [[ "$full" == *\? && $argc -ge 2 ]] && return 0

  local first
  first="$(printf '%s' "${args[0]}" | tr '[:upper:]' '[:lower:]')"
  case "$first" in
    is|are|am|do|does|did|can|could|should|would|will|why|how|when|where|who)
			if [[ $argc -ge 2 ]]; then
				if [[ $has_path_like -eq 0 || ( $natural_wrapper -eq 1 && $argc -ge 3 ) ]]; then
					return 0
				fi
			fi
      ;;
  esac

	case "$cmd_lower" in
		what|which|who|where)
			if [[ $argc -ge 3 ]]; then
				local limit=4
				(( argc < limit )) && limit=$argc
				local i token raw
				for (( i=1; i<limit; i++ )); do
					raw="${args[$i]}"
					token="$(printf '%s' "$raw" | tr '[:upper:]' '[:lower:]')"
					token="${token%%[?!.,:;]}"
					case "$token" in
						is|are|am|do|does|did|can|could|should|would|will|why|how|when|where|who|if)
							return 0
							;;
					esac
				done
			fi
			;;
		say)
			if [[ $argc -ge 2 ]]; then
				local first_token
				first_token="$(printf '%s' "${args[0]}" | tr '[:upper:]' '[:lower:]')"
				first_token="${first_token%%[?!.,:;]}"
				case "$first_token" in
					out|something|a|an|the|please|why|how|when|where|who|what|can|could|should|would)
						return 0
						;;
				esac
			fi
			;;
		at)
			if [[ $argc -ge 2 ]]; then
				local first_token
				first_token="$(printf '%s' "${args[0]}" | tr '[:upper:]' '[:lower:]')"
				first_token="${first_token%%[?!.,:;]}"
				case "$first_token" in
					remind|tell|ask|message|note|please|what|when|how|why|who|where)
						return 0
						;;
				esac
			fi
			;;
	esac

  return 1
}

_ash_route_or_delegate() {
  local cmd="$1"
  shift
  if _ash_should_route "$cmd" "$@"; then
    ash "$cmd" "$@"
    return $?
  fi
  command "$cmd" "$@"
}

_ash_route_or_delegate_builtin() {
  local builtin_name="$1"
  shift
  if _ash_should_route "$builtin_name" "$@"; then
    ash "$builtin_name" "$@"
    return $?
  fi
  builtin "$builtin_name" "$@"
}

what()  { _ash_route_or_delegate what  "$@"; }
What()  { _ash_route_or_delegate What  "$@"; }
which() { _ash_route_or_delegate which "$@"; }
Which() { _ash_route_or_delegate Which "$@"; }
who()   { _ash_route_or_delegate who   "$@"; }
Who()   { _ash_route_or_delegate Who   "$@"; }
say()   { _ash_route_or_delegate say   "$@"; }
Say()   { _ash_route_or_delegate Say   "$@"; }
at()    { _ash_route_or_delegate at    "$@"; }
At()    { _ash_route_or_delegate At    "$@"; }

test()  { _ash_route_or_delegate_builtin test "$@"; }
Test()  { _ash_route_or_delegate_builtin test "$@"; }
type()  { _ash_route_or_delegate_builtin type "$@"; }
Type()  { _ash_route_or_delegate_builtin type "$@"; }
Time()  { _ash_route_or_delegate Time "$@"; }
` + installEndMarker)
	case "zsh":
		return strings.TrimSpace(`
` + installStartMarker + `
command_not_found_handler() {
  ash "$@"
  return $?
}

_ash_should_route() {
  local cmd="$1"
  shift
  local -a args
  args=("$@")
  local argc=${#args}
	local cmd_lower
	cmd_lower="$(printf '%s' "$cmd" | tr '[:upper:]' '[:lower:]')"
	local natural_wrapper=0
	case "$cmd_lower" in
		what|which|who|where|at) natural_wrapper=1 ;;
	esac

  [[ $argc -eq 0 ]] && return 1

  local a
  for a in "${args[@]}"; do
    [[ "$a" == -* ]] && return 1
  done

	local has_path_like=0
	for a in "${args[@]}"; do
		if [[ "$a" == */* || "$a" == ./* || "$a" == ../* ]]; then
			has_path_like=1
			break
		fi
	done
	if [[ $has_path_like -eq 1 && ( $natural_wrapper -eq 0 || $argc -eq 1 ) ]]; then
		return 1
	fi

	if [[ "$cmd_lower" == "at" ]]; then
		local first_at
		first_at="$(printf '%s' "${args[1]}" | tr '[:upper:]' '[:lower:]')"
		first_at="${first_at%%[?!.,:;]}"
		if [[ "$first_at" =~ [0-9:] ]]; then
			return 1
		fi
		case "$first_at" in
			now|today|tomorrow|teatime|midnight|noon)
				return 1
				;;
			am|pm)
				return 1
				;;
		esac
	fi

  if [[ "$cmd" == "Time" || "$cmd" == "test" || "$cmd" == "Test" || "$cmd" == "type" || "$cmd" == "Type" ]]; then
    if [[ $argc -eq 1 && "${args[1]}" =~ '^[A-Za-z0-9_.-]+$' ]]; then
      return 1
    fi
  fi

  local full="$cmd"
  for a in "${args[@]}"; do
    full+=" $a"
  done

  [[ "$full" == *\? && $argc -ge 2 ]] && return 0

  local first
  first="$(printf '%s' "${args[1]}" | tr '[:upper:]' '[:lower:]')"
  case "$first" in
    is|are|am|do|does|did|can|could|should|would|will|why|how|when|where|who)
			if [[ $argc -ge 2 ]]; then
				if [[ $has_path_like -eq 0 || ( $natural_wrapper -eq 1 && $argc -ge 3 ) ]]; then
					return 0
				fi
			fi
      ;;
  esac

	case "$cmd_lower" in
		what|which|who|where)
			if [[ $argc -ge 3 ]]; then
				local limit=4
				(( argc < limit )) && limit=$argc
				local i token raw
				for (( i=2; i<=limit; i++ )); do
					raw="${args[$i]}"
					token="$(printf '%s' "$raw" | tr '[:upper:]' '[:lower:]')"
					token="${token%%[?!.,:;]}"
					case "$token" in
						is|are|am|do|does|did|can|could|should|would|will|why|how|when|where|who|if)
							return 0
							;;
					esac
				done
			fi
			;;
		at)
			if [[ $argc -ge 2 ]]; then
				local first_token
				first_token="$(printf '%s' "${args[1]}" | tr '[:upper:]' '[:lower:]')"
				first_token="${first_token%%[?!.,:;]}"
				case "$first_token" in
					remind|tell|ask|message|note|please|what|when|how|why|who|where)
						return 0
						;;
				esac
			fi
			;;
	esac

  return 1
}

_ash_route_or_delegate() {
  local cmd="$1"
  shift
  if _ash_should_route "$cmd" "$@"; then
    ash "$cmd" "$@"
    return $?
  fi
  command "$cmd" "$@"
}

_ash_route_or_delegate_builtin() {
  local builtin_name="$1"
  shift
  if _ash_should_route "$builtin_name" "$@"; then
    ash "$builtin_name" "$@"
    return $?
  fi
  builtin "$builtin_name" "$@"
}

what()  { _ash_route_or_delegate what  "$@"; }
What()  { _ash_route_or_delegate What  "$@"; }
which() { _ash_route_or_delegate which "$@"; }
Which() { _ash_route_or_delegate Which "$@"; }
who()   { _ash_route_or_delegate who   "$@"; }
Who()   { _ash_route_or_delegate Who   "$@"; }
where() { _ash_route_or_delegate_builtin where "$@"; }
Where() { _ash_route_or_delegate_builtin where "$@"; }
at()    { _ash_route_or_delegate at    "$@"; }
At()    { _ash_route_or_delegate At    "$@"; }

test()  { _ash_route_or_delegate_builtin test "$@"; }
Test()  { _ash_route_or_delegate_builtin test "$@"; }
type()  { _ash_route_or_delegate_builtin type "$@"; }
Type()  { _ash_route_or_delegate_builtin type "$@"; }
Time()  { _ash_route_or_delegate Time "$@"; }
` + installEndMarker)
	default:
		return ""
	}
}
