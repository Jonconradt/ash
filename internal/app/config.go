package app

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// #nosec G101 -- these are environment variable names, not secrets.
	aiEnvEndpoint = "AI_ENDPOINT"
	// #nosec G101 -- these are environment variable names, not secrets.
	aiEnvModel = "AI_MODEL"
	// #nosec G101 -- these are environment variable names, not secrets.
	aiEnvAuthType = "AI_AUTH_TYPE"
	// #nosec G101 -- these are environment variable names, not secrets.
	aiEnvAuthToken = "AI_AUTH_TOKEN"
	// #nosec G101 -- these are environment variable names, not secrets.
	aiEnvProvider = "AI_PROVIDER"
	// #nosec G101 -- these are environment variable names, not secrets.
	aiEnvCache = "AI_CACHE"
	// ashEnvAlwaysOpenAIAPI, when truthy, routes the Ollama provider through the
	// OpenAI-compatible adapter instead of the hand-rolled Ollama one, for manual
	// testing of whether the Ollama-specific implementation is still required.
	ashEnvAlwaysOpenAIAPI = "ASH_ALWAYS_OPENAI_API"
)

type aiConfig struct {
	BaseURL          string
	Model            string
	HistoryKey       string
	Authorization    string
	AuthToken        string
	Provider         aiProvider
	UseNativeCaching bool
	OllamaOpenAIAPI  bool
}

// parseAIConfigFromEnv parses and validates input values.
func parseAIConfigFromEnv() (aiConfig, error) {
	if legacy := strings.TrimSpace(os.Getenv("AI")); legacy != "" {
		return aiConfig{}, errors.New("AI is no longer supported; use AI_ENDPOINT and AI_MODEL")
	}

	rawEndpoint := strings.TrimSpace(os.Getenv(aiEnvEndpoint))
	model := strings.TrimSpace(os.Getenv(aiEnvModel))
	authToken := strings.TrimSpace(os.Getenv(aiEnvAuthToken))
	if rawEndpoint == "" || model == "" {
		return aiConfig{}, missingAIEnvSetupError()
	}

	baseURL, host, scheme, err := parseAIEndpoint(rawEndpoint)
	if err != nil {
		return aiConfig{}, err
	}

	provider, err := resolveAIProvider(strings.TrimSpace(os.Getenv(aiEnvProvider)), baseURL, host)
	if err != nil {
		return aiConfig{}, err
	}
	ollamaOpenAIAPI := provider == providerOllama && alwaysUseOpenAIAPIForOllama()
	if ollamaOpenAIAPI {
		provider = providerOpenAI
	}

	useCaching, err := parseAICacheEnabled(strings.TrimSpace(os.Getenv(aiEnvCache)))
	if err != nil {
		return aiConfig{}, err
	}

	if isCloudAIHost(host) {
		if scheme != "https" {
			return aiConfig{}, errors.New("AI_ENDPOINT must use https for cloud endpoints")
		}
		if authToken == "" {
			return aiConfig{}, missingAIEnvSetupError()
		}
	}

	cfg := aiConfig{
		BaseURL:          baseURL,
		Model:            model,
		HistoryKey:       fmt.Sprintf("%s/%s", baseURL, model),
		Provider:         provider,
		UseNativeCaching: useCaching,
		OllamaOpenAIAPI:  ollamaOpenAIAPI,
	}
	if authToken != "" {
		cfg.Authorization = "Bearer " + authToken
		cfg.AuthToken = authToken
	}

	return cfg, nil
}

func parseAICacheEnabled(raw string) (bool, error) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return true, nil
	}
	switch trimmed {
	case "1", "true", "yes", "on", "enabled":
		return true, nil
	case "0", "false", "no", "off", "disabled":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be a boolean-like value (true/false)", aiEnvCache)
	}
}

// alwaysUseOpenAIAPIForOllama reports whether ASH_ALWAYS_OPENAI_API is truthy.
// Defaults to true (on) when unset or unrecognized; set it to a falsy value
// to opt back into the hand-rolled ollama adapter.
func alwaysUseOpenAIAPIForOllama() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(ashEnvAlwaysOpenAIAPI))) {
	case "0", "false", "no", "off", "disabled":
		return false
	default:
		return true
	}
}

