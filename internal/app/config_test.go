// Copyright (c) 2026 Jonathan L. Conradt
// SPDX-License-Identifier: MIT

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAlwaysUseOpenAIAPIForOllama(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want bool
	}{
		{name: "unset defaults to on", env: "", want: true},
		{name: "unrecognized value defaults to on", env: "maybe", want: true},
		{name: "explicit 1", env: "1", want: true},
		{name: "explicit true", env: "true", want: true},
		{name: "explicit on", env: "on", want: true},
		{name: "explicit 0", env: "0", want: false},
		{name: "explicit false", env: "false", want: false},
		{name: "explicit off", env: "off", want: false},
		{name: "explicit disabled", env: "disabled", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(ashEnvAlwaysOpenAIAPI, tt.env)
			if got := alwaysUseOpenAIAPIForOllama(); got != tt.want {
				t.Fatalf("alwaysUseOpenAIAPIForOllama() with %s=%q = %v, want %v", ashEnvAlwaysOpenAIAPI, tt.env, got, tt.want)
			}
		})
	}
}

func TestStreamingEnabled(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want bool
	}{
		{name: "unset defaults to on", env: "", want: true},
		{name: "unrecognized value defaults to on", env: "maybe", want: true},
		{name: "explicit 1", env: "1", want: true},
		{name: "explicit true", env: "true", want: true},
		{name: "explicit on", env: "on", want: true},
		{name: "explicit 0", env: "0", want: false},
		{name: "explicit false", env: "false", want: false},
		{name: "explicit off", env: "off", want: false},
		{name: "explicit disabled", env: "disabled", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ASH_STREAM", tt.env)
			if got := streamingEnabled(); got != tt.want {
				t.Fatalf("streamingEnabled() with ASH_STREAM=%q = %v, want %v", tt.env, got, tt.want)
			}
		})
	}
}

func TestValidateRuntimeSettingsRejectsMalformedValues(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: aiEnvCache, value: "maybe"},
		{name: ashEnvAlwaysOpenAIAPI, value: "maybe"},
		{name: "ASH_STREAM", value: "maybe"},
		{name: "ASH_HISTORY_MAX", value: "0"},
		{name: "ASH_RETRY_MAX_ATTEMPTS", value: "many"},
		{name: "ASH_RETRY_BASE_DELAY", value: "-1s"},
		{name: "ASH_RETRY_MAX_DELAY", value: "later"},
		{name: "ASH_AI_TIMEOUT", value: "0s"},
		{name: "ASH_TOOL_TIMEOUT", value: "fast"},
		{name: "ASH_TOOL_OUTPUT_MAX", value: "unlimited"},
		{name: "ASH_MAX_TOOL_ITERS", value: "-1"},
		{name: maxAgentsEnvName, value: "0"},
		{name: "ASH_TASK_MAX", value: "0"},
		{name: "ASH_RELEVANCE_WINDOW", value: "0"},
		{name: "ASH_TASK_STALL_ROUNDS", value: "0"},
		{name: "ASH_TOOL_REPEAT_LIMIT", value: "0"},
	}
	settingNames := []string{
		aiEnvCache, ashEnvAlwaysOpenAIAPI, "ASH_STREAM",
		"ASH_HISTORY_MAX", "ASH_RETRY_MAX_ATTEMPTS",
		"ASH_RETRY_BASE_DELAY", "ASH_RETRY_MAX_DELAY", "ASH_AI_TIMEOUT",
		"ASH_TOOL_TIMEOUT", "ASH_TOOL_OUTPUT_MAX", "ASH_MAX_TOOL_ITERS",
		maxAgentsEnvName, "ASH_TASK_MAX", "ASH_RELEVANCE_WINDOW",
		"ASH_TASK_STALL_ROUNDS", "ASH_TOOL_REPEAT_LIMIT",
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, name := range settingNames {
				t.Setenv(name, "")
			}
			t.Setenv(tt.name, tt.value)
			err := validateRuntimeSettings()
			if err == nil || !strings.Contains(err.Error(), tt.name) {
				t.Fatalf("validateRuntimeSettings() error = %v, want error naming %s", err, tt.name)
			}
		})
	}
}

