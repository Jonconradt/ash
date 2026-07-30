package main

import (
	"errors"
	"fmt"
	"net/http"
)

type openAIAdapter struct{}

func (a openAIAdapter) Name() aiProvider {
	return providerOpenAI
}

func (a openAIAdapter) Capabilities() providerCapabilities {
	return providerCapabilities{SupportsNativeCaching: true}
}

func (a openAIAdapter) Endpoint(baseURL string) string {
	return baseURL + "/v1/responses"
}

func (a openAIAdapter) BuildPayload(aiCfg aiConfig, messages []message, tools []toolDefinition) ([]byte, error) {
	return nil, errors.New("OpenAI provider is not implemented yet")
}

func (a openAIAdapter) ApplyHeaders(req *http.Request, aiCfg aiConfig) {
	req.Header.Set("Content-Type", "application/json")
	if aiCfg.Authorization != "" {
		req.Header.Set("Authorization", aiCfg.Authorization)
	}
}

func (a openAIAdapter) ParseResponse(body []byte) (chatResponse, error) {
	return chatResponse{}, fmt.Errorf("OpenAI provider is not implemented yet")
}
