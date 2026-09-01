package main

import (
	"context"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"

	openai "github.com/openai/openai-go/v2"
	"github.com/openai/openai-go/v2/option"
	"github.com/openai/openai-go/v2/packages/param"
	"github.com/openai/openai-go/v2/responses"
	"github.com/openai/openai-go/v2/shared"
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

// isRealOpenAIHost reports whether baseURL targets api.openai.com, the only
// host verified to support the Responses API; every other OpenAI-compatible
// host (Ollama, llama.cpp, LM Studio, etc.) uses Chat Completions instead.
// Indirected so tests can simulate hitting the real OpenAI host.
var isRealOpenAIHost = func(baseURL string) bool {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Hostname(), "api.openai.com")
}

// Send implements sdkProviderAdapter using the official openai-go client: the
// Responses API for api.openai.com, or Chat Completions for every other
// OpenAI-compatible host (Ollama, llama.cpp, LM Studio, etc. — see
// ASH_ALWAYS_OPENAI_API).
func (a openAIAdapter) Send(ctx context.Context, aiCfg aiConfig, messages []message, tools []toolDefinition) (chatResponse, error) {
	client := openai.NewClient(
		option.WithBaseURL(openAIEndpoint(aiCfg.BaseURL)),
		option.WithAPIKey(aiCfg.AuthToken),
		option.WithHTTPClient(newAshHTTPClient()),
		option.WithMaxRetries(0), // retries are handled by ashRoundTripper
	)

	if isRealOpenAIHost(aiCfg.BaseURL) {
		return sendOpenAIResponses(ctx, client, aiCfg, messages, tools)
	}
	return sendOpenAIChatCompletions(ctx, client, aiCfg, messages, tools)
}

func sendOpenAIResponses(ctx context.Context, client openai.Client, aiCfg aiConfig, messages []message, tools []toolDefinition) (chatResponse, error) {
	params := responses.ResponseNewParams{
		Model: aiCfg.Model,
		Input: responses.ResponseNewParamsInputUnion{OfInputItemList: buildOpenAIInput(messages)},
	}
	if len(tools) > 0 {
		params.Tools = buildOpenAITools(tools)
	}

	resp, err := client.Responses.New(ctx, params)
	if err != nil {
		return chatResponse{}, mapOpenAIError(err)
	}
	return parseOpenAIResponse(resp), nil
}

// SendStream implements streamingProviderAdapter. Only the Responses API (real
// api.openai.com) path streams; the Chat Completions fallback used for other
// OpenAI-compatible hosts falls back to a single non-streaming call, since
// streaming support there is unverified/inconsistent across servers.
func (a openAIAdapter) SendStream(ctx context.Context, aiCfg aiConfig, messages []message, tools []toolDefinition, onDelta func(streamDelta)) (chatResponse, error) {
	if !isRealOpenAIHost(aiCfg.BaseURL) {
		return a.Send(ctx, aiCfg, messages, tools)
	}

	client := openai.NewClient(
		option.WithBaseURL(openAIEndpoint(aiCfg.BaseURL)),
		option.WithAPIKey(aiCfg.AuthToken),
		option.WithHTTPClient(newAshHTTPClient()),
		option.WithMaxRetries(0),
	)
	params := responses.ResponseNewParams{
		Model: aiCfg.Model,
		Input: responses.ResponseNewParamsInputUnion{OfInputItemList: buildOpenAIInput(messages)},
	}
	if len(tools) > 0 {
		params.Tools = buildOpenAITools(tools)
	}

	stream := client.Responses.NewStreaming(ctx, params)
	defer func() { _ = stream.Close() }()
	var final *responses.Response
	for stream.Next() {
		event := stream.Current()
		switch event.Type {
		case "response.output_text.delta":
			if onDelta != nil && event.Delta != "" {
				onDelta(streamDelta{TextDelta: event.Delta})
			}
		case "response.completed":
			resp := event.Response
			final = &resp
		}
	}
	if err := stream.Err(); err != nil {
		return chatResponse{}, mapOpenAIError(err)
	}
	if final == nil {
		return chatResponse{}, errors.New("openai stream ended without a completed response")
	}
	return parseOpenAIResponse(final), nil
}

