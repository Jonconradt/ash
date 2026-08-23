package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
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
)

type aiConfig struct {
	BaseURL          string
	Model            string
	HistoryKey       string
	Authorization    string
	AuthToken        string
	Provider         aiProvider
	UseNativeCaching bool
}

// parseAIConfigFromEnv parses and validates input values.
func parseAIConfigFromEnv() (aiConfig, error) {
	if legacy := strings.TrimSpace(os.Getenv("AI")); legacy != "" {
		return aiConfig{}, errors.New("AI is no longer supported; use AI_ENDPOINT and AI_MODEL")
	}

	rawEndpoint := strings.TrimSpace(os.Getenv(aiEnvEndpoint))
	if rawEndpoint == "" {
		return aiConfig{}, fmt.Errorf("%s is required", aiEnvEndpoint)
	}

	model := strings.TrimSpace(os.Getenv(aiEnvModel))
	if model == "" {
		return aiConfig{}, fmt.Errorf("%s is required", aiEnvModel)
	}

	baseURL, host, scheme, err := parseAIEndpoint(rawEndpoint)
	if err != nil {
		return aiConfig{}, err
	}

	provider, err := resolveAIProvider(strings.TrimSpace(os.Getenv(aiEnvProvider)), baseURL, host)
	if err != nil {
		return aiConfig{}, err
	}

	useCaching, err := parseAICacheEnabled(strings.TrimSpace(os.Getenv(aiEnvCache)))
	if err != nil {
		return aiConfig{}, err
	}

	authToken := strings.TrimSpace(os.Getenv(aiEnvAuthToken))

	if isCloudAIHost(host) {
		if scheme != "https" {
			return aiConfig{}, errors.New("AI_ENDPOINT must use https for cloud endpoints")
		}
		if authToken == "" {
			return aiConfig{}, errors.New("cloud endpoints require AI_AUTH_TOKEN")
		}
	}

	cfg := aiConfig{
		BaseURL:          baseURL,
		Model:            model,
		HistoryKey:       fmt.Sprintf("%s/%s", baseURL, model),
		Provider:         provider,
		UseNativeCaching: useCaching,
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
	default:
		return "", fmt.Errorf("%s must be one of: ollama, openai, google, gemini, anthropic", aiEnvProvider)
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

	return providerOllama
}

// parseAIEndpoint parses and validates input values.
func parseAIEndpoint(value string) (baseURL string, host string, scheme string, err error) {
	u, err := url.Parse(value)
	if err != nil {
		return "", "", "", err
	}

	scheme = strings.ToLower(strings.TrimSpace(u.Scheme))
	if scheme != "http" && scheme != "https" {
		return "", "", "", errors.New("AI_ENDPOINT scheme must be http or https")
	}
	host = strings.TrimSpace(u.Hostname())
	if host == "" {
		return "", "", "", errors.New("AI_ENDPOINT host is required")
	}
	if strings.TrimSpace(u.RawQuery) != "" || strings.TrimSpace(u.Fragment) != "" {
		return "", "", "", errors.New("AI_ENDPOINT must not include query or fragment")
	}

	cleanPath := strings.TrimRight(strings.TrimSpace(u.Path), "/")
	baseURL = fmt.Sprintf("%s://%s%s", scheme, u.Host, cleanPath)
	return baseURL, host, scheme, nil
}

// isCloudAIHost reports whether the condition is true.
func isCloudAIHost(host string) bool {
	h := strings.TrimSpace(strings.ToLower(host))
	if h == "localhost" {
		return false
	}
	ip := net.ParseIP(h)
	if ip == nil {
		return true
	}
	return !ip.IsLoopback()
}

// readSystemPrompt reads data from the filesystem.
func readSystemPrompt() (string, error) {
	if root, err := ashWorkspaceDir(); err == nil {
		canonicalPath := filepath.Join(root, systemFileName)
		if content, err := osReadFile(canonicalPath); err == nil {
			return expandSystemPrompt(string(content)), nil
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
		return expandSystemPrompt(string(content)), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	home, err := osUserHomeDir()
	if err != nil {
		return "", err
	}

	homePath := filepath.Join(home, systemFileName)
	if content, err := osReadFile(homePath); err == nil {
		return expandSystemPrompt(string(content)), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	return "", nil
}

// loadAllowlistedCommands loads data from storage.
func loadAllowlistedCommands() (map[string]struct{}, error) {
	if raw := strings.TrimSpace(os.Getenv("ASH_TOOL_ALLOWLIST")); raw != "" {
		return parseAllowlistCSV(raw), nil
	}

	if root, err := ashWorkspaceDir(); err == nil {
		canonicalPath := filepath.Join(root, toolsFileName)
		if content, err := osReadFile(canonicalPath); err == nil {
			return parseAllowlistFile(string(content)), nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}

	cwd, err := osGetwd()
	if err != nil {
		return nil, err
	}

	cwdPath := filepath.Join(cwd, toolsFileName)
	if content, err := osReadFile(cwdPath); err == nil {
		return parseAllowlistFile(string(content)), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	home, err := osUserHomeDir()
	if err != nil {
		return nil, err
	}

	homePath := filepath.Join(home, toolsFileName)
	if content, err := osReadFile(homePath); err == nil {
		return parseAllowlistFile(string(content)), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	return map[string]struct{}{}, nil
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

// normalizeToolName trims whitespace and rejects slash-delimited values so tool names remain canonical.
func normalizeToolName(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.Contains(trimmed, "/") {
		return ""
	}
	return trimmed
}

// expandSystemPrompt expands environment variables and the UNAME placeholder in the supplied prompt template.
func expandSystemPrompt(prompt string) string {
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
		return os.Getenv(key)
	})
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
