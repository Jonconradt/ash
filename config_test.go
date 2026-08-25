package main

import (
	"strings"
	"testing"
)

func TestParseAIConfigFromEnv(t *testing.T) {
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
				"AI_ENDPOINT": "http://localhost:11434",
				"AI_MODEL":    "llama3.1",
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
				"AI_ENDPOINT":   "https://api.example.com/ollama",
				"AI_MODEL":      "mistral",
				"AI_AUTH_TYPE":  "bearer",
				"AI_AUTH_TOKEN": "abc123",
			},
			wantBaseURL:    "https://api.example.com/ollama",
			wantModel:      "mistral",
			wantHistoryKey: "https://api.example.com/ollama/mistral",
			wantAuth:       "Bearer abc123",
			wantProvider:   providerOllama,
			wantUseCache:   true,
		},
		{
			name: "auto-detect openai provider",
			env: map[string]string{
				"AI_ENDPOINT":   "https://api.openai.com/v1",
				"AI_MODEL":      "gpt-4.1-mini",
				"AI_AUTH_TYPE":  "bearer",
				"AI_AUTH_TOKEN": "openai-token",
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
				"AI_ENDPOINT": "http://localhost:11434",
				"AI_MODEL":    "llama3.1",
				"AI_PROVIDER": "google",
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
				"AI_ENDPOINT": "http://localhost:11434",
				"AI_MODEL":    "llama3.1",
				"AI_CACHE":    "off",
			},
			wantBaseURL:    "http://localhost:11434",
			wantModel:      "llama3.1",
			wantHistoryKey: "http://localhost:11434/llama3.1",
			wantProvider:   providerOllama,
			wantUseCache:   false,
		},
		{
			name: "legacy AI env rejected",
			env: map[string]string{
				"AI": "ollama://localhost/llama3.1",
			},
			wantErr: "AI is no longer supported",
		},
		{
			name: "missing endpoint",
			env: map[string]string{
				"AI_MODEL": "llama3.1",
				"SHELL":    "/bin/zsh",
			},
			wantErr: "Please add these lines to your ~/.zshrc file",
		},
		{
			name: "missing model",
			env: map[string]string{
				"AI_ENDPOINT": "http://localhost:11434",
				"SHELL":       "/bin/bash",
			},
			wantErr: "Please add these lines to your ~/.bashrc file",
		},
		{
			name: "token enables bearer auth",
			env: map[string]string{
				"AI_ENDPOINT":   "http://localhost:11434",
				"AI_MODEL":      "llama3.1",
				"AI_AUTH_TOKEN": "abc",
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
				"AI_ENDPOINT":  "http://localhost:11434",
				"AI_MODEL":     "llama3.1",
				"AI_AUTH_TYPE": "basic",
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
				"AI_ENDPOINT":   "http://api.example.com",
				"AI_MODEL":      "llama3.1",
				"AI_AUTH_TYPE":  "bearer",
				"AI_AUTH_TOKEN": "abc",
			},
			wantErr: "AI_ENDPOINT must use https for cloud endpoints",
		},
		{
			name: "cloud endpoint requires auth",
			env: map[string]string{
				"AI_ENDPOINT": "https://api.example.com",
				"AI_MODEL":    "llama3.1",
				"SHELL":       "/bin/zsh",
			},
			wantErr: "export AI_AUTH_TOKEN='your-token'",
		},
		{
			name: "invalid provider override",
			env: map[string]string{
				"AI_ENDPOINT": "http://localhost:11434",
				"AI_MODEL":    "llama3.1",
				"AI_PROVIDER": "unsupported-provider",
			},
			wantErr: "AI_PROVIDER must be one of",
		},
		{
			name: "invalid cache override",
			env: map[string]string{
				"AI_ENDPOINT": "http://localhost:11434",
				"AI_MODEL":    "llama3.1",
				"AI_CACHE":    "sometimes",
			},
			wantErr: "AI_CACHE must be a boolean-like value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AI", "")
			t.Setenv("AI_ENDPOINT", "")
			t.Setenv("AI_MODEL", "")
			t.Setenv("AI_AUTH_TYPE", "")
			t.Setenv("AI_AUTH_TOKEN", "")
			t.Setenv("AI_PROVIDER", "")
			t.Setenv("AI_CACHE", "")
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
		{name: "missing scheme", input: "localhost:11434", wantErr: "scheme must be http or https"},
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
	if err == nil || !strings.Contains(err.Error(), "boolean-like value") {
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
	if err == nil || !strings.Contains(err.Error(), "AI_PROVIDER must be one of") {
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