func TestValidateRuntimeSettingsAcceptsDocumentedValues(t *testing.T) {
	settings := map[string]string{
		aiEnvCache:               "disabled",
		ashEnvAlwaysOpenAIAPI:    "on",
		"ASH_STREAM":             "0",
		"ASH_HISTORY_MAX":        "1",
		"ASH_RETRY_MAX_ATTEMPTS": "1",
		"ASH_RETRY_BASE_DELAY":   "0s",
		"ASH_RETRY_MAX_DELAY":    "5m",
		"ASH_AI_TIMEOUT":         "1ms",
		"ASH_TOOL_TIMEOUT":       "1s",
		"ASH_TOOL_OUTPUT_MAX":    "1",
		"ASH_MAX_TOOL_ITERS":     "0",
		maxAgentsEnvName:         "1",
		"ASH_TASK_MAX":           "1",
		"ASH_RELEVANCE_WINDOW":   "1",
		"ASH_TASK_STALL_ROUNDS":  "1",
		"ASH_TOOL_REPEAT_LIMIT":  "1",
	}
	for name, value := range settings {
		t.Setenv(name, value)
	}
	if err := validateRuntimeSettings(); err != nil {
		t.Fatalf("validateRuntimeSettings() rejected documented settings: %v", err)
	}
}

func TestLoadAllowlistedCommandsIgnoresLegacyAshTools(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalCwd) })
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("ASH_ALLOW", "")
	t.Setenv("ASH_DENY", "")
	workspace := filepath.Join(home, ashWorkspaceDirName)
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, legacyToolsFileName), []byte("ls\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	allowed, err := loadAllowlistedCommands()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := allowed["ls"]; ok {
		t.Fatalf("legacy %s must not be imported", legacyToolsFileName)
	}
}

func TestParseAllowlistFileIgnoresRemoteMCPURLs(t *testing.T) {
	allowed := parseAllowlistFile("ls\nhttps://example.com/mcp,python3\nHTTP://localhost:8080/mcp,rm\nps\n")
	for _, command := range []string{"ls", "ps"} {
		if _, ok := allowed[command]; !ok {
			t.Errorf("allowlist is missing command %q", command)
		}
	}
	if len(allowed) != 2 {
		t.Fatalf("allowlist commands = %v, want only ls and ps", allowed)
	}
}

func TestManagedPluginRootsAndUserOverride(t *testing.T) {
	home := t.TempDir()
	dataRoot := filepath.Join(home, "data")
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", dataRoot)
	pluginDir := filepath.Join(dataRoot, "ash", "plugins")
	if err := os.MkdirAll(pluginDir, 0o700); err != nil {
		t.Fatal(err)
	}
	pluginPath := filepath.Join(pluginDir, "calculator")
	if err := os.WriteFile(pluginPath, []byte("user plugin"), 0o700); err != nil {
		t.Fatal(err)
	}

	path, ok := managedNativePlugin("calculator")
	if !ok || path != pluginPath {
		t.Fatalf("managedNativePlugin() = (%q, %v), want (%q, true)", path, ok, pluginPath)
	}
	plugins, err := eligiblePlugins()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, name := range plugins {
		found = found || name == "calculator"
	}
	if !found {
		t.Fatalf("eligiblePlugins() = %v, want calculator", plugins)
	}

	if got := systemPluginRoot("linux"); got != "/usr/lib/ash/plugins" {
		t.Fatalf("systemPluginRoot(linux) = %q", got)
	}
	if got := systemPluginRoot("darwin"); got != "/usr/local/lib/ash/plugins" {
		t.Fatalf("systemPluginRoot(darwin) = %q", got)
	}
	if got := systemPluginRoot("windows"); got != "" {
		t.Fatalf("systemPluginRoot(windows) = %q, want empty", got)
	}
}

// TestParseAIConfigFromEnvOllamaOpenAIAPIDefault covers the ASH_ALWAYS_OPENAI_API
// default (on) end-to-end through parseAIConfigFromEnv, separately from the raw
// provider-detection table above which explicitly disables it.
func TestParseAIConfigFromEnvOllamaOpenAIAPIDefault(t *testing.T) {
	t.Setenv("ASH_AI_ENDPOINT", "http://localhost:11434")
	t.Setenv("ASH_AI_MODEL", "llama3.1")
	t.Setenv("ASH_AI_PROVIDER", "")
	t.Setenv(ashEnvAlwaysOpenAIAPI, "")

	cfg, err := parseAIConfigFromEnv()
	if err != nil {
		t.Fatalf("parseAIConfigFromEnv returned unexpected error: %v", err)
	}
	if cfg.Provider != providerOpenAI {
		t.Fatalf("expected providerOpenAI by default for an ollama endpoint, got %q", cfg.Provider)
	}
	if !cfg.OllamaOpenAIAPI {
		t.Fatal("expected OllamaOpenAIAPI=true by default")
	}
}