// streamingEnabled reports whether ASH_STREAM is truthy. Defaults to true (on)
// when unset or unrecognized; set it to a falsy value to disable streaming.
func streamingEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ASH_STREAM"))) {
	case "0", "false", "no", "off", "disabled":
		return false
	default:
		return true
	}
}

func resolveAIProvider(override string, baseURL string, host string) (aiProvider, error) {
	if strings.TrimSpace(override) == "" {
		return detectAIProvider(baseURL, host), nil
	}

	switch strings.ToLower(strings.TrimSpace(override)) {
	case string(providerOllama):
		return providerOllama, nil
	case string(providerOpenAI):
		return providerOpenAI, nil
	case "gemini", string(providerGoogle):
		return providerGoogle, nil
	case string(providerAnthropic):
		return providerAnthropic, nil
	case string(providerCohere):
		return providerCohere, nil
	case string(providerBedrock):
		return providerBedrock, nil
	default:
		return "", fmt.Errorf("%s must be one of: ollama, openai, google, gemini, anthropic, cohere, bedrock", aiEnvProvider)
	}
}

func detectAIProvider(baseURL string, host string) aiProvider {
	h := strings.ToLower(strings.TrimSpace(host))
	url := strings.ToLower(strings.TrimSpace(baseURL))

	if strings.Contains(h, "anthropic.com") {
		return providerAnthropic
	}
	if strings.Contains(h, "openai.com") {
		return providerOpenAI
	}
	if strings.Contains(h, "googleapis.com") && strings.Contains(url, "/openai") {
		return providerGoogle
	}
	if strings.Contains(h, "googleapis.com") && strings.Contains(h, "generativelanguage") {
		return providerGoogle
	}
	if strings.Contains(h, "ollama.com") {
		return providerOllama
	}
	if strings.Contains(h, "api.cohere.com") {
		return providerCohere
	}
	if strings.Contains(h, "bedrock-runtime.") && strings.Contains(h, "amazonaws.com") {
		return providerBedrock
	}

	// Unknown cloud hosts are assumed to be OpenAI-compatible (e.g. Mistral, Groq, DeepSeek);
	// only unknown local/loopback hosts default to the native Ollama protocol.
	if isCloudAIHost(h) {
		return providerOpenAI
	}
	return providerOllama
}

