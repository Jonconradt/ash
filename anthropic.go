package main

import (
	"errors"
	"fmt"
	"net/http"
)

type anthropicAdapter struct{}

func (a anthropicAdapter) Name() aiProvider {
	return providerAnthropic
}

func (a anthropicAdapter) Capabilities() providerCapabilities {
	return providerCapabilities{SupportsNativeCaching: true}
}

func (a anthropicAdapter) Endpoint(baseURL string) string {
	return baseURL + "/v1/messages"
}

func (a anthropicAdapter) BuildPayload(aiCfg aiConfig, messages []message, tools []toolDefinition) ([]byte, error) {
	return nil, errors.New("Anthropic provider is not implemented yet")
}

func (a anthropicAdapter) ApplyHeaders(req *http.Request, aiCfg aiConfig) {
	req.Header.Set("Content-Type", "application/json")
	if aiCfg.Authorization != "" {
		req.Header.Set("Authorization", aiCfg.Authorization)
	}
}

func (a anthropicAdapter) ParseResponse(body []byte) (chatResponse, error) {
	return chatResponse{}, fmt.Errorf("Anthropic provider is not implemented yet")
}
