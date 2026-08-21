package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type anthropicAdapter struct{}

const anthropicVersionHeaderValue = "2023-06-01"

type anthropicMessagesRequest struct {
	Model      string             `json:"model"`
	MaxTokens  int                `json:"max_tokens"`
	System     any                `json:"system,omitempty"`
	Messages   []anthropicMessage `json:"messages"`
	Tools      []anthropicTool    `json:"tools,omitempty"`
	ToolChoice map[string]string  `json:"tool_choice,omitempty"`
}

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
}

type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

type anthropicContentBlock struct {
	Type         string         `json:"type"`
	Text         string         `json:"text,omitempty"`
	ID           string         `json:"id,omitempty"`
	Name         string         `json:"name,omitempty"`
	Input        map[string]any `json:"input,omitempty"`
	ToolUseID    string         `json:"tool_use_id,omitempty"`
	Content      string         `json:"content,omitempty"`
	CacheControl *cacheControl  `json:"cache_control,omitempty"`
}

type cacheControl struct {
	Type string `json:"type"`
}

type anthropicMessagesResponse struct {
	Content []anthropicContentBlock `json:"content"`
	Error   *anthropicErrorBody     `json:"error,omitempty"`
	Usage   *anthropicUsage         `json:"usage,omitempty"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type anthropicErrorBody struct {
	Message string `json:"message"`
}

func (a anthropicAdapter) Name() aiProvider {
	return providerAnthropic
}

func (a anthropicAdapter) Capabilities() providerCapabilities {
	return providerCapabilities{SupportsNativeCaching: true}
}

func (a anthropicAdapter) Endpoint(baseURL string) string {
	if strings.HasSuffix(strings.TrimRight(baseURL, "/"), "/v1") {
		return strings.TrimRight(baseURL, "/") + "/messages"
	}
	return strings.TrimRight(baseURL, "/") + "/v1/messages"
}

func (a anthropicAdapter) BuildPayload(aiCfg aiConfig, messages []message, tools []toolDefinition) ([]byte, error) {
	request := anthropicMessagesRequest{
		Model:     aiCfg.Model,
		MaxTokens: 2048,
		Messages:  make([]anthropicMessage, 0, len(messages)),
	}

	useCache := shouldUseProviderNativeCaching(aiCfg, a)
	systemBlocks := make([]anthropicContentBlock, 0, 1)
	for _, msg := range messages {
		if msg.Role == "system" {
			if text := strings.TrimSpace(msg.Content); text != "" {
				block := anthropicContentBlock{Type: "text", Text: text}
				if useCache {
					block.CacheControl = &cacheControl{Type: "ephemeral"}
				}
				systemBlocks = append(systemBlocks, block)
			}
			continue
		}

		role := msg.Role
		if role == "tool" {
			role = "user"
		}
		wire := anthropicMessage{Role: role}

		if msg.Role == "tool" {
			if strings.TrimSpace(msg.ToolCallID) == "" {
				continue
			}
			wire.Content = append(wire.Content, anthropicContentBlock{
				Type:      "tool_result",
				ToolUseID: msg.ToolCallID,
				Content:   msg.Content,
			})
		} else {
			if text := strings.TrimSpace(msg.Content); text != "" {
				block := anthropicContentBlock{Type: "text", Text: text}
				if useCache && msg.Role == "user" {
					block.CacheControl = &cacheControl{Type: "ephemeral"}
				}
				wire.Content = append(wire.Content, block)
			}
			for _, call := range msg.ToolCalls {
				callID := strings.TrimSpace(call.ID)
				if callID == "" {
					callID = fmt.Sprintf("call_%s", strings.ReplaceAll(call.Function.Name, " ", "_"))
				}
				wire.Content = append(wire.Content, anthropicContentBlock{
					Type:  "tool_use",
					ID:    callID,
					Name:  call.Function.Name,
					Input: call.Function.Arguments,
				})
			}
		}

		if len(wire.Content) > 0 {
			request.Messages = append(request.Messages, wire)
		}
	}

	if len(systemBlocks) > 0 {
		request.System = systemBlocks
	}

	if len(tools) > 0 {
		request.Tools = make([]anthropicTool, 0, len(tools))
		for _, tool := range tools {
			request.Tools = append(request.Tools, anthropicTool{
				Name:        tool.Function.Name,
				Description: tool.Function.Description,
				InputSchema: tool.Function.Parameters,
			})
		}
		request.ToolChoice = map[string]string{"type": "auto"}
	}

	return json.Marshal(request)
}

func (a anthropicAdapter) ApplyHeaders(req *http.Request, aiCfg aiConfig) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", anthropicVersionHeaderValue)
	if aiCfg.AuthToken != "" {
		req.Header.Set("x-api-key", aiCfg.AuthToken)
	} else if aiCfg.Authorization != "" {
		token := strings.TrimSpace(strings.TrimPrefix(aiCfg.Authorization, "Bearer "))
		if token != "" {
			req.Header.Set("x-api-key", token)
		}
	}
}

func (a anthropicAdapter) ParseResponse(body []byte) (chatResponse, error) {
	var parsed anthropicMessagesResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return chatResponse{}, err
	}
	if parsed.Error != nil && strings.TrimSpace(parsed.Error.Message) != "" {
		return chatResponse{Error: parsed.Error.Message}, nil
	}

	assistant := message{Role: "assistant"}
	textParts := make([]string, 0, 2)
	for _, block := range parsed.Content {
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				textParts = append(textParts, block.Text)
			}
		case "tool_use":
			assistant.ToolCalls = append(assistant.ToolCalls, toolCall{
				ID:   block.ID,
				Type: "function",
				Function: toolFunctionCall{
					Name:      block.Name,
					Arguments: block.Input,
				},
			})
		}
	}
	assistant.Content = strings.TrimSpace(strings.Join(textParts, "\n"))
	result := chatResponse{Message: assistant}
	if parsed.Usage != nil {
		result.Usage = chatUsage{
			InputTokens:  parsed.Usage.InputTokens,
			OutputTokens: parsed.Usage.OutputTokens,
			Available:    true,
		}
	}
	return result, nil
}
