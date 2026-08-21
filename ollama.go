package main

import (
	"encoding/json"
	"net/http"
)

type ollamaAdapter struct{}

type ollamaMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
	ToolName  string           `json:"tool_name,omitempty"`
}

type ollamaToolCall struct {
	Type     string             `json:"type,omitempty"`
	Function ollamaFunctionCall `json:"function"`
}

type ollamaFunctionCall struct {
	Index     *int           `json:"index,omitempty"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type ollamaChatRequest struct {
	Model      string           `json:"model"`
	Messages   []ollamaMessage  `json:"messages"`
	Tools      []toolDefinition `json:"tools,omitempty"`
	ToolChoice string           `json:"tool_choice,omitempty"`
	Stream     bool             `json:"stream"`
}

type ollamaChatResponse struct {
	Message         message `json:"message"`
	Error           string  `json:"error"`
	PromptEvalCount int     `json:"prompt_eval_count"`
	EvalCount       int     `json:"eval_count"`
}

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
	wireMessages := make([]ollamaMessage, 0, len(messages))
	for _, msg := range messages {
		wireMsg := ollamaMessage{
			Role:     msg.Role,
			Content:  msg.Content,
			ToolName: msg.ToolName,
		}
		if len(msg.ToolCalls) > 0 {
			wireMsg.ToolCalls = make([]ollamaToolCall, 0, len(msg.ToolCalls))
			for _, call := range msg.ToolCalls {
				wireMsg.ToolCalls = append(wireMsg.ToolCalls, ollamaToolCall{
					Type: call.Type,
					Function: ollamaFunctionCall{
						Index:     call.Function.Index,
						Name:      call.Function.Name,
						Arguments: call.Function.Arguments,
					},
				})
			}
		}
		wireMessages = append(wireMessages, wireMsg)
	}

	requestBody := ollamaChatRequest{
		Model:    aiCfg.Model,
		Messages: wireMessages,
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
	var parsed ollamaChatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return chatResponse{}, err
	}
	return chatResponse{
		Message: parsed.Message,
		Error:   parsed.Error,
		Usage: chatUsage{
			InputTokens:  parsed.PromptEvalCount,
			OutputTokens: parsed.EvalCount,
			Available:    parsed.PromptEvalCount > 0 || parsed.EvalCount > 0,
		},
	}, nil
}
