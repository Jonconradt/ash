package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type googleAdapter struct{}

type googleChatCompletionsRequest struct {
	Model      string               `json:"model"`
	Messages   []googleChatMessage  `json:"messages"`
	Tools      []googleFunctionTool `json:"tools,omitempty"`
	ToolChoice string               `json:"tool_choice,omitempty"`
	Stream     bool                 `json:"stream"`
	Metadata   map[string]string    `json:"metadata,omitempty"`
}

type googleFunctionTool struct {
	Type     string             `json:"type"`
	Function googleFunctionSpec `json:"function"`
}

type googleFunctionSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type googleChatMessage struct {
	Role       string               `json:"role"`
	Content    string               `json:"content,omitempty"`
	ToolCalls  []googleToolCallWire `json:"tool_calls,omitempty"`
	ToolCallID string               `json:"tool_call_id,omitempty"`
	Name       string               `json:"name,omitempty"`
}

type googleToolCallWire struct {
	ID       string                 `json:"id,omitempty"`
	Type     string                 `json:"type,omitempty"`
	Function googleToolFunctionWire `json:"function"`
}

type googleToolFunctionWire struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type googleChatCompletionsResponse struct {
	Choices       []googleChoice       `json:"choices"`
	Error         *googleAPIError      `json:"error,omitempty"`
	Usage         *googleUsage         `json:"usage,omitempty"`
	UsageMetadata *googleUsageMetadata `json:"usageMetadata,omitempty"`
}

type googleUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type googleUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
}

type googleChoice struct {
	Message googleChatMessage `json:"message"`
}

type googleAPIError struct {
	Message string `json:"message"`
}

func (a googleAdapter) Name() aiProvider {
	return providerGoogle
}

func (a googleAdapter) Capabilities() providerCapabilities {
	return providerCapabilities{SupportsNativeCaching: true}
}

func (a googleAdapter) Endpoint(baseURL string) string {
	trimmed := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(trimmed, "/chat/completions") {
		return trimmed
	}
	return trimmed + "/chat/completions"
}

func (a googleAdapter) BuildPayload(aiCfg aiConfig, messages []message, tools []toolDefinition) ([]byte, error) {
	request := googleChatCompletionsRequest{
		Model:    aiCfg.Model,
		Stream:   false,
		Messages: make([]googleChatMessage, 0, len(messages)),
	}

	for _, msg := range messages {
		wire := googleChatMessage{Role: msg.Role, Content: msg.Content}
		if msg.Role == "tool" {
			wire.ToolCallID = msg.ToolCallID
			wire.Name = msg.ToolName
		}
		if len(msg.ToolCalls) > 0 {
			wire.ToolCalls = make([]googleToolCallWire, 0, len(msg.ToolCalls))
			for _, call := range msg.ToolCalls {
				wire.ToolCalls = append(wire.ToolCalls, googleToolCallWire{
					ID:   call.ID,
					Type: "function",
					Function: googleToolFunctionWire{
						Name:      call.Function.Name,
						Arguments: marshalJSONObject(call.Function.Arguments),
					},
				})
			}
		}
		request.Messages = append(request.Messages, wire)
	}

	if len(tools) > 0 {
		request.Tools = make([]googleFunctionTool, 0, len(tools))
		for _, tool := range tools {
			request.Tools = append(request.Tools, googleFunctionTool{
				Type: "function",
				Function: googleFunctionSpec{
					Name:        tool.Function.Name,
					Description: tool.Function.Description,
					Parameters:  tool.Function.Parameters,
				},
			})
		}
		request.ToolChoice = "auto"
	}

	if shouldUseProviderNativeCaching(aiCfg, a) {
		request.Metadata = map[string]string{"cache_preference": "provider-default"}
	}

	return json.Marshal(request)
}

func (a googleAdapter) ApplyHeaders(req *http.Request, aiCfg aiConfig) {
	req.Header.Set("Content-Type", "application/json")
	if aiCfg.Authorization != "" {
		req.Header.Set("Authorization", aiCfg.Authorization)
	}
}

func (a googleAdapter) ParseResponse(body []byte) (chatResponse, error) {
	var parsed googleChatCompletionsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return chatResponse{}, err
	}
	if parsed.Error != nil && strings.TrimSpace(parsed.Error.Message) != "" {
		return chatResponse{Error: parsed.Error.Message}, nil
	}
	if len(parsed.Choices) == 0 {
		return chatResponse{}, errors.New("google response missing choices")
	}

	msg := parsed.Choices[0].Message
	out := message{Role: msg.Role, Content: msg.Content}
	if out.Role == "" {
		out.Role = "assistant"
	}
	if len(msg.ToolCalls) > 0 {
		out.ToolCalls = make([]toolCall, 0, len(msg.ToolCalls))
		for _, call := range msg.ToolCalls {
			out.ToolCalls = append(out.ToolCalls, toolCall{
				ID:   call.ID,
				Type: call.Type,
				Function: toolFunctionCall{
					Name:      call.Function.Name,
					Arguments: parseJSONObject(call.Function.Arguments),
				},
			})
		}
	}

	result := chatResponse{Message: out}
	if parsed.Usage != nil {
		result.Usage = chatUsage{
			InputTokens:  parsed.Usage.PromptTokens,
			OutputTokens: parsed.Usage.CompletionTokens,
			Available:    true,
		}
	} else if parsed.UsageMetadata != nil {
		result.Usage = chatUsage{
			InputTokens:  parsed.UsageMetadata.PromptTokenCount,
			OutputTokens: parsed.UsageMetadata.CandidatesTokenCount,
			Available:    true,
		}
	}
	return result, nil
}
