package app

import (
	"context"
	"errors"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
)

type anthropicAdapter struct{}

func (a anthropicAdapter) Name() aiProvider {
	return providerAnthropic
}

func (a anthropicAdapter) Capabilities() providerCapabilities {
	return providerCapabilities{SupportsNativeCaching: true}
}

// anthropicEndpoint strips a trailing `/v1`, since the SDK appends its own `/v1/messages`.
func anthropicEndpoint(baseURL string) string {
	trimmed := strings.TrimRight(baseURL, "/")
	return strings.TrimSuffix(trimmed, "/v1")
}

// Send implements sdkProviderAdapter using the official anthropic-sdk-go Messages API client.
func (a anthropicAdapter) Send(ctx context.Context, aiCfg aiConfig, messages []message, tools []toolDefinition) (chatResponse, error) {
	client := anthropic.NewClient(
		option.WithAPIKey(aiCfg.AuthToken),
		option.WithBaseURL(anthropicEndpoint(aiCfg.BaseURL)),
		option.WithHTTPClient(newAshHTTPClient()),
		option.WithMaxRetries(0), // retries are handled by ashRoundTripper
	)

	useCache := shouldUseProviderNativeCaching(aiCfg, a)
	system, msgs := buildAnthropicMessages(messages, useCache)

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(aiCfg.Model),
		MaxTokens: 2048,
		System:    system,
		Messages:  msgs,
	}
	if len(tools) > 0 {
		params.Tools = buildAnthropicTools(tools)
	}

	resp, err := client.Messages.New(ctx, params)
	if err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) {
			return chatResponse{}, chatStatusError{StatusCode: apiErr.StatusCode, Body: apiErr.Error()}
		}
		return chatResponse{}, err
	}
	return parseAnthropicMessage(resp), nil
}

func buildAnthropicMessages(messages []message, useCache bool) ([]anthropic.TextBlockParam, []anthropic.MessageParam) {
	system := make([]anthropic.TextBlockParam, 0, 1)
	out := make([]anthropic.MessageParam, 0, len(messages))

	for _, msg := range messages {
		if msg.Role == "system" {
			if text := strings.TrimSpace(msg.Content); text != "" {
				block := anthropic.TextBlockParam{Text: text}
				if useCache {
					block.CacheControl = anthropic.NewCacheControlEphemeralParam()
				}
				system = append(system, block)
			}
			continue
		}

		blocks := make([]anthropic.ContentBlockParamUnion, 0, 2)
		if msg.Role == "tool" {
			if strings.TrimSpace(msg.ToolCallID) == "" {
				continue
			}
			blocks = append(blocks, anthropic.NewToolResultBlock(msg.ToolCallID, msg.Content, false))
		} else {
			if text := strings.TrimSpace(msg.Content); text != "" {
				block := anthropic.NewTextBlock(text)
				if useCache && msg.Role == "user" {
					block.OfText.CacheControl = anthropic.NewCacheControlEphemeralParam()
				}
				blocks = append(blocks, block)
			}
			for _, call := range msg.ToolCalls {
				callID := strings.TrimSpace(call.ID)
				if callID == "" {
					callID = "call_" + strings.ReplaceAll(call.Function.Name, " ", "_")
				}
				blocks = append(blocks, anthropic.NewToolUseBlock(callID, call.Function.Arguments, call.Function.Name))
			}
		}
		if len(blocks) == 0 {
			continue
		}

		role := anthropic.MessageParamRoleUser
		if msg.Role == "assistant" {
			role = anthropic.MessageParamRoleAssistant
		}
		out = append(out, anthropic.MessageParam{Role: role, Content: blocks})
	}
	return system, out
}

func buildAnthropicTools(tools []toolDefinition) []anthropic.ToolUnionParam {
	out := make([]anthropic.ToolUnionParam, 0, len(tools))
	for _, tool := range tools {
		schema := anthropic.ToolInputSchemaParam{}
		if props, ok := tool.Function.Parameters["properties"]; ok {
			schema.Properties = props
		}
		if required, ok := tool.Function.Parameters["required"].([]any); ok {
			names := make([]string, 0, len(required))
			for _, r := range required {
				if s, ok := r.(string); ok {
					names = append(names, s)
				}
			}
			schema.Required = names
		}
		toolParam := anthropic.ToolParam{Name: tool.Function.Name, InputSchema: schema}
		if tool.Function.Description != "" {
			toolParam.Description = param.NewOpt(tool.Function.Description)
		}
		out = append(out, anthropic.ToolUnionParam{OfTool: &toolParam})
	}
	return out
}

func parseAnthropicMessage(resp *anthropic.Message) chatResponse {
	assistant := message{Role: "assistant"}
	textParts := make([]string, 0, 2)
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			if text := strings.TrimSpace(block.Text); text != "" {
				textParts = append(textParts, text)
			}
		case "tool_use":
			assistant.ToolCalls = append(assistant.ToolCalls, toolCall{
				ID:   block.ID,
				Type: "function",
				Function: toolFunctionCall{
					Name:      block.Name,
					Arguments: parseJSONObject(string(block.Input)),
				},
			})
		}
	}
	assistant.Content = strings.TrimSpace(strings.Join(textParts, "\n"))

	return chatResponse{
		Message: assistant,
		Usage: chatUsage{
			InputTokens:  int(resp.Usage.InputTokens),
			OutputTokens: int(resp.Usage.OutputTokens),
			Available:    resp.Usage.InputTokens > 0 || resp.Usage.OutputTokens > 0,
		},
	}
}
