package app

import (
	"context"
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

type bedrockAdapter struct{}

func (a bedrockAdapter) Name() aiProvider {
	return providerBedrock
}

func (a bedrockAdapter) Capabilities() providerCapabilities {
	return providerCapabilities{SupportsNativeCaching: false}
}

// Send implements sdkProviderAdapter using the official AWS SDK v2 Bedrock Runtime
// Converse API. Unlike every other provider, Bedrock authenticates via AWS SigV4
// through the standard AWS credential chain (environment variables, ~/.aws/credentials,
// SSO, instance role, etc.) rather than a bearer token; aiCfg.AuthToken/Authorization
// are not used here — AI_AUTH_TOKEN only needs to be set to satisfy ash's generic
// cloud-endpoint validation and is otherwise ignored for this provider.
func (a bedrockAdapter) Send(ctx context.Context, aiCfg aiConfig, messages []message, tools []toolDefinition) (chatResponse, error) {
	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithHTTPClient(newAshHTTPClient()))
	if err != nil {
		return chatResponse{}, err
	}

	client := bedrockruntime.NewFromConfig(awsCfg, func(o *bedrockruntime.Options) {
		if aiCfg.BaseURL != "" {
			endpoint := strings.TrimRight(aiCfg.BaseURL, "/")
			o.BaseEndpoint = &endpoint
		}
	})

	system, msgs := buildBedrockMessages(messages)
	input := &bedrockruntime.ConverseInput{
		ModelId:  aws.String(aiCfg.Model),
		Messages: msgs,
		System:   system,
	}
	if len(tools) > 0 {
		input.ToolConfig = &types.ToolConfiguration{Tools: buildBedrockTools(tools)}
	}

	resp, err := client.Converse(ctx, input)
	if err != nil {
		var respErr *smithyhttp.ResponseError
		if errors.As(err, &respErr) {
			return chatResponse{}, chatStatusError{StatusCode: respErr.HTTPStatusCode(), Body: respErr.Err.Error()}
		}
		return chatResponse{}, err
	}
	return parseBedrockResponse(resp), nil
}

func buildBedrockMessages(messages []message) ([]types.SystemContentBlock, []types.Message) {
	system := make([]types.SystemContentBlock, 0, 1)
	out := make([]types.Message, 0, len(messages))

	for _, msg := range messages {
		if msg.Role == "system" {
			if text := strings.TrimSpace(msg.Content); text != "" {
				system = append(system, &types.SystemContentBlockMemberText{Value: text})
			}
			continue
		}

		blocks := make([]types.ContentBlock, 0, 2)
		role := types.ConversationRoleUser
		switch msg.Role {
		case "tool":
			callID := strings.TrimSpace(msg.ToolCallID)
			if callID == "" {
				callID = strings.TrimSpace(msg.ToolName)
			}
			if callID == "" {
				continue
			}
			blocks = append(blocks, &types.ContentBlockMemberToolResult{Value: types.ToolResultBlock{
				ToolUseId: aws.String(callID),
				Content:   []types.ToolResultContentBlock{&types.ToolResultContentBlockMemberText{Value: msg.Content}},
			}})
		case "assistant":
			role = types.ConversationRoleAssistant
			if text := strings.TrimSpace(msg.Content); text != "" {
				blocks = append(blocks, &types.ContentBlockMemberText{Value: text})
			}
			for _, call := range msg.ToolCalls {
				callID := strings.TrimSpace(call.ID)
				if callID == "" {
					callID = "call_" + strings.ReplaceAll(call.Function.Name, " ", "_")
				}
				blocks = append(blocks, &types.ContentBlockMemberToolUse{Value: types.ToolUseBlock{
					ToolUseId: aws.String(callID),
					Name:      aws.String(call.Function.Name),
					Input:     document.NewLazyDocument(call.Function.Arguments),
				}})
			}
		default:
			if text := strings.TrimSpace(msg.Content); text != "" {
				blocks = append(blocks, &types.ContentBlockMemberText{Value: text})
			}
		}
		if len(blocks) == 0 {
			continue
		}
		out = append(out, types.Message{Role: role, Content: blocks})
	}
	return system, out
}

func buildBedrockTools(tools []toolDefinition) []types.Tool {
	out := make([]types.Tool, 0, len(tools))
	for _, tool := range tools {
		spec := types.ToolSpecification{
			Name:        aws.String(tool.Function.Name),
			InputSchema: &types.ToolInputSchemaMemberJson{Value: document.NewLazyDocument(tool.Function.Parameters)},
		}
		if tool.Function.Description != "" {
			spec.Description = aws.String(tool.Function.Description)
		}
		out = append(out, &types.ToolMemberToolSpec{Value: spec})
	}
	return out
}

func parseBedrockResponse(resp *bedrockruntime.ConverseOutput) chatResponse {
	assistant := message{Role: "assistant"}
	if outputMessage, ok := resp.Output.(*types.ConverseOutputMemberMessage); ok {
		textParts := make([]string, 0, 2)
		for _, block := range outputMessage.Value.Content {
			switch b := block.(type) {
			case *types.ContentBlockMemberText:
				if text := strings.TrimSpace(b.Value); text != "" {
					textParts = append(textParts, text)
				}
			case *types.ContentBlockMemberToolUse:
				args := map[string]any{}
				_ = b.Value.Input.UnmarshalSmithyDocument(&args)
				assistant.ToolCalls = append(assistant.ToolCalls, toolCall{
					ID:   aws.ToString(b.Value.ToolUseId),
					Type: "function",
					Function: toolFunctionCall{
						Name:      aws.ToString(b.Value.Name),
						Arguments: args,
					},
				})
			}
		}
		assistant.Content = strings.TrimSpace(strings.Join(textParts, "\n"))
	}

	result := chatResponse{Message: assistant}
	if resp.Usage != nil {
		input := int(aws.ToInt32(resp.Usage.InputTokens))
		output := int(aws.ToInt32(resp.Usage.OutputTokens))
		result.Usage = chatUsage{
			InputTokens:  input,
			OutputTokens: output,
			Available:    input > 0 || output > 0,
		}
	}
	return result
}