func mapOpenAIError(err error) error {
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		return chatStatusError{StatusCode: apiErr.StatusCode, Body: apiErr.Message}
	}
	return err
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
			if text := strings.TrimSpace(msg.Content); text != "" && len(msg.Attachments) == 0 {
				items = append(items, responses.ResponseInputItemParamOfMessage(text, openAIMessageRole(msg.Role)))
			} else if text := strings.TrimSpace(msg.Content); text != "" || len(msg.Attachments) > 0 {
				content := make(responses.ResponseInputMessageContentListParam, 0, len(msg.Attachments)+1)
				if text != "" {
					content = append(content, responses.ResponseInputContentParamOfInputText(text))
				}
				for _, att := range msg.Attachments {
					content = append(content, openAIAttachmentContentPart(att))
				}
				items = append(items, responses.ResponseInputItemParamOfMessage(content, openAIMessageRole(msg.Role)))
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

// openAIAttachmentContentPart encodes an attachment as a Responses API content part:
// images become input_image parts (inline base64 data URL), everything else becomes
// an input_file part.
func openAIAttachmentContentPart(att attachment) responses.ResponseInputContentUnionParam {
	dataURL := "data:" + att.MimeType + ";base64," + base64.StdEncoding.EncodeToString(att.Data)
	if strings.HasPrefix(att.MimeType, "image/") {
		part := responses.ResponseInputContentParamOfInputImage(responses.ResponseInputImageDetailAuto)
		part.OfInputImage.ImageURL = param.NewOpt(dataURL)
		return part
	}
	var filePart responses.ResponseInputFileParam
	filePart.FileData = param.NewOpt(dataURL)
	if att.FileName != "" {
		filePart.Filename = param.NewOpt(att.FileName)
	}
	return responses.ResponseInputContentUnionParam{OfInputFile: &filePart}
}

// openAIAttachmentChatContentPart encodes an attachment as a Chat Completions content
// part, mirroring openAIAttachmentContentPart.
func openAIAttachmentChatContentPart(att attachment) openai.ChatCompletionContentPartUnionParam {
	dataURL := "data:" + att.MimeType + ";base64," + base64.StdEncoding.EncodeToString(att.Data)
	if strings.HasPrefix(att.MimeType, "image/") {
		return openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{URL: dataURL})
	}
	return openai.FileContentPart(openai.ChatCompletionContentPartFileFileParam{
		FileData: param.NewOpt(dataURL),
		Filename: param.NewOpt(att.FileName),
	})
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

// sendOpenAIChatCompletions is used for every OpenAI-compatible host other
// than api.openai.com (Ollama, llama.cpp, LM Studio, vLLM, etc.), since
// Responses API support outside OpenAI itself is unverified/inconsistent.
func sendOpenAIChatCompletions(ctx context.Context, client openai.Client, aiCfg aiConfig, messages []message, tools []toolDefinition) (chatResponse, error) {
	params := openai.ChatCompletionNewParams{
		Model:    aiCfg.Model,
		Messages: buildOpenAIChatMessages(messages),
	}
	if len(tools) > 0 {
		params.Tools = buildOpenAIChatTools(tools)
	}

	resp, err := client.Chat.Completions.New(ctx, params)
	if err != nil {
		return chatResponse{}, mapOpenAIError(err)
	}
	return parseOpenAIChatCompletion(resp), nil
}

func buildOpenAIChatMessages(messages []message) []openai.ChatCompletionMessageParamUnion {
	out := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages))
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
			out = append(out, openai.ToolMessage(msg.Content, callID))
		case "system":
			if text := strings.TrimSpace(msg.Content); text != "" {
				out = append(out, openai.SystemMessage(text))
			}
		case "assistant":
			if len(msg.ToolCalls) == 0 {
				if text := strings.TrimSpace(msg.Content); text != "" {
					out = append(out, openai.AssistantMessage(text))
				}
				continue
			}
			assistantParam := openai.ChatCompletionAssistantMessageParam{
				Content: openai.ChatCompletionAssistantMessageParamContentUnion{OfString: param.NewOpt(msg.Content)},
			}
			for _, call := range msg.ToolCalls {
				callID := strings.TrimSpace(call.ID)
				if callID == "" {
					callID = "call_" + strings.ReplaceAll(call.Function.Name, " ", "_")
				}
				assistantParam.ToolCalls = append(assistantParam.ToolCalls, openai.ChatCompletionMessageToolCallUnionParam{
					OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
						ID: callID,
						Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
							Name:      call.Function.Name,
							Arguments: marshalJSONObject(call.Function.Arguments),
						},
					},
				})
			}
			out = append(out, openai.ChatCompletionMessageParamUnion{OfAssistant: &assistantParam})
		default:
			text := strings.TrimSpace(msg.Content)
			if len(msg.Attachments) == 0 {
				if text != "" {
					out = append(out, openai.UserMessage(text))
				}
				continue
			}
			parts := make([]openai.ChatCompletionContentPartUnionParam, 0, len(msg.Attachments)+1)
			if text != "" {
				parts = append(parts, openai.TextContentPart(text))
			}
			for _, att := range msg.Attachments {
				parts = append(parts, openAIAttachmentChatContentPart(att))
			}
			if len(parts) > 0 {
				out = append(out, openai.UserMessage(parts))
			}
		}
	}
	return out
}

func buildOpenAIChatTools(tools []toolDefinition) []openai.ChatCompletionToolUnionParam {
	out := make([]openai.ChatCompletionToolUnionParam, 0, len(tools))
	for _, tool := range tools {
		out = append(out, openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
			Name:        tool.Function.Name,
			Description: param.NewOpt(tool.Function.Description),
			Parameters:  tool.Function.Parameters,
		}))
	}
	return out
}

func parseOpenAIChatCompletion(resp *openai.ChatCompletion) chatResponse {
	assistant := message{Role: "assistant"}
	if len(resp.Choices) > 0 {
		choice := resp.Choices[0].Message
		assistant.Content = strings.TrimSpace(choice.Content)
		for _, call := range choice.ToolCalls {
			assistant.ToolCalls = append(assistant.ToolCalls, toolCall{
				ID:   call.ID,
				Type: "function",
				Function: toolFunctionCall{
					Name:      call.Function.Name,
					Arguments: parseJSONObject(call.Function.Arguments),
				},
			})
		}
	}

	return chatResponse{
		Message: assistant,
		Usage: chatUsage{
			InputTokens:  int(resp.Usage.PromptTokens),
			OutputTokens: int(resp.Usage.CompletionTokens),
			Available:    resp.Usage.PromptTokens > 0 || resp.Usage.CompletionTokens > 0,
		},
	}
}