// parseAIEndpoint parses and validates input values. If value omits a
// scheme (e.g. "localhost:11434"), the scheme defaults to http for local/LAN
// hosts and https for everything else, so local ollama servers need not
// spell out "http://".
func parseAIEndpoint(value string) (baseURL string, host string, scheme string, err error) {
	raw := strings.TrimSpace(value)
	hasScheme := strings.Contains(raw, "://")
	parseValue := raw
	if !hasScheme {
		parseValue = "http://" + raw
	}

	u, err := url.Parse(parseValue)
	if err != nil {
		return "", "", "", err
	}

	scheme = strings.ToLower(strings.TrimSpace(u.Scheme))
	if hasScheme && scheme != "http" && scheme != "https" {
		return "", "", "", errors.New("AI_ENDPOINT scheme must be http or https")
	}
	host = strings.TrimSpace(u.Hostname())
	if host == "" {
		return "", "", "", errors.New("AI_ENDPOINT host is required")
	}
	if strings.TrimSpace(u.RawQuery) != "" || strings.TrimSpace(u.Fragment) != "" {
		return "", "", "", errors.New("AI_ENDPOINT must not include query or fragment")
	}

	if !hasScheme {
		if isCloudAIHost(host) {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}

	cleanPath := strings.TrimRight(strings.TrimSpace(u.Path), "/")
	baseURL = fmt.Sprintf("%s://%s%s", scheme, u.Host, cleanPath)
	return baseURL, host, scheme, nil
}

// isCloudAIHost reports whether the condition is true. Private/LAN addresses
// (RFC1918 IPv4, IPv6 ULA), .local mDNS hostnames, and bare single-label
// hostnames (e.g. "brain", resolved via mDNS/NetBIOS/hosts file rather than
// public DNS) are treated as non-cloud unless ASH_STRICT is enabled, in which
// case only localhost/loopback are exempt.
func isCloudAIHost(host string) bool {
	h := strings.TrimSpace(strings.ToLower(host))
	if h == "localhost" {
		return false
	}
	ip := net.ParseIP(h)
	if ip == nil {
		if !strictSecurityModeEnabled() && (strings.HasSuffix(h, ".local") || !strings.Contains(h, ".")) {
			return false
		}
		return true
	}
	if ip.IsLoopback() {
		return false
	}
	if !strictSecurityModeEnabled() && ip.IsPrivate() {
		return false
	}
	return true
}

// readSystemPrompt reads data from the filesystem.
func readSystemPrompt() (string, error) {
	allowlist, err := loadAllowlistedCommands()
	if err != nil {
		return "", err
	}
	return readSystemPromptWithAllowlist(allowlist)
}

// readSystemPromptWithAllowlist renders the selected prompt against one resolved tool policy.
func readSystemPromptWithAllowlist(allowlist map[string]struct{}) (string, error) {
	if root, err := ashWorkspaceDir(); err == nil {
		canonicalPath := filepath.Join(root, systemFileName)
		if content, err := osReadFile(canonicalPath); err == nil {
			return expandSystemPromptWithAllowlist(string(content), allowlist), nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}

	cwd, err := osGetwd()
	if err != nil {
		return "", err
	}

	cwdPath := filepath.Join(cwd, systemFileName)
	if content, err := osReadFile(cwdPath); err == nil {
		return expandSystemPromptWithAllowlist(string(content), allowlist), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	home, err := osUserHomeDir()
	if err != nil {
		return "", err
	}

	homePath := filepath.Join(home, systemFileName)
	if content, err := osReadFile(homePath); err == nil {
		return expandSystemPromptWithAllowlist(string(content), allowlist), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	return "", nil
}

// loadAllowlistedCommands loads data from storage.
func loadAllowlistedCommands() (map[string]struct{}, error) {
	allowed, err := loadAllowlistedCommandsFromSource()
	if err != nil {
		return nil, err
	}
	denied, err := loadDenylistedCommands()
	if err != nil {
		return nil, err
	}
	for item := range denied {
		delete(allowed, item)
	}
	// python3 has its own scratch-scoped tool (run_python3); dropping it here
	// stops the model from bypassing that tool via run_unix_command/run_unix_pipeline.
	if pythonExecutionAvailable() {
		delete(allowed, "python3")
	}
	return allowed, nil
}

// loadDenylistedCommands loads denied commands and tools from environment or files.
func loadDenylistedCommands() (map[string]struct{}, error) {
	return loadDenylistedCommandsFromSource()
}

// loadDenylistedCommandsFromSource resolves the raw denylist from env, cwd, or home.
func loadDenylistedCommandsFromSource() (map[string]struct{}, error) {
	if raw := strings.TrimSpace(os.Getenv("ASH_DENY")); raw != "" {
		return parseAllowlistCSV(raw), nil
	}

	if root, err := ashWorkspaceDir(); err == nil {
		canonicalPath := filepath.Join(root, denyFileName)
		if content, err := osReadFile(canonicalPath); err == nil {
			return parseDenylistFile(string(content)), nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}

	cwd, err := osGetwd()
	if err != nil {
		return nil, err
	}

	cwdPath := filepath.Join(cwd, denyFileName)
	if content, err := osReadFile(cwdPath); err == nil {
		return parseDenylistFile(string(content)), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	home, err := osUserHomeDir()
	if err != nil {
		return nil, err
	}

	homePath := filepath.Join(home, denyFileName)
	if content, err := osReadFile(homePath); err == nil {
		return parseDenylistFile(string(content)), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	return map[string]struct{}{}, nil
}

// loadAllowlistedCommandsFromSource resolves the raw allowlist from env, cwd, or home, before any post-processing.
func loadAllowlistedCommandsFromSource() (map[string]struct{}, error) {
	if raw := strings.TrimSpace(os.Getenv("ASH_ALLOW")); raw != "" {
		return parseAllowlistCSV(raw), nil
	}

	if root, err := ashWorkspaceDir(); err == nil {
		canonicalPath := filepath.Join(root, allowFileName)
		if content, err := osReadFile(canonicalPath); err == nil {
			return parseAllowlistFileWithTokens(string(content))
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}

	cwd, err := osGetwd()
	if err != nil {
		return nil, err
	}

	cwdPath := filepath.Join(cwd, allowFileName)
	if content, err := osReadFile(cwdPath); err == nil {
		return parseAllowlistFileWithTokens(string(content))
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	home, err := osUserHomeDir()
	if err != nil {
		return nil, err
	}

	homePath := filepath.Join(home, allowFileName)
	if content, err := osReadFile(homePath); err == nil {
		return parseAllowlistFileWithTokens(string(content))
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	return map[string]struct{}{}, nil
}

// parseDenylistFile parses and validates denylist entries.
func parseDenylistFile(raw string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		for _, token := range strings.Split(trimmed, ",") {
			name := normalizeToolName(token)
			if name == "" {
				continue
			}
			set[name] = struct{}{}
		}
	}
	return set
}

// parseAllowlistCSV parses and validates input values.
func parseAllowlistCSV(raw string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, part := range strings.Split(raw, ",") {
		name := normalizeToolName(part)
		if name == "" {
			continue
		}
		set[name] = struct{}{}
	}
	return set
}

// parseAllowlistFile parses and validates input values.
func parseAllowlistFile(raw string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.Contains(trimmed, toolsDirListToken) || strings.Contains(trimmed, pluginsDirListToken) || strings.HasPrefix(trimmed, "#") {
			continue
		}
		for _, token := range strings.Split(trimmed, ",") {
			name := normalizeToolName(token)
			if name == "" {
				continue
			}
			set[name] = struct{}{}
		}
	}
	return set
}

// parseAllowlistFileWithTokens expands internal Ash tokens only in non-strict files.
func parseAllowlistFileWithTokens(raw string) (map[string]struct{}, error) {
	set := parseAllowlistFile(raw)
	if strictSecurityModeEnabled() {
		return set, nil
	}
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == toolsDirListToken {
			tools, err := eligibleToolScripts()
			if err != nil {
				return nil, err
			}
			for _, tool := range tools {
				set[tool] = struct{}{}
			}
		}
		if trimmed == pluginsDirListToken {
			plugins, err := eligiblePlugins()
			if err != nil {
				return nil, err
			}
			for _, plugin := range plugins {
				set[plugin] = struct{}{}
			}
		}
	}
	return set, nil
}

// normalizeToolName trims whitespace and rejects slash-delimited or dot-prefixed (hidden) values so tool names remain canonical.
func normalizeToolName(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed == toolsDirListToken || trimmed == pluginsDirListToken || strings.Contains(trimmed, "/") || strings.HasPrefix(trimmed, ".") {
		return ""
	}
	return trimmed
}

const (
	pythonAvailableInstructionsPath   = "ash_bootstrap/prompt-instructions/python-available.txt"
	pythonUnavailableInstructionsPath = "ash_bootstrap/prompt-instructions/python-unavailable.txt"
	// #nosec G101 -- this is an internal prompt placeholder, not a credential.
	toolsDirListToken = "$TOOLS_DIR_LIST"
	// #nosec G101 -- this is an internal prompt placeholder, not a credential.
	pluginsDirListToken = "$PLUGINS_DIR_LIST"
)

// expandSystemPrompt injects conditional guidance, strips source comments, and expands runtime values.
func expandSystemPrompt(prompt string) string {
	return expandSystemPromptWithAllowlist(prompt, nil)
}

// expandSystemPromptWithAllowlist expands Ash-owned prompt tokens before ordinary environment values.
func expandSystemPromptWithAllowlist(prompt string, allowlist map[string]struct{}) string {
	instructionsPath := pythonUnavailableInstructionsPath
	if pythonExecutionAvailable() {
		instructionsPath = pythonAvailableInstructionsPath
	}
	instructions, err := readEmbeddedBootstrapAsset(instructionsPath)
	if err != nil {
		panic("embedded " + instructionsPath + " is missing: " + err.Error())
	}
	prompt = strings.ReplaceAll(prompt, "$IF_PYTHON_AVAILABLE", string(instructions))
	prompt = stripSystemPromptComments(prompt)
	prompt = strings.ReplaceAll(prompt, toolsDirListToken, renderEligibleToolScripts(allowlist))
	prompt = strings.ReplaceAll(prompt, pluginsDirListToken, renderEligiblePlugins(allowlist))

	unameValue := ""
	if _, err := execLookPath("uname"); err == nil {
		if output, err := execCommandOutput("uname", "-a"); err == nil {
			unameValue = strings.TrimSpace(string(output))
		}
	}

	return os.Expand(prompt, func(key string) string {
		if key == "UNAME" && unameValue != "" {
			return unameValue
		}
		if key == "ASH_MAX_TOOL_ITERS" {
			return strconv.Itoa(maxToolIterations())
		}
		return os.Getenv(key)
	})
}

// eligibleToolScripts returns readable, executable regular files in Ash's managed tools directory.
func eligibleToolScripts() ([]string, error) {
	root, err := ashWorkspaceDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(root, "tools"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	tools := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || normalizeToolName(entry.Name()) != entry.Name() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		mode := info.Mode()
		if !mode.IsRegular() || mode.Perm()&0o444 == 0 || mode.Perm()&0o111 == 0 {
			continue
		}
		tools = append(tools, entry.Name())
	}
	sort.Strings(tools)
	return tools, nil
}

func renderEligibleToolScripts(allowlist map[string]struct{}) string {
	tools, err := eligibleToolScripts()
	if err != nil || len(tools) == 0 {
		return "No eligible managed tool scripts are currently available."
	}
	allowed := make([]string, 0, len(tools))
	for _, tool := range tools {
		if _, ok := allowlist[tool]; ok {
			allowed = append(allowed, tool)
		}
	}
	if len(allowed) == 0 {
		return "No eligible managed tool scripts are currently allowed."
	}
	return strings.Join(allowed, ", ")
}

// eligiblePlugins returns readable, executable regular files in Ash's managed plugins directory.
func eligiblePlugins() ([]string, error) {
	root, err := ashWorkspaceDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(root, "plugins"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	plugins := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || normalizeToolName(entry.Name()) != entry.Name() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		mode := info.Mode()
		if !mode.IsRegular() || mode.Perm()&0o444 == 0 || mode.Perm()&0o111 == 0 {
			continue
		}
		plugins = append(plugins, entry.Name())
	}
	sort.Strings(plugins)
	return plugins, nil
}

func renderEligiblePlugins(allowlist map[string]struct{}) string {
	plugins, err := eligiblePlugins()
	if err != nil || len(plugins) == 0 {
		return "No eligible native plugins are currently available."
	}
	allowed := make([]string, 0, len(plugins))
	for _, plugin := range plugins {
		if _, ok := allowlist[plugin]; ok {
			allowed = append(allowed, plugin)
		}
	}
	if len(allowed) == 0 {
		return "No eligible native plugins are currently allowed."
	}
	return strings.Join(allowed, ", ")
}

// managedNativePlugin resolves an installed plugin name to its executable path in ~/.ash/plugins/.
func managedNativePlugin(name string) (string, bool) {
	if normalizeToolName(name) != name {
		return "", false
	}
	root, err := ashWorkspaceDir()
	if err != nil {
		return "", false
	}
	pluginPath := filepath.Join(root, "plugins", name)
	info, err := os.Stat(pluginPath)
	if err != nil || info.IsDir() {
		return "", false
	}
	mode := info.Mode()
	if !mode.IsRegular() || mode.Perm()&0o444 == 0 || mode.Perm()&0o111 == 0 {
		return "", false
	}
	return pluginPath, true
}

func stripSystemPromptComments(prompt string) string {
	lines := strings.Split(prompt, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if !strings.HasPrefix(line, "#") {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// historyLimit returns the maximum number of messages retained for chat history, or the default when unset or invalid.
func historyLimit() int {
	if raw := strings.TrimSpace(os.Getenv("ASH_HISTORY_MAX")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			return parsed
		}
	}

	return defaultHistoryMax
}

// aiTimeout returns the configured AI request timeout, or the default when unset or invalid.
func aiTimeout() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("AI_TIMEOUT")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			return parsed
		}
	}

	return defaultAITimeout
}

// retryMaxAttempts returns the configured number of retry attempts, or the default when unset or invalid.
func retryMaxAttempts() int {
	if raw := strings.TrimSpace(os.Getenv("ASH_RETRY_MAX_ATTEMPTS")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			return parsed
		}
	}
	return defaultRetryMaxAttempts
}

// retryBaseDelay returns the configured delay before the first retry, or the default when unset or invalid.
func retryBaseDelay() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("ASH_RETRY_BASE_DELAY")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed >= 0 {
			return parsed
		}
	}
	return defaultRetryBaseDelay
}

// retryMaxDelay returns the configured maximum retry delay, or the default when unset or invalid.
func retryMaxDelay() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("ASH_RETRY_MAX_DELAY")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed >= 0 {
			return parsed
		}
	}
	return defaultRetryMaxDelay
}

// toolTimeout returns the configured timeout for tool commands, or the default when unset or invalid.
func toolTimeout() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("ASH_TOOL_TIMEOUT")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			return parsed
		}
	}
	return defaultToolTimeout
}