func TestParseAIConfigFromEnv(t *testing.T) {
	// This table exercises raw provider detection (detectAIProvider), so it
	// explicitly disables the ASH_ALWAYS_OPENAI_API override rather than relying
	// on its default (which is now on) - see TestAlwaysUseOpenAIAPIForOllamaDefaultsToOn
	// for coverage of the default itself.
	t.Setenv(ashEnvAlwaysOpenAIAPI, "0")
	tests := []struct {
		name           string
		env            map[string]string
		wantBaseURL    string
		wantModel      string
		wantHistoryKey string
		wantAuth       string
		wantProvider   aiProvider
		wantUseCache   bool
		wantErr        string
	}{
		{
			name: "local endpoint without auth",
			env: map[string]string{
				"ASH_AI_ENDPOINT": "http://localhost:11434",
				"ASH_AI_MODEL":    "llama3.1",
			},
			wantBaseURL:    "http://localhost:11434",
			wantModel:      "llama3.1",
			wantHistoryKey: "http://localhost:11434/llama3.1",
			wantProvider:   providerOllama,
			wantUseCache:   true,
		},
		{
			name: "cloud endpoint with bearer auth",
			env: map[string]string{
				"ASH_AI_ENDPOINT":   "https://api.example.com/ollama",
				"ASH_AI_MODEL":      "mistral",
				"ASH_AI_AUTH_TYPE":  "bearer",
				"ASH_AI_AUTH_TOKEN": "abc123",
			},
			wantBaseURL:    "https://api.example.com/ollama",
			wantModel:      "mistral",
			wantHistoryKey: "https://api.example.com/ollama/mistral",
			wantAuth:       "Bearer abc123",
			wantProvider:   providerOpenAI,
			wantUseCache:   true,
		},
		{
			name: "auto-detect openai provider",
			env: map[string]string{
				"ASH_AI_ENDPOINT":   "https://api.openai.com/v1",
				"ASH_AI_MODEL":      "gpt-4.1-mini",
				"ASH_AI_AUTH_TYPE":  "bearer",
				"ASH_AI_AUTH_TOKEN": "openai-token",
			},
			wantBaseURL:    "https://api.openai.com/v1",
			wantModel:      "gpt-4.1-mini",
			wantHistoryKey: "https://api.openai.com/v1/gpt-4.1-mini",
			wantAuth:       "Bearer openai-token",
			wantProvider:   providerOpenAI,
			wantUseCache:   true,
		},
		{
			name: "optional provider override",
			env: map[string]string{
				"ASH_AI_ENDPOINT": "http://localhost:11434",
				"ASH_AI_MODEL":    "llama3.1",
				"ASH_AI_PROVIDER": "google",
			},
			wantBaseURL:    "http://localhost:11434",
			wantModel:      "llama3.1",
			wantHistoryKey: "http://localhost:11434/llama3.1",
			wantProvider:   providerGoogle,
			wantUseCache:   true,
		},
		{
			name: "cache disabled optional override",
			env: map[string]string{
				"ASH_AI_ENDPOINT": "http://localhost:11434",
				"ASH_AI_MODEL":    "llama3.1",
				"ASH_AI_CACHE":    "off",
			},
			wantBaseURL:    "http://localhost:11434",
			wantModel:      "llama3.1",
			wantHistoryKey: "http://localhost:11434/llama3.1",
			wantProvider:   providerOllama,
			wantUseCache:   false,
		},
		{
			name: "legacy AI env is ignored",
			env: map[string]string{
				"AI": "ollama://localhost/llama3.1",
			},
			wantErr: "export ASH_AI_ENDPOINT=",
		},
		{
			name: "missing endpoint",
			env: map[string]string{
				"ASH_AI_MODEL": "llama3.1",
				"SHELL":        "/bin/zsh",
			},
			wantErr: "export ASH_AI_ENDPOINT=",
		},
		{
			name: "missing model",
			env: map[string]string{
				"ASH_AI_ENDPOINT": "http://localhost:11434",
				"SHELL":           "/bin/bash",
			},
			wantErr: "export ASH_AI_ENDPOINT=",
		},
		{
			name: "token enables bearer auth",
			env: map[string]string{
				"ASH_AI_ENDPOINT":   "http://localhost:11434",
				"ASH_AI_MODEL":      "llama3.1",
				"ASH_AI_AUTH_TOKEN": "abc",
			},
			wantBaseURL:    "http://localhost:11434",
			wantModel:      "llama3.1",
			wantHistoryKey: "http://localhost:11434/llama3.1",
			wantAuth:       "Bearer abc",
			wantProvider:   providerOllama,
			wantUseCache:   true,
		},
		{
			name: "auth type is ignored",
			env: map[string]string{
				"ASH_AI_ENDPOINT":  "http://localhost:11434",
				"ASH_AI_MODEL":     "llama3.1",
				"ASH_AI_AUTH_TYPE": "basic",
			},
			wantBaseURL:    "http://localhost:11434",
			wantModel:      "llama3.1",
			wantHistoryKey: "http://localhost:11434/llama3.1",
			wantProvider:   providerOllama,
			wantUseCache:   true,
		},
		{
			name: "cloud endpoint requires https",
			env: map[string]string{
				"ASH_AI_ENDPOINT":   "http://api.example.com",
				"ASH_AI_MODEL":      "llama3.1",
				"ASH_AI_AUTH_TYPE":  "bearer",
				"ASH_AI_AUTH_TOKEN": "abc",
			},
			wantErr: "ASH_AI_ENDPOINT must use https for cloud endpoints",
		},
		{
			name: "cloud endpoint requires auth",
			env: map[string]string{
				"ASH_AI_ENDPOINT": "https://api.example.com",
				"ASH_AI_MODEL":    "llama3.1",
				"SHELL":           "/bin/zsh",
			},
			wantErr: "export ASH_AI_AUTH_TOKEN='your-token'",
		},
		{
			name: "bedrock uses aws credentials without bearer token",
			env: map[string]string{
				"ASH_AI_ENDPOINT": "https://bedrock-runtime.us-east-1.amazonaws.com",
				"ASH_AI_MODEL":    "anthropic.claude-3-sonnet",
			},
			wantBaseURL:    "https://bedrock-runtime.us-east-1.amazonaws.com",
			wantModel:      "anthropic.claude-3-sonnet",
			wantHistoryKey: "https://bedrock-runtime.us-east-1.amazonaws.com/anthropic.claude-3-sonnet",
			wantProvider:   providerBedrock,
			wantUseCache:   true,
		},
		{
			name: "invalid provider override",
			env: map[string]string{
				"ASH_AI_ENDPOINT": "http://localhost:11434",
				"ASH_AI_MODEL":    "llama3.1",
				"ASH_AI_PROVIDER": "unsupported-provider",
			},
			wantErr: "ASH_AI_PROVIDER must be one of",
		},
		{
			name: "invalid cache override",
			env: map[string]string{
				"ASH_AI_ENDPOINT": "http://localhost:11434",
				"ASH_AI_MODEL":    "llama3.1",
				"ASH_AI_CACHE":    "sometimes",
			},
			wantErr: "ASH_AI_CACHE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AI", "")
			t.Setenv("ASH_AI_ENDPOINT", "")
			t.Setenv("ASH_AI_MODEL", "")
			t.Setenv("ASH_AI_AUTH_TYPE", "")
			t.Setenv("ASH_AI_AUTH_TOKEN", "")
			t.Setenv("ASH_AI_PROVIDER", "")
			t.Setenv("ASH_AI_CACHE", "")
			t.Setenv(ashEnvAlwaysOpenAIAPI, "0")
			t.Setenv("SHELL", "")
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			cfg, err := parseAIConfigFromEnv()
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("parseAIConfigFromEnv returned unexpected error: %v", err)
			}
			if cfg.BaseURL != tt.wantBaseURL {
				t.Fatalf("baseURL mismatch: got %q want %q", cfg.BaseURL, tt.wantBaseURL)
			}
			if cfg.Model != tt.wantModel {
				t.Fatalf("model mismatch: got %q want %q", cfg.Model, tt.wantModel)
			}
			if cfg.HistoryKey != tt.wantHistoryKey {
				t.Fatalf("historyKey mismatch: got %q want %q", cfg.HistoryKey, tt.wantHistoryKey)
			}
			if cfg.Authorization != tt.wantAuth {
				t.Fatalf("authorization mismatch: got %q want %q", cfg.Authorization, tt.wantAuth)
			}
			if tt.wantProvider != "" && cfg.Provider != tt.wantProvider {
				t.Fatalf("provider mismatch: got %q want %q", cfg.Provider, tt.wantProvider)
			}
			if cfg.UseNativeCaching != tt.wantUseCache {
				t.Fatalf("useNativeCaching mismatch: got %v want %v", cfg.UseNativeCaching, tt.wantUseCache)
			}
		})
	}
}

