package main

import (
	"context"
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
	providerCohere    aiProvider = "cohere"
	providerBedrock   aiProvider = "bedrock"
)

type providerCapabilities struct {
	SupportsNativeCaching bool
}

// providerAdapter is the minimal contract every provider implements.
type providerAdapter interface {
	Name() aiProvider
	Capabilities() providerCapabilities
}

// byteProviderAdapter is implemented by adapters that build the raw HTTP
// request/response themselves; chat() drives their retry loop directly.
type byteProviderAdapter interface {
	providerAdapter
	Endpoint(baseURL string) string
	BuildPayload(aiCfg aiConfig, messages []message, tools []toolDefinition) ([]byte, error)
	ApplyHeaders(req *http.Request, aiCfg aiConfig)
	ParseResponse(body []byte) (chatResponse, error)
}

// sdkProviderAdapter is implemented by adapters backed by an official provider
// SDK client (constructed with newAshHTTPClient, which already gets retry and
// broker support from ashRoundTripper) instead of hand-built HTTP requests.
type sdkProviderAdapter interface {
	providerAdapter
	Send(ctx context.Context, aiCfg aiConfig, messages []message, tools []toolDefinition) (chatResponse, error)
}

// streamDelta is one incremental update from a streaming provider call.
// ToolCallDelta is only ever populated once a tool call's arguments are fully
// buffered server-side into a single delta event; callers must not dispatch a
// tool call until the stream completes.
type streamDelta struct {
	TextDelta string
}

// streamingProviderAdapter is an optional extension of sdkProviderAdapter for
// adapters that can consume the provider's streaming (SSE) API. onDelta is
// invoked for each incremental text chunk; the final return value is the same
// complete chatResponse SendStream would have produced non-streaming.
type streamingProviderAdapter interface {
	sdkProviderAdapter
	SendStream(ctx context.Context, aiCfg aiConfig, messages []message, tools []toolDefinition, onDelta func(streamDelta)) (chatResponse, error)
}

var providerRegistry = map[aiProvider]providerAdapter{
	providerOllama:    ollamaAdapter{},
	providerOpenAI:    openAIAdapter{},
	providerGoogle:    googleAdapter{},
	providerAnthropic: anthropicAdapter{},
	providerCohere:    cohereAdapter{},
	providerBedrock:   bedrockAdapter{},
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