// toolOutputLimit returns the configured maximum output size for tool command results, or the default when unset or invalid.
func toolOutputLimit() int {
	if raw := strings.TrimSpace(os.Getenv("ASH_TOOL_OUTPUT_MAX")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			return parsed
		}
	}
	return defaultToolOutputMax
}

// maxToolIterations returns the configured maximum number of tool iterations, or the default when unset or invalid.
func maxToolIterations() int {
	if raw := strings.TrimSpace(os.Getenv("ASH_MAX_TOOL_ITERS")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
			return parsed
		}
	}
	return defaultMaxToolIters
}

// maxAgents returns the configured maximum number of child agents for this parent process.
func maxAgents() int {
	if raw := strings.TrimSpace(os.Getenv(maxAgentsEnvName)); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			return parsed
		}
	}
	return defaultMaxAgents
}

// keepRecentMessages returns the most recent messages up to max, preserving their existing order.
func keepRecentMessages(messages []message, max int) []message {
	if len(messages) <= max {
		return messages
	}

	return append([]message(nil), messages[len(messages)-max:]...)
}

// taskListMax returns the configured maximum number of execution tasks, or the default when unset or invalid.
func taskListMax() int {
	if raw := strings.TrimSpace(os.Getenv("ASH_TASK_MAX")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			return parsed
		}
	}
	return defaultTaskMax
}

// relevanceWindow returns the configured number of recent tool observations to include in task state messages, or the default when unset or invalid.
func relevanceWindow() int {
	if raw := strings.TrimSpace(os.Getenv("ASH_RELEVANCE_WINDOW")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			return parsed
		}
	}
	return defaultRelevanceWin
}

// maxTaskStallRounds returns the configured number of stalled rounds before execution stops, or the default when unset or invalid.
func maxTaskStallRounds() int {
	if raw := strings.TrimSpace(os.Getenv("ASH_TASK_STALL_ROUNDS")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			return parsed
		}
	}
	return defaultStallRounds
}

// toolRepeatLimit returns the configured number of consecutive identical tool calls allowed before the loop intervenes, or the default when unset or invalid.
func toolRepeatLimit() int {
	if raw := strings.TrimSpace(os.Getenv("ASH_TOOL_REPEAT_LIMIT")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			return parsed
		}
	}
	return defaultToolRepeatLimit
}
