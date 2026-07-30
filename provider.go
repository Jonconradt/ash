package main

import (
	"fmt"
	"net/http"
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