func TestParseAIEndpointValidation(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{name: "invalid URL encoding", input: "http://%zz", wantErr: "%zz"},
		{name: "unsupported scheme", input: "ftp://example.com", wantErr: "scheme must be http or https"},
		{name: "missing host", input: "https:///v1", wantErr: "host is required"},
		{name: "query not allowed", input: "https://api.example.com/v1?x=1", wantErr: "must not include query or fragment"},
		{name: "fragment not allowed", input: "https://api.example.com/v1#frag", wantErr: "must not include query or fragment"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, err := parseAIEndpoint(tt.input)
			if err == nil {
				t.Fatalf("expected error for %q", tt.input)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
			}
		})
	}
}

// TestParseAIEndpointSchemeDefaulting covers ASH_AI_ENDPOINT values that omit a
// scheme: local/LAN/bare hostnames default to http, everything else to https.
func TestParseAIEndpointSchemeDefaulting(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantScheme string
		wantHost   string
	}{
		{name: "bare localhost:port", input: "localhost:11434", wantScheme: "http", wantHost: "localhost"},
		{name: "bare single-label hostname", input: "brain:11434", wantScheme: "http", wantHost: "brain"},
		{name: "bare .local hostname", input: "brain.local:11434", wantScheme: "http", wantHost: "brain.local"},
		{name: "bare private IP", input: "192.168.1.3:11434", wantScheme: "http", wantHost: "192.168.1.3"},
		{name: "bare public hostname", input: "api.example.com", wantScheme: "https", wantHost: "api.example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseURL, host, scheme, err := parseAIEndpoint(tt.input)
			if err != nil {
				t.Fatalf("parseAIEndpoint(%q) returned error: %v", tt.input, err)
			}
			if scheme != tt.wantScheme {
				t.Fatalf("scheme mismatch: got %q want %q", scheme, tt.wantScheme)
			}
			if host != tt.wantHost {
				t.Fatalf("host mismatch: got %q want %q", host, tt.wantHost)
			}
			if !strings.HasPrefix(baseURL, tt.wantScheme+"://") {
				t.Fatalf("baseURL scheme mismatch: got %q", baseURL)
			}
		})
	}
}

