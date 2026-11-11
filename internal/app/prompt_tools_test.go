// Copyright (c) 2026 Jonathan L. Conradt
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Jonconradt/ash/internal/brokerproto"
)

func TestConfiguredPromptToolsMode(t *testing.T) {
	tests := []struct {
		value string
		want  promptToolsMode
	}{
		{"", promptToolsAuto},
		{"auto", promptToolsAuto},
		{"garbage", promptToolsAuto},
		{"force", promptToolsForce},
		{"always", promptToolsForce},
		{"1", promptToolsForce},
		{"on", promptToolsForce},
		{"off", promptToolsOff},
		{"never", promptToolsOff},
		{"0", promptToolsOff},
		{"FALSE", promptToolsOff},
	}
	for _, test := range tests {
		t.Setenv("ASH_PROMPT_TOOLS", test.value)
		if got := configuredPromptToolsMode(); got != test.want {
			t.Fatalf("ASH_PROMPT_TOOLS=%q: got mode %v, want %v", test.value, got, test.want)
		}
	}
}

func TestIsToolsUnsupportedError(t *testing.T) {
	ollamaNative := chatStatusError{StatusCode: 400, Body: `{"error":"registry.ollama.ai/library/gemma4:latest does not support tools"}`}
	if !isToolsUnsupportedError(ollamaNative) {
		t.Fatalf("expected native Ollama 400 body to be detected")
	}
	openAICompat := chatStatusError{StatusCode: 400, Body: "registry.ollama.ai/library/gemma4:latest does not support tools"}
	if !isToolsUnsupportedError(openAICompat) {
		t.Fatalf("expected OpenAI-compatible 400 message to be detected")
	}
	if !isToolsUnsupportedError(mustError(t, "model Does Not Support Tools")) {
		t.Fatalf("expected plain model error text to be detected case-insensitively")
	}
	if isToolsUnsupportedError(nil) {
		t.Fatalf("nil error must not be detected")
	}
	if isToolsUnsupportedError(chatStatusError{StatusCode: 400, Body: "invalid request"}) {
		t.Fatalf("unrelated 400 must not be detected")
	}
}

func mustError(t *testing.T, text string) error {
	t.Helper()
	return &textError{text: text}
}

type textError struct{ text string }

func (e *textError) Error() string { return e.text }

func TestBuildPromptToolCatalogMessage(t *testing.T) {
	tools := []toolDefinition{{
		Type: "function",
		Function: toolFunctionDefinition{
			Name:        "run_unix_command",
			Description: "run an allowlisted executable",
			Parameters:  map[string]any{"type": "object", "required": []string{"command"}},
		},
	}}
	msg := buildPromptToolCatalogMessage(tools)
	if msg.Role != "user" {
		t.Fatalf("expected user role, got %q", msg.Role)
	}
	for _, want := range []string{"run_unix_command", "run an allowlisted executable", promptToolCallFence, `"required":["command"]`} {
		if !strings.Contains(msg.Content, want) {
			t.Fatalf("catalog message missing %q:\n%s", want, msg.Content)
		}
	}
}

