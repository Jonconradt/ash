package main

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/genai"
)

type googleAdapter struct{}

func (a googleAdapter) Name() aiProvider {
	return providerGoogle
}

func (a googleAdapter) Capabilities() providerCapabilities {
	// Gemini's explicit context-caching API (CachedContent) requires creating
	// and managing a separate cache resource; not wired up yet, so this
	// deliberately reports no native caching rather than a no-op placeholder.
	return providerCapabilities{SupportsNativeCaching: false}
}

// Send implements sdkProviderAdapter using the official google.golang.org/genai client.
func (a googleAdapter) Send(ctx context.Context, aiCfg aiConfig, messages []message, tools []toolDefinition) (chatResponse, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:      aiCfg.AuthToken,
		Backend:     genai.BackendGeminiAPI,
		HTTPClient:  newAshHTTPClient(),
		HTTPOptions: genai.HTTPOptions{BaseURL: aiCfg.BaseURL},
	})
	if err != nil {
		return chatResponse{}, err
	}

	config := &genai.GenerateContentConfig{}
	contents := make([]*genai.Content, 0, len(messages))
	for _, msg := range messages {
		switch msg.Role {
		case "system":
			if text := strings.TrimSpace(msg.Content); text != "" {
				config.SystemInstruction = &genai.Content{Parts: []*genai.Part{genai.NewPartFromText(text)}}
			}
		case "tool":
			name := strings.TrimSpace(msg.ToolName)
			if name == "" {
				name = strings.TrimSpace(msg.ToolCallID)
			}
			if name == "" {
				continue
			}
			contents = append(contents, genai.NewContentFromFunctionResponse(name, map[string]any{"result": msg.Content}, genai.RoleUser))
		default:
			if text := strings.TrimSpace(msg.Content); text != "" {
				contents = append(contents, genai.NewContentFromText(text, googleContentRole(msg.Role)))
			}
			for _, call := range msg.ToolCalls {
				contents = append(contents, genai.NewContentFromFunctionCall(call.Function.Name, call.Function.Arguments, genai.RoleModel))
			}
		}
	}

	if len(tools) > 0 {
		declarations := make([]*genai.FunctionDeclaration, 0, len(tools))
		for _, tool := range tools {
			declarations = append(declarations, &genai.FunctionDeclaration{
				Name:                 tool.Function.Name,
				Description:          tool.Function.Description,
				ParametersJsonSchema: tool.Function.Parameters,
			})
		}
		config.Tools = []*genai.Tool{{FunctionDeclarations: declarations}}
	}

	resp, err := client.Models.GenerateContent(ctx, aiCfg.Model, contents, config)
	if err != nil {
		var apiErr genai.APIError
		if errors.As(err, &apiErr) {
			return chatResponse{}, chatStatusError{StatusCode: apiErr.Code, Body: apiErr.Message}
		}
		return chatResponse{}, err
	}

	assistant := parseGoogleResponse(resp)

	result := chatResponse{Message: assistant}
	if resp.UsageMetadata != nil {
		result.Usage = chatUsage{
			InputTokens:  int(resp.UsageMetadata.PromptTokenCount),
			OutputTokens: int(resp.UsageMetadata.CandidatesTokenCount),
			Available:    resp.UsageMetadata.PromptTokenCount > 0 || resp.UsageMetadata.CandidatesTokenCount > 0,
		}
	}
	return result, nil
}

// parseGoogleResponse extracts text and function calls directly from the response's
// candidate parts rather than using the SDK's Text()/FunctionCalls() helpers, which
// log an unsolicited warning to stderr via the standard log package whenever a
// response mixes text and function-call parts (the common case for tool-using turns).
func parseGoogleResponse(resp *genai.GenerateContentResponse) message {
	assistant := message{Role: "assistant"}
	if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
		return assistant
	}
	textParts := make([]string, 0, 2)
	for _, part := range resp.Candidates[0].Content.Parts {
		if part == nil {
			continue
		}
		if text := strings.TrimSpace(part.Text); text != "" {
			textParts = append(textParts, text)
		}
		if part.FunctionCall != nil {
			assistant.ToolCalls = append(assistant.ToolCalls, toolCall{
				ID:   part.FunctionCall.ID,
				Type: "function",
				Function: toolFunctionCall{
					Name:      part.FunctionCall.Name,
					Arguments: part.FunctionCall.Args,
				},
			})
		}
	}
	assistant.Content = strings.TrimSpace(strings.Join(textParts, "\n"))
	return assistant
}

func googleContentRole(role string) genai.Role {
	if role == "assistant" {
		return genai.RoleModel
	}
	return genai.RoleUser
}
