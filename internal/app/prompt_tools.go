// Copyright (c) 2026 Jonathan L. Conradt
// SPDX-License-Identifier: MIT

package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// promptToolsMode selects how ash behaves when the backend rejects the native
// tools parameter (e.g. Ollama answering 400 "does not support tools" for a
// model without a registered tool template).
type promptToolsMode int

const (
	// promptToolsAuto retries once without native tools after a rejection,
	// injecting the tool catalog into the prompt instead.
	promptToolsAuto promptToolsMode = iota
	// promptToolsForce always uses prompt-based tool calls, never sending the
	// native tools parameter.
	promptToolsForce
	// promptToolsOff never falls back; the rejection surfaces as an error.
	promptToolsOff
)

// configuredPromptToolsMode reads ASH_PROMPT_TOOLS: force/always/on enables
// prompt-based tool calls unconditionally, off/never/false disables the
// fallback entirely, and anything else (the default) falls back automatically
// when the backend rejects native tool calling.
func configuredPromptToolsMode() promptToolsMode {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ASH_PROMPT_TOOLS"))) {
	case "1", "true", "yes", "y", "on", "always", "force":
		return promptToolsForce
	case "0", "false", "no", "n", "off", "never", "disabled":
		return promptToolsOff
	default:
		return promptToolsAuto
	}
}

// isToolsUnsupportedError reports whether err is the backend rejecting native
// tool calling for the configured model. Ollama returns HTTP 400 with a body
// containing "does not support tools" on both its native /api/chat endpoint
// and its OpenAI-compatible /v1/chat/completions endpoint; the detection keys
// on that error text rather than on any model-name list.
func isToolsUnsupportedError(err error) bool {
	if err == nil {
		return false
	}
	detail := err.Error()
	if statusErr, ok := errors.AsType[chatStatusError](err); ok {
		detail = statusErr.Body
	}
	return strings.Contains(strings.ToLower(detail), "does not support tools")
}

// promptToolCallFence opens a prompt-based tool call block. The format is
// deliberately distinct from normal prose so call-like text outside a fenced
// block is never executed.
const promptToolCallFence = "```tool_call"

// buildPromptToolCatalogMessage renders the native tool definitions as a
// system message for backends without native tool calling: the catalog of
// names, descriptions, and argument schemas, plus the single machine-parseable
// call format parsePromptToolCalls understands.
func buildPromptToolCatalogMessage(tools []toolDefinition) message {
	var b strings.Builder
	b.WriteString("This server does not support native tool calling, so tool calls are exchanged as plain text.\n")
	b.WriteString("Available tools:\n")
	for _, tool := range tools {
		fmt.Fprintf(&b, "- %s: %s\n", tool.Function.Name, tool.Function.Description)
		if len(tool.Function.Parameters) > 0 {
			if schema, err := json.Marshal(tool.Function.Parameters); err == nil {
				fmt.Fprintf(&b, "  arguments JSON schema: %s\n", schema)
			}
		}
	}
	b.WriteString("To call a tool, reply with one fenced block per call, exactly in this format:\n")
	b.WriteString(promptToolCallFence + "\n")
	b.WriteString(`{"name": "<tool_name>", "arguments": {}}` + "\n")
	b.WriteString("```\n")
	b.WriteString("The block body must be a single JSON object with a listed tool name and arguments matching that tool's schema. ")
	b.WriteString("If you cannot produce the fenced block, a single bare JSON object in that same format is also accepted. ")
	b.WriteString("After each call you will receive a message beginning with \"Tool result for\" containing the output; use it to continue. ")
	b.WriteString("If no tool is needed, answer normally without any tool_call block.")
	return message{Role: "system", Content: b.String()}
}

// parsePromptToolCalls extracts prompt-based tool calls from an assistant
// reply. A fenced block opened by a line equal to ```tool_call and closed by
// a ``` line, or a reply consisting only of one JSON object, parses as a call
// only when it names a known tool. Malformed JSON, unknown tool names, and
// call-like text inside ordinary prose are left as plain text and never
// executed. The returned calls carry the same tool names and argument objects
// native tool calling would, so they pass through the identical validation
// and allowlist enforcement downstream.
func parsePromptToolCalls(content string, tools []toolDefinition) []toolCall {
	known := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		known[tool.Function.Name] = struct{}{}
	}
	if !strings.Contains(content, promptToolCallFence) {
		if call, ok := parsePromptToolCallBody(strings.TrimSpace(content), known); ok {
			return []toolCall{call}
		}
		return nil
	}

	var calls []toolCall
	lines := strings.Split(content, "\n")
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != promptToolCallFence {
			continue
		}
		body := make([]string, 0, 4)
		closed := false
		end := i + 1
		for ; end < len(lines); end++ {
			if strings.TrimSpace(lines[end]) == "```" {
				closed = true
				break
			}
			body = append(body, lines[end])
		}
		if !closed {
			break
		}
		i = end

		if call, ok := parsePromptToolCallBody(strings.TrimSpace(strings.Join(body, "\n")), known); ok {
			calls = append(calls, call)
		}
	}
	return calls
}

func parsePromptToolCallBody(body string, known map[string]struct{}) (toolCall, bool) {
	var parsed struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return toolCall{}, false
	}
	name := strings.TrimSpace(parsed.Name)
	if _, ok := known[name]; !ok {
		return toolCall{}, false
	}
	args := parsed.Arguments
	if args == nil {
		args = map[string]any{}
	}
	return toolCall{
		Type:     "function",
		Function: toolFunctionCall{Name: name, Arguments: args},
	}, true
}

// promptToolResultMessage wraps a tool result for a backend without native
// tool calling. The role is "user" rather than "tool" because servers that
// reject the tools parameter may also mishandle tool-role messages.
func promptToolResultMessage(toolName, content string) message {
	return message{Role: "user", Content: fmt.Sprintf("Tool result for %q:\n%s", toolName, content)}
}