func TestParsePromptToolCalls(t *testing.T) {
	tools := []toolDefinition{
		{Type: "function", Function: toolFunctionDefinition{Name: "run_unix_command"}},
		{Type: "function", Function: toolFunctionDefinition{Name: "what_time_is_it"}},
	}

	t.Run("valid call", func(t *testing.T) {
		content := "Let me check.\n```tool_call\n{\"name\": \"run_unix_command\", \"arguments\": {\"command\": \"date\"}}\n```\n"
		calls := parsePromptToolCalls(content, tools)
		if len(calls) != 1 {
			t.Fatalf("expected one call, got %#v", calls)
		}
		if calls[0].Function.Name != "run_unix_command" || calls[0].Function.Arguments["command"] != "date" {
			t.Fatalf("unexpected call: %#v", calls[0])
		}
	})

	t.Run("bare JSON call", func(t *testing.T) {
		content := `{"name": "run_unix_command", "arguments": {"command": "date"}}`
		calls := parsePromptToolCalls(content, tools)
		if len(calls) != 1 || calls[0].Function.Name != "run_unix_command" || calls[0].Function.Arguments["command"] != "date" {
			t.Fatalf("expected one parsed bare JSON call, got %#v", calls)
		}
	})

	t.Run("missing arguments defaults to empty object", func(t *testing.T) {
		content := "```tool_call\n{\"name\": \"what_time_is_it\"}\n```"
		calls := parsePromptToolCalls(content, tools)
		if len(calls) != 1 || calls[0].Function.Arguments == nil || len(calls[0].Function.Arguments) != 0 {
			t.Fatalf("expected one call with empty arguments, got %#v", calls)
		}
	})

	t.Run("bare JSON surrounded by prose is not executed", func(t *testing.T) {
		content := `Try this: {"name": "run_unix_command", "arguments": {"command": "date"}}`
		if calls := parsePromptToolCalls(content, tools); len(calls) != 0 {
			t.Fatalf("expected no calls from prose, got %#v", calls)
		}
	})

	t.Run("malformed JSON is not executed", func(t *testing.T) {
		content := "```tool_call\n{\"name\": \"run_unix_command\", \"arguments\": {broken\n```"
		if calls := parsePromptToolCalls(content, tools); len(calls) != 0 {
			t.Fatalf("expected no calls for malformed JSON, got %#v", calls)
		}
	})

	t.Run("unknown tool name is not executed", func(t *testing.T) {
		content := "```tool_call\n{\"name\": \"rm_everything\", \"arguments\": {}}\n```"
		if calls := parsePromptToolCalls(content, tools); len(calls) != 0 {
			t.Fatalf("expected no calls for unknown tool, got %#v", calls)
		}
	})

	t.Run("multiple calls parse in order", func(t *testing.T) {
		content := "```tool_call\n{\"name\": \"what_time_is_it\", \"arguments\": {}}\n```\nthen\n```tool_call\n{\"name\": \"run_unix_command\", \"arguments\": {\"command\": \"ls\"}}\n```"
		calls := parsePromptToolCalls(content, tools)
		if len(calls) != 2 || calls[0].Function.Name != "what_time_is_it" || calls[1].Function.Name != "run_unix_command" {
			t.Fatalf("expected two ordered calls, got %#v", calls)
		}
	})

	t.Run("call-like prose outside a fence is not executed", func(t *testing.T) {
		content := `You could write {"name": "run_unix_command", "arguments": {"command": "ls"}} or use a ` + "```tool_call" + ` block when needed.`
		if calls := parsePromptToolCalls(content, tools); len(calls) != 0 {
			t.Fatalf("expected no calls from prose, got %#v", calls)
		}
	})

	t.Run("unclosed fence is not executed", func(t *testing.T) {
		content := "```tool_call\n{\"name\": \"run_unix_command\", \"arguments\": {}}"
		if calls := parsePromptToolCalls(content, tools); len(calls) != 0 {
			t.Fatalf("expected no calls from unclosed fence, got %#v", calls)
		}
	})
}

type recordingToolShim struct {
	tools  []toolDefinition
	calls  []string
	result string
}

func (s *recordingToolShim) ListTools() []toolDefinition { return s.tools }
func (s *recordingToolShim) CallTool(_ context.Context, name string, _ map[string]any) string {
	s.calls = append(s.calls, name)
	return s.result
}

func timeToolShim() *recordingToolShim {
	return &recordingToolShim{
		tools: []toolDefinition{{
			Type: "function",
			Function: toolFunctionDefinition{
				Name:        "what_time_is_it",
				Description: "report the current time",
				Parameters:  map[string]any{"type": "object"},
			},
		}},
		result: `{"ok":true,"command":"what_time_is_it","exit_code":0,"stdout":"12:00"}`,
	}
}

func toolsUnsupportedStatusError() chatStatusError {
	return chatStatusError{StatusCode: 400, Body: `{"error":"registry.ollama.ai/library/gemma4:latest does not support tools"}`}
}

