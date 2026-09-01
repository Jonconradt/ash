package main

import (
	"context"
	"errors"
	"strings"

	openai "github.com/openai/openai-go/v2"
	"github.com/openai/openai-go/v2/option"
	"github.com/openai/openai-go/v2/packages/param"
	"github.com/openai/openai-go/v2/responses"
)

type openAIAdapter struct{}

func (a openAIAdapter) Name() aiProvider {
	return providerOpenAI
}

func (a openAIAdapter) Capabilities() providerCapabilities {
	// OpenAI applies automatic prompt caching server-side for eligible
	// requests; there is no client-controllable caching toggle to send.
	return providerCapabilities{SupportsNativeCaching: true}
}

// openAIEndpoint normalizes baseURL to the SDK's expected `/v1`-rooted base.
func openAIEndpoint(baseURL string) string {
	trimmed := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(trimmed, "/v1") {
		return trimmed
	}
	return trimmed + "/v1"
}

// Send implements sdkProviderAdapter using the official openai-go Responses API client.
func (a openAIAdapter) Send(ctx context.Context, aiCfg aiConfig, messages []message, tools []toolDefinition) (chatResponse, error) {
	client := openai.NewClient(
		option.WithBaseURL(openAIEndpoint(aiCfg.BaseURL)),
		option.WithAPIKey(aiCfg.AuthToken),
		option.WithHTTPClient(newAshHTTPClient()),
		option.WithMaxRetries(0), // retries are handled by ashRoundTripper
	)

	params := responses.ResponseNewParams{
		Model: aiCfg.Model,
		Input: responses.ResponseNewParamsInputUnion{OfInputItemList: buildOpenAIInput(messages)},
	}
	if len(tools) > 0 {
		params.Tools = buildOpenAITools(tools)
	}

	resp, err := client.Responses.New(ctx, params)
	if err != nil {
		var apiErr *openai.Error
		if errors.As(err, &apiErr) {
			return chatResponse{}, chatStatusError{StatusCode: apiErr.StatusCode, Body: apiErr.Message}
		}
		return chatResponse{}, err
	}
	return parseOpenAIResponse(resp), nil
}

func buildOpenAIInput(messages []message) responses.ResponseInputParam {
	items := make(responses.ResponseInputParam, 0, len(messages))
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
			items = append(items, responses.ResponseInputItemParamOfFunctionCallOutput(callID, msg.Content))
		default:
			if text := strings.TrimSpace(msg.Content); text != "" {
				items = append(items, responses.ResponseInputItemParamOfMessage(text, openAIMessageRole(msg.Role)))
			}
			for _, call := range msg.ToolCalls {
				callID := strings.TrimSpace(call.ID)
				if callID == "" {
					callID = "call_" + strings.ReplaceAll(call.Function.Name, " ", "_")
				}
				items = append(items, responses.ResponseInputItemParamOfFunctionCall(marshalJSONObject(call.Function.Arguments), callID, call.Function.Name))
			}
		}
	}
	return items
}

func openAIMessageRole(role string) responses.EasyInputMessageRole {
	switch role {
	case "system":
		return responses.EasyInputMessageRoleSystem
	case "assistant":
		return responses.EasyInputMessageRoleAssistant
	default:
		return responses.EasyInputMessageRoleUser
	}
}

func buildOpenAITools(tools []toolDefinition) []responses.ToolUnionParam {
	out := make([]responses.ToolUnionParam, 0, len(tools))
	for _, tool := range tools {
		fn := responses.ToolParamOfFunction(tool.Function.Name, tool.Function.Parameters, false)
		if tool.Function.Description != "" {
			fn.OfFunction.Description = param.NewOpt(tool.Function.Description)
		}
		out = append(out, fn)
	}
	return out
}

func parseOpenAIResponse(resp *responses.Response) chatResponse {
	assistant := message{Role: "assistant"}
	textParts := make([]string, 0, 2)
	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			for _, content := range item.Content {
				if text := strings.TrimSpace(content.Text); text != "" {
					textParts = append(textParts, text)
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

	return chatResponse{
		Message: assistant,
		Usage: chatUsage{
			InputTokens:  int(resp.Usage.InputTokens),
			OutputTokens: int(resp.Usage.OutputTokens),
			Available:    resp.Usage.InputTokens > 0 || resp.Usage.OutputTokens > 0,
		},
	}
}
