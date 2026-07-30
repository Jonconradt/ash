package main

import (
	"encoding/json"
	"net/http"
)

type ollamaAdapter struct{}

func (a ollamaAdapter) Name() aiProvider {
	return providerOllama
}

func (a ollamaAdapter) Capabilities() providerCapabilities {
	return providerCapabilities{}
}

func (a ollamaAdapter) Endpoint(baseURL string) string {
	return baseURL + "/api/chat"
}

func (a ollamaAdapter) BuildPayload(aiCfg aiConfig, messages []message, tools []toolDefinition) ([]byte, error) {
	requestBody := chatRequest{
		Model:    aiCfg.Model,
		Messages: messages,
		Tools:    tools,
		Stream:   false,
	}
	return json.Marshal(requestBody)
}

func (a ollamaAdapter) ApplyHeaders(req *http.Request, aiCfg aiConfig) {
	req.Header.Set("Content-Type", "application/json")
	if aiCfg.Authorization != "" {
		req.Header.Set("Authorization", aiCfg.Authorization)
	}
}

func (a ollamaAdapter) ParseResponse(body []byte) (chatResponse, error) {
	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return chatResponse{}, err
	}
	return parsed, nil
}