func TestRunToolLoopPromptToolsFallback(t *testing.T) {
	originalChatStreamExecutor := chatStreamExecutor
	t.Cleanup(func() { chatStreamExecutor = originalChatStreamExecutor })
	t.Setenv("ASH_PROMPT_TOOLS", "")

	shim := timeToolShim()
	calls := 0
	chatStreamExecutor = func(_ context.Context, _ aiConfig, msgs []message, tools []toolDefinition, _ func(streamDelta)) (chatResponse, error) {
		calls++
		switch calls {
		case 1:
			if len(tools) == 0 {
				t.Fatalf("first attempt must send native tools")
			}
			return chatResponse{}, toolsUnsupportedStatusError()
		case 2:
			if len(tools) != 0 {
				t.Fatalf("fallback retry must not send native tools, got %#v", tools)
			}
			foundCatalog := false
			for _, msg := range msgs {
				if msg.Role == "user" && strings.Contains(msg.Content, promptToolCallFence) {
					foundCatalog = true
				}
			}
			if !foundCatalog {
				t.Fatalf("fallback retry must carry the prompt tool catalog")
			}
			return chatResponse{Message: message{Role: "assistant", Content: `{"name": "what_time_is_it", "arguments": {}}`}}, nil
		default:
			// The tool result is a user-role message, but it is no longer
			// necessarily the last user message: the per-round injections
			// (execution state, injection defense, tool catalog) are now
			// user-role messages too and are appended after it in the
			// payload, so look for the tool result anywhere.
			sawToolResult := false
			for _, msg := range msgs {
				if msg.Role == "user" && strings.Contains(msg.Content, "Tool result for") && strings.Contains(msg.Content, "12:00") {
					sawToolResult = true
					break
				}
			}
			if !sawToolResult {
				t.Fatalf("expected the tool result as a user message, got %#v", msgs)
			}
			for _, msg := range msgs {
				if msg.Role == "tool" || len(msg.ToolCalls) > 0 {
					t.Fatalf("prompt mode must not replay wire-level tool messages, got %#v", msg)
				}
			}
			return chatResponse{Message: message{Role: "assistant", Content: "It is 12:00."}}, nil
		}
	}

	final, _, err := runToolLoop(context.Background(), testAIConfig("http://example.invalid", "gemma4:latest"), "what time is it?", []message{{Role: "user", Content: "what time is it?"}}, shim)
	if err != nil {
		t.Fatalf("runToolLoop returned error: %v", err)
	}
	if final != "It is 12:00." {
		t.Fatalf("unexpected final answer: %q", final)
	}
	if calls != 3 {
		t.Fatalf("expected 3 chat calls, got %d", calls)
	}
	if len(shim.calls) != 1 || shim.calls[0] != "what_time_is_it" {
		t.Fatalf("expected one what_time_is_it call, got %#v", shim.calls)
	}
}

func TestRunToolLoopPromptToolsFallbackDisabled(t *testing.T) {
	originalChatStreamExecutor := chatStreamExecutor
	t.Cleanup(func() { chatStreamExecutor = originalChatStreamExecutor })
	t.Setenv("ASH_PROMPT_TOOLS", "off")

	calls := 0
	chatStreamExecutor = func(context.Context, aiConfig, []message, []toolDefinition, func(streamDelta)) (chatResponse, error) {
		calls++
		return chatResponse{}, toolsUnsupportedStatusError()
	}

	_, _, err := runToolLoop(context.Background(), testAIConfig("http://example.invalid", "gemma4:latest"), "what time is it?", []message{{Role: "user", Content: "what time is it?"}}, timeToolShim())
	if err == nil || !isToolsUnsupportedError(err) {
		t.Fatalf("expected the rejection to surface when the fallback is off, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected a single chat call with the fallback off, got %d", calls)
	}
}