func TestParseAIEndpointSuccessNormalization(t *testing.T) {
	baseURL, host, scheme, err := parseAIEndpoint("HTTPS://api.example.com:8443/v1/")
	if err != nil {
		t.Fatalf("parseAIEndpoint returned error: %v", err)
	}
	if baseURL != "https://api.example.com:8443/v1" {
		t.Fatalf("baseURL mismatch: got %q", baseURL)
	}
	if host != "api.example.com" {
		t.Fatalf("host mismatch: got %q", host)
	}
	if scheme != "https" {
		t.Fatalf("scheme mismatch: got %q", scheme)
	}
}

func TestParseAICacheEnabled(t *testing.T) {
	for _, value := range []string{"", "1", "true", "yes", "on", "enabled"} {
		got, err := parseAICacheEnabled(value)
		if err != nil {
			t.Fatalf("parseAICacheEnabled(%q) error: %v", value, err)
		}
		if !got {
			t.Fatalf("parseAICacheEnabled(%q) = false, want true", value)
		}
	}

	for _, value := range []string{"0", "false", "no", "off", "disabled"} {
		got, err := parseAICacheEnabled(value)
		if err != nil {
			t.Fatalf("parseAICacheEnabled(%q) error: %v", value, err)
		}
		if got {
			t.Fatalf("parseAICacheEnabled(%q) = true, want false", value)
		}
	}

	_, err := parseAICacheEnabled("sometimes")
	if err == nil || !strings.Contains(err.Error(), aiEnvCache) {
		t.Fatalf("expected invalid cache error, got %v", err)
	}
}

