package main

import (
	"context"
	"errors"
	"strings"

	cohere "github.com/cohere-ai/cohere-go/v2"
	cohereclient "github.com/cohere-ai/cohere-go/v2/client"
	"github.com/cohere-ai/cohere-go/v2/core"
)

type cohereAdapter struct{}

func (a cohereAdapter) Name() aiProvider {
	return providerCohere
}

func (a cohereAdapter) Capabilities() providerCapabilities {
	return providerCapabilities{SupportsNativeCaching: false}
}

// Send implements sdkProviderAdapter using the official cohere-go/v2 V2 Chat client.
func (a cohereAdapter) Send(ctx context.Context, aiCfg aiConfig, messages []message, tools []toolDefinition) (chatResponse, error) {
	client := cohereclient.NewClient(
		cohereclient.WithToken(aiCfg.AuthToken),
		cohereclient.WithBaseURL(strings.TrimRight(aiCfg.BaseURL, "/")),
		cohereclient.WithHTTPClient(newAshHTTPClient()),
	)

	request := &cohere.V2ChatRequest{
		Model:    aiCfg.Model,
		Messages: buildCohereMessages(messages),
	}
	if len(tools) > 0 {
		request.Tools = buildCohereTools(tools)
	}

	resp, err := client.V2.Chat(ctx, request)
	if err != nil {
		var apiErr *core.APIError
		if errors.As(err, &apiErr) {
			return chatResponse{}, chatStatusError{StatusCode: apiErr.StatusCode, Body: apiErr.Error()}
		}
		return chatResponse{}, err
	}
	return parseCohereResponse(resp), nil
}

func buildCohereMessages(messages []message) cohere.ChatMessages {
	out := make(cohere.ChatMessages, 0, len(messages))
	for _, msg := range messages {
		switch msg.Role {
		case "system":
			if text := strings.TrimSpace(msg.Content); text != "" {
				out = append(out, &cohere.ChatMessageV2{
					Role:   "system",
					System: &cohere.SystemMessageV2{Content: &cohere.SystemMessageV2Content{String: text}},
				})
			}
		case "tool":
			callID := strings.TrimSpace(msg.ToolCallID)
			if callID == "" {
				callID = strings.TrimSpace(msg.ToolName)
			}
			if callID == "" {
				continue
			}
			out = append(out, &cohere.ChatMessageV2{
				Role: "tool",
				Tool: &cohere.ToolMessageV2{
					ToolCallId: callID,
					Content:    &cohere.ToolMessageV2Content{String: msg.Content},
				},
			})
		case "assistant":
			assistant := &cohere.AssistantMessage{}
			if text := strings.TrimSpace(msg.Content); text != "" {
				assistant.Content = &cohere.AssistantMessageV2Content{String: text}
			}
			for _, call := range msg.ToolCalls {
				callID := strings.TrimSpace(call.ID)
				if callID == "" {
					callID = "call_" + strings.ReplaceAll(call.Function.Name, " ", "_")
				}
				name := call.Function.Name
				arguments := marshalJSONObject(call.Function.Arguments)
				assistant.ToolCalls = append(assistant.ToolCalls, &cohere.ToolCallV2{
					Id:       callID,
					Function: &cohere.ToolCallV2Function{Name: &name, Arguments: &arguments},
				})
			}
			out = append(out, &cohere.ChatMessageV2{Role: "assistant", Assistant: assistant})
		default:
			if text := strings.TrimSpace(msg.Content); text != "" {
				out = append(out, &cohere.ChatMessageV2{
					Role: "user",
					User: &cohere.UserMessageV2{Content: &cohere.UserMessageV2Content{String: text}},
				})
			}
		}
	}
	return out
}

func buildCohereTools(tools []toolDefinition) []*cohere.ToolV2 {
	out := make([]*cohere.ToolV2, 0, len(tools))
	for _, tool := range tools {
		fn := &cohere.ToolV2Function{
			Name:       tool.Function.Name,
			Parameters: tool.Function.Parameters,
		}
		if tool.Function.Description != "" {
			description := tool.Function.Description
			fn.Description = &description
		}
		out = append(out, &cohere.ToolV2{Function: fn})
	}
	return out
}

func parseCohereResponse(resp *cohere.V2ChatResponse) chatResponse {
	assistant := message{Role: "assistant"}
	if resp.Message != nil {
		textParts := make([]string, 0, 2)
		for _, item := range resp.Message.Content {
			if item.Text != nil {
				if text := strings.TrimSpace(item.Text.Text); text != "" {
					textParts = append(textParts, text)
				}
			}
		}
		assistant.Content = strings.TrimSpace(strings.Join(textParts, "\n"))
		for _, call := range resp.Message.ToolCalls {
			toolCallOut := toolCall{ID: call.Id, Type: "function"}
			if call.Function != nil {
				if call.Function.Name != nil {
					toolCallOut.Function.Name = *call.Function.Name
				}
				if call.Function.Arguments != nil {
					toolCallOut.Function.Arguments = parseJSONObject(*call.Function.Arguments)
				}
			}
			assistant.ToolCalls = append(assistant.ToolCalls, toolCallOut)
		}
	}

	result := chatResponse{Message: assistant}
	if resp.Usage != nil && resp.Usage.Tokens != nil {
		input := 0
		output := 0
		if resp.Usage.Tokens.InputTokens != nil {
			input = int(*resp.Usage.Tokens.InputTokens)
		}
		if resp.Usage.Tokens.OutputTokens != nil {
			output = int(*resp.Usage.Tokens.OutputTokens)
		}
		result.Usage = chatUsage{
			InputTokens:  input,
			OutputTokens: output,
			Available:    input > 0 || output > 0,
		}
	}
	return result
}