func TestRunToolLoopPromptToolsFallbackDoesNotLoopOnRepeatedRejection(t *testing.T) {
	originalChatStreamExecutor := chatStreamExecutor
	t.Cleanup(func() { chatStreamExecutor = originalChatStreamExecutor })
	t.Setenv("ASH_PROMPT_TOOLS", "")

	calls := 0
	chatStreamExecutor = func(context.Context, aiConfig, []message, []toolDefinition, func(streamDelta)) (chatResponse, error) {
		calls++
		return chatResponse{}, toolsUnsupportedStatusError()
	}

	_, _, err := runToolLoop(context.Background(), testAIConfig("http://example.invalid", "gemma4:latest"), "what time is it?", []message{{Role: "user", Content: "what time is it?"}}, timeToolShim())
	if err == nil {
		t.Fatalf("expected a repeated rejection to surface as an error")
	}
	if calls != 2 {
		t.Fatalf("expected exactly one fallback retry, got %d calls", calls)
	}
}

func TestRunToolLoopPromptToolsForcedPlainAnswer(t *testing.T) {
	originalChatStreamExecutor := chatStreamExecutor
	t.Cleanup(func() { chatStreamExecutor = originalChatStreamExecutor })
	t.Setenv("ASH_PROMPT_TOOLS", "force")

	calls := 0
	chatStreamExecutor = func(_ context.Context, _ aiConfig, msgs []message, tools []toolDefinition, _ func(streamDelta)) (chatResponse, error) {
		calls++
		if len(tools) != 0 {
			t.Fatalf("forced prompt mode must never send native tools, got %#v", tools)
		}
		foundCatalog := false
		for _, msg := range msgs {
			if msg.Role == "user" && strings.Contains(msg.Content, promptToolCallFence) {
				foundCatalog = true
			}
		}
		if !foundCatalog {
			t.Fatalf("forced prompt mode must carry the prompt tool catalog")
		}
		return chatResponse{Message: message{Role: "assistant", Content: "No tool needed; hello!"}}, nil
	}

	final, _, err := runToolLoop(context.Background(), testAIConfig("http://example.invalid", "gemma4:latest"), "say hello", []message{{Role: "user", Content: "say hello"}}, timeToolShim())
	if err != nil {
		t.Fatalf("runToolLoop returned error: %v", err)
	}
	if final != "No tool needed; hello!" {
		t.Fatalf("expected the plain answer back, got %q", final)
	}
	if calls != 1 {
		t.Fatalf("expected a single chat call, got %d", calls)
	}
}

func TestRunToolLoopNativeToolsUnchangedOnSuccess(t *testing.T) {
	originalChatStreamExecutor := chatStreamExecutor
	t.Cleanup(func() { chatStreamExecutor = originalChatStreamExecutor })
	t.Setenv("ASH_PROMPT_TOOLS", "")

	shim := timeToolShim()
	calls := 0
	chatStreamExecutor = func(_ context.Context, _ aiConfig, msgs []message, tools []toolDefinition, _ func(streamDelta)) (chatResponse, error) {
		calls++
		if len(tools) == 0 {
			t.Fatalf("native tool calling must keep sending tools, got none on call %d", calls)
		}
		for _, msg := range msgs {
			if msg.Role == "user" && strings.Contains(msg.Content, promptToolCallFence) {
				t.Fatalf("native tool calling must not inject the prompt tool catalog")
			}
		}
		if calls == 1 {
			return chatResponse{Message: message{Role: "assistant", ToolCalls: []toolCall{{Type: "function", Function: toolFunctionCall{Name: "what_time_is_it", Arguments: map[string]any{}}}}}}, nil
		}
		return chatResponse{Message: message{Role: "assistant", Content: "It is 12:00."}}, nil
	}

	final, updated, err := runToolLoop(context.Background(), testAIConfig("http://example.invalid", "llama3.1"), "what time is it?", []message{{Role: "user", Content: "what time is it?"}}, shim)
	if err != nil {
		t.Fatalf("runToolLoop returned error: %v", err)
	}
	if final != "It is 12:00." {
		t.Fatalf("unexpected final answer: %q", final)
	}
	foundToolMessage := false
	for _, msg := range updated {
		if msg.Role == "tool" {
			foundToolMessage = true
		}
	}
	if !foundToolMessage {
		t.Fatalf("native tool calling must keep recording tool-role messages")
	}
}