func TestResolveAIProviderValidation(t *testing.T) {
	provider, err := resolveAIProvider("", "http://localhost:11434", "localhost")
	if err != nil {
		t.Fatalf("resolveAIProvider auto-detect returned error: %v", err)
	}
	if provider != providerOllama {
		t.Fatalf("expected ollama provider, got %q", provider)
	}

	provider, err = resolveAIProvider("gemini", "http://localhost:11434", "localhost")
	if err != nil {
		t.Fatalf("resolveAIProvider(gemini) returned error: %v", err)
	}
	if provider != providerGoogle {
		t.Fatalf("expected google provider for gemini alias, got %q", provider)
	}

	_, err = resolveAIProvider("unsupported", "http://localhost:11434", "localhost")
	if err == nil || !strings.Contains(err.Error(), "ASH_AI_PROVIDER must be one of") {
		t.Fatalf("expected invalid provider error, got %v", err)
	}
}

func TestDetectAIProviderAndCloudHostClassification(t *testing.T) {
	if got := detectAIProvider("https://api.openai.com/v1", "api.openai.com"); got != providerOpenAI {
		t.Fatalf("expected openai provider, got %q", got)
	}
	if got := detectAIProvider("https://anthropic.com/v1", "api.anthropic.com"); got != providerAnthropic {
		t.Fatalf("expected anthropic provider, got %q", got)
	}
	if got := detectAIProvider("https://us-central1-aiplatform.googleapis.com/openai", "us-central1-aiplatform.googleapis.com"); got != providerGoogle {
		t.Fatalf("expected google provider for openai path adapter, got %q", got)
	}
	if got := detectAIProvider("https://generativelanguage.googleapis.com/v1beta/models", "generativelanguage.googleapis.com"); got != providerGoogle {
		t.Fatalf("expected google provider for generative language host, got %q", got)
	}
	if got := detectAIProvider("https://ollama.com", "ollama.com"); got != providerOllama {
		t.Fatalf("expected ollama provider for Ollama Cloud host, got %q", got)
	}
	if got := detectAIProvider("https://api.mistral.ai/v1", "api.mistral.ai"); got != providerOpenAI {
		t.Fatalf("expected openai-compatible provider for unknown cloud host, got %q", got)
	}
	if got := detectAIProvider("https://api.cohere.com", "api.cohere.com"); got != providerCohere {
		t.Fatalf("expected cohere provider, got %q", got)
	}
	if got := detectAIProvider("https://bedrock-runtime.us-east-1.amazonaws.com", "bedrock-runtime.us-east-1.amazonaws.com"); got != providerBedrock {
		t.Fatalf("expected bedrock provider, got %q", got)
	}

	if isCloudAIHost("localhost") {
		t.Fatalf("localhost should not be classified as cloud")
	}
	if isCloudAIHost("127.0.0.1") {
		t.Fatalf("loopback IPv4 should not be classified as cloud")
	}
	if isCloudAIHost("::1") {
		t.Fatalf("loopback IPv6 should not be classified as cloud")
	}
	if !isCloudAIHost("api.example.com") {
		t.Fatalf("public hostname should be classified as cloud")
	}
}

func TestIsCloudAIHostLANExemption(t *testing.T) {
	lanHosts := []string{"brain.local", "192.168.1.50", "10.0.0.5", "172.16.0.1", "fd12:3456:789a::1"}

	t.Run("not strict: LAN hosts are exempt", func(t *testing.T) {
		t.Setenv("ASH_STRICT", "")
		for _, host := range lanHosts {
			if isCloudAIHost(host) {
				t.Fatalf("expected %q to not be classified as cloud", host)
			}
		}
		if !isCloudAIHost("api.example.com") {
			t.Fatalf("public hostname should still be classified as cloud")
		}
		if !isCloudAIHost("8.8.8.8") {
			t.Fatalf("public IP should still be classified as cloud")
		}
	})

	t.Run("strict: LAN hosts require https/token like any other cloud host", func(t *testing.T) {
		t.Setenv("ASH_STRICT", "true")
		for _, host := range lanHosts {
			if !isCloudAIHost(host) {
				t.Fatalf("expected %q to be classified as cloud under ASH_STRICT", host)
			}
		}
		if isCloudAIHost("localhost") {
			t.Fatalf("localhost should remain exempt even under ASH_STRICT")
		}
		if isCloudAIHost("127.0.0.1") {
			t.Fatalf("loopback should remain exempt even under ASH_STRICT")
		}
	})
}
