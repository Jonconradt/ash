package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type aiProvider string

const (
	providerOllama    aiProvider = "ollama"
	providerOpenAI    aiProvider = "openai"
	providerGoogle    aiProvider = "google"
	providerAnthropic aiProvider = "anthropic"
)

type providerCapabilities struct {
	SupportsNativeCaching bool
}

type providerAdapter interface {
	Name() aiProvider
	Capabilities() providerCapabilities
	Endpoint(baseURL string) string
	BuildPayload(aiCfg aiConfig, messages []message, tools []toolDefinition) ([]byte, error)
	ApplyHeaders(req *http.Request, aiCfg aiConfig)
	ParseResponse(body []byte) (chatResponse, error)
}

var providerRegistry = map[aiProvider]providerAdapter{
	providerOllama:    ollamaAdapter{},
	providerOpenAI:    openAIAdapter{},
	providerGoogle:    googleAdapter{},
	providerAnthropic: anthropicAdapter{},
}

func adapterForProvider(provider aiProvider) (providerAdapter, error) {
	adapter, ok := providerRegistry[provider]
	if !ok {
		return nil, fmt.Errorf("unsupported AI provider %q", provider)
	}
	return adapter, nil
}

func shouldUseProviderNativeCaching(aiCfg aiConfig, adapter providerAdapter) bool {
	return aiCfg.UseNativeCaching && adapter.Capabilities().SupportsNativeCaching
}

func parseJSONObject(raw string) map[string]any {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return map[string]any{}
	}
	if out == nil {
		return map[string]any{}
	}
	return out
}

func marshalJSONObject(value map[string]any) string {
	if len(value) == 0 {
		return "{}"
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}