func TestRunToolLoopCachesUnsupportedToolsWithBroker(t *testing.T) {
	originalChatStreamExecutor := chatStreamExecutor
	t.Cleanup(func() { chatStreamExecutor = originalChatStreamExecutor })
	t.Setenv("ASH_PROMPT_TOOLS", "")
	t.Setenv("ASH_VERBOSE", "1")
	var logOutput bytes.Buffer
	originalDebugWriter := debugWriter
	originalLogger := slog.Default()
	debugWriter = &logOutput
	configureDebugLogging(&logOutput)
	t.Cleanup(func() {
		debugWriter = originalDebugWriter
		slog.SetDefault(originalLogger)
	})

	socket := filepath.Join(os.TempDir(), "ash-cap-"+strconv.Itoa(os.Getpid())+".sock")
	_ = os.Remove(socket)
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
		_ = os.Remove(socket)
	})
	t.Setenv(brokerSocketEnv, socket)
	t.Setenv(brokerTokenEnv, "test-token")

	var stateMu sync.Mutex
	unsupported := false
	var actions []string
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				payload, readErr := brokerproto.ReadFrame(conn)
				if readErr != nil {
					return
				}
				var request brokerproto.Request
				if json.Unmarshal(payload, &request) != nil || request.Token != "test-token" {
					return
				}
				stateMu.Lock()
				actions = append(actions, request.ToolCacheAction)
				response := brokerproto.Response{Version: brokerproto.Version}
				switch request.ToolCacheAction {
				case "lookup":
					response.ToolUnsupported = unsupported
				case "mark_unsupported":
					unsupported = true
				default:
					response.Error = "unexpected tool capability action"
				}
				stateMu.Unlock()
				responsePayload, marshalErr := json.Marshal(response)
				if marshalErr == nil {
					_ = brokerproto.WriteFrame(conn, responsePayload)
				}
			}()
		}
	}()

	chatCalls := 0
	chatStreamExecutor = func(_ context.Context, _ aiConfig, msgs []message, tools []toolDefinition, _ func(streamDelta)) (chatResponse, error) {
		chatCalls++
		switch chatCalls {
		case 1:
			if len(tools) == 0 {
				t.Fatal("first invocation should try native tools before the broker has a cached rejection")
			}
			return chatResponse{}, toolsUnsupportedStatusError()
		case 2, 3:
			if len(tools) != 0 {
				t.Fatalf("expected prompt-based request after cached rejection, got %d tools", len(tools))
			}
			foundCatalog := false
			for _, msg := range msgs {
				if msg.Role == "user" && strings.Contains(msg.Content, promptToolCallFence) {
					foundCatalog = true
					break
				}
			}
			if !foundCatalog {
				t.Fatal("prompt-based request is missing the tool catalog")
			}
			return chatResponse{Message: message{Role: "assistant", Content: "done"}}, nil
		default:
			t.Fatalf("unexpected chat call %d", chatCalls)
			return chatResponse{}, nil
		}
	}

	cfg := testAIConfig("http://ollama.example:11434", "gemma4:latest")
	for invocation := 1; invocation <= 2; invocation++ {
		reply, _, runErr := runToolLoop(context.Background(), cfg, "say hello", []message{{Role: "user", Content: "say hello"}}, timeToolShim())
		if runErr != nil {
			t.Fatalf("invocation %d returned error: %v", invocation, runErr)
		}
		if reply != "done" {
			t.Fatalf("invocation %d reply = %q, want done", invocation, reply)
		}
	}

	if chatCalls != 3 {
		t.Fatalf("chat calls = %d, want 3 (native rejection, fallback, cached prompt mode)", chatCalls)
	}
	stateMu.Lock()
	defer stateMu.Unlock()
	wantActions := []string{"lookup", "mark_unsupported", "lookup"}
	if len(actions) != len(wantActions) {
		t.Fatalf("broker actions = %#v, want %#v", actions, wantActions)
	}
	for i := range wantActions {
		if actions[i] != wantActions[i] {
			t.Fatalf("broker actions = %#v, want %#v", actions, wantActions)
		}
	}
	if warnings := strings.Count(logOutput.String(), `"level":"warn"`); warnings < 2 {
		t.Fatalf("verbose logs contain %d fallback warnings, want fallback and cached-mode warnings:\n%s", warnings, logOutput.String())
	}
}
