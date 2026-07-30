package main

import (
	"errors"
	"fmt"
	"net/http"
)

type googleAdapter struct{}

func (a googleAdapter) Name() aiProvider {
	return providerGoogle
}

func (a googleAdapter) Capabilities() providerCapabilities {
	return providerCapabilities{SupportsNativeCaching: true}
}

func (a googleAdapter) Endpoint(baseURL string) string {
	return baseURL + "/chat/completions"
}

func (a googleAdapter) BuildPayload(aiCfg aiConfig, messages []message, tools []toolDefinition) ([]byte, error) {
	return nil, errors.New("Google provider is not implemented yet")
}

func (a googleAdapter) ApplyHeaders(req *http.Request, aiCfg aiConfig) {
	req.Header.Set("Content-Type", "application/json")
	if aiCfg.Authorization != "" {
		req.Header.Set("Authorization", aiCfg.Authorization)
	}
}

func (a googleAdapter) ParseResponse(body []byte) (chatResponse, error) {
	return chatResponse{}, fmt.Errorf("Google provider is not implemented yet")
}
