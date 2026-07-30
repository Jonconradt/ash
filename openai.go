package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type openAIAdapter struct{}

type openAIResponsesRequest struct {
	Model      string               `json:"model"`
	Input      []map[string]any     `json:"input"`
	Tools      []openAIFunctionTool `json:"tools,omitempty"`
	ToolChoice string               `json:"tool_choice,omitempty"`
	Metadata   map[string]string    `json:"metadata,omitempty"`
}

type openAIFunctionTool struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type openAIResponsesResponse struct {
	Output []openAIOutputItem `json:"output"`
	Error  *openAIErrorBody   `json:"error,omitempty"`
}

type openAIOutputItem struct {
	Type      string              `json:"type"`
	ID        string              `json:"id,omitempty"`
	CallID    string              `json:"call_id,omitempty"`
	Name      string              `json:"name,omitempty"`
	Arguments string              `json:"arguments,omitempty"`
	Role      string              `json:"role,omitempty"`
	Content   []openAIContentItem `json:"content,omitempty"`
}

type openAIContentItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type openAIErrorBody struct {
	Message string `json:"message"`
}

func (a openAIAdapter) Name() aiProvider {
	return providerOpenAI
}

func (a openAIAdapter) Capabilities() providerCapabilities {
	return providerCapabilities{SupportsNativeCaching: true}
}

func (a openAIAdapter) Endpoint(baseURL string) string {
	if strings.HasSuffix(strings.TrimRight(baseURL, "/"), "/v1") {
		return strings.TrimRight(baseURL, "/") + "/responses"
	}
	return strings.TrimRight(baseURL, "/") + "/v1/responses"
}

func (a openAIAdapter) BuildPayload(aiCfg aiConfig, messages []message, tools []toolDefinition) ([]byte, error) {
	input := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		switch msg.Role {
		case "tool":
			callID := strings.TrimSpace(msg.ToolCallID)
			if callID == "" {
				callID = strings.TrimSpace(msg.ToolName)
			}
			if callID == "" {
				continue
			}
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": callID,
				"output":  msg.Content,
			})
		default:
			if text := strings.TrimSpace(msg.Content); text != "" {
				input = append(input, map[string]any{
					"type": "message",
					"role": msg.Role,
					"content": []map[string]any{{
						"type": "input_text",
						"text": text,
					}},
				})
			}
			for _, call := range msg.ToolCalls {
				callID := strings.TrimSpace(call.ID)
				if callID == "" {
					callID = fmt.Sprintf("call_%s", strings.ReplaceAll(call.Function.Name, " ", "_"))
				}
				input = append(input, map[string]any{
					"type":      "function_call",
					"call_id":   callID,
					"name":      call.Function.Name,
					"arguments": marshalJSONObject(call.Function.Arguments),
				})
			}
		}
	}

	request := openAIResponsesRequest{
		Model: aiCfg.Model,
		Input: input,
	}
	if len(tools) > 0 {
		request.Tools = make([]openAIFunctionTool, 0, len(tools))
		for _, tool := range tools {
			request.Tools = append(request.Tools, openAIFunctionTool{
				Type:        "function",
				Name:        tool.Function.Name,
				Description: tool.Function.Description,
				Parameters:  tool.Function.Parameters,
			})
		}
		request.ToolChoice = "auto"
	}

	if shouldUseProviderNativeCaching(aiCfg, a) {
		request.Metadata = map[string]string{"cache_preference": "provider-default"}
	}

	return json.Marshal(request)
}

func (a openAIAdapter) ApplyHeaders(req *http.Request, aiCfg aiConfig) {
	req.Header.Set("Content-Type", "application/json")
	if aiCfg.Authorization != "" {
		req.Header.Set("Authorization", aiCfg.Authorization)
	}
}

func (a openAIAdapter) ParseResponse(body []byte) (chatResponse, error) {
	var parsed openAIResponsesResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return chatResponse{}, err
	}
	if parsed.Error != nil && strings.TrimSpace(parsed.Error.Message) != "" {
		return chatResponse{Error: parsed.Error.Message}, nil
	}

	assistant := message{Role: "assistant"}
	textParts := make([]string, 0, 2)
	for _, item := range parsed.Output {
		switch item.Type {
		case "message":
			for _, content := range item.Content {
				if (content.Type == "output_text" || content.Type == "text") && strings.TrimSpace(content.Text) != "" {
					textParts = append(textParts, content.Text)
				}
			}
		case "function_call":
			assistant.ToolCalls = append(assistant.ToolCalls, toolCall{
				ID:   item.CallID,
				Type: "function",
				Function: toolFunctionCall{
					Name:      item.Name,
					Arguments: parseJSONObject(item.Arguments),
				},
			})
		}
	}
	assistant.Content = strings.TrimSpace(strings.Join(textParts, "\n"))

	return chatResponse{Message: assistant}, nil
}
