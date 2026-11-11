// Copyright (c) 2026 Jonathan L. Conradt
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Regression tests for Jonconradt/ash#22: strict chat templates (for
// example, qwen3.8-flash-next behind an Ollama-compatible server) reject a
// system message after a non-system turn with HTTP 400. Mid-conversation
// injections must therefore never use the system role, and no system-role
// message may persist in saved history.

// assertNoSystemAfterNonSystemTurn fails the test if any message with role
// "system" appears after the first non-system message. A leading system
// prompt (messages[0]) is allowed: strict templates accept it there.
func assertNoSystemAfterNonSystemTurn(t *testing.T, label string, msgs []message) {
	t.Helper()
	seenNonSystem := false
	for i, msg := range msgs {
		if msg.Role == "system" {
			if seenNonSystem {
				t.Fatalf("%s: system-role message at index %d follows a non-system turn: %#v", label, i, msgs)
			}
			continue
		}
		seenNonSystem = true
	}
}

// assertNoSystemRole fails the test if any message has role "system".
func assertNoSystemRole(t *testing.T, label string, msgs []message) {
	t.Helper()
	for i, msg := range msgs {
		if msg.Role == "system" {
			t.Fatalf("%s: system-role message at index %d: %#v", label, i, msgs)
		}
	}
}

func TestStripSystemMessageRemovesAllSystemMessages(t *testing.T) {
	in := []message{
		{Role: "system", Content: "leading prompt"},
		{Role: "user", Content: "list files"},
		{Role: "system", Content: "stale mid-history injection"},
		{Role: "assistant", Content: "done"},
		{Role: "system", Content: "trailing injection"},
	}
	got := stripSystemMessage(in)
	assertNoSystemRole(t, "stripped history", got)
	if len(got) != 2 || got[0].Role != "user" || got[1].Role != "assistant" {
		t.Fatalf("expected only the user and assistant turns to survive, got %#v", got)
	}
	if got := stripSystemMessage(nil); got != nil {
		t.Fatalf("expected nil for empty history, got %#v", got)
	}
}

func TestDropNonLeadingSystemMessages(t *testing.T) {
	in := []message{
		{Role: "system", Content: "leading prompt"},
		{Role: "user", Content: "list files"},
		{Role: "system", Content: "stale mid-history injection"},
		{Role: "assistant", Content: "done"},
	}
	got := dropNonLeadingSystemMessages(in)
	if len(got) != 3 || got[0].Role != "system" || got[1].Role != "user" || got[2].Role != "assistant" {
		t.Fatalf("expected the leading prompt kept and the stale injection dropped, got %#v", got)
	}
	if got := dropNonLeadingSystemMessages(nil); got != nil {
		t.Fatalf("expected nil for empty history, got %#v", got)
	}
}

func TestRunToolLoopNoSystemAfterNonSystemTurn(t *testing.T) {
	// Drive the loop through one tool call and a final answer, capturing
	// every request payload, and check the strict-template invariant on
	// each one. Also covers prompt-tools mode, where the tool catalog is
	// injected every round, and legacy history carrying a stale
	// mid-history system message from before this fix.
	originalExecutor := chatStreamExecutor
	originalRunner := toolCommandRunner
	t.Cleanup(func() {
		chatStreamExecutor = originalExecutor
		toolCommandRunner = originalRunner
	})
	toolCommandRunner = func(ctx context.Context, name string, args []string, timeout time.Duration, outputMax int) toolCommandResult {
		return toolCommandResult{OK: true, Command: "ls -1", ExitCode: 0, Stdout: "a\nb\n"}
	}

	run := func(t *testing.T, promptToolsMode string, history []message) (payloads [][]message, returned []message) {
		t.Helper()
		t.Setenv("ASH_PROMPT_TOOLS", promptToolsMode)
		var captured [][]message
		calls := 0
		chatStreamExecutor = func(_ context.Context, _ aiConfig, msgs []message, _ []toolDefinition, _ func(streamDelta)) (chatResponse, error) {
			captured = append(captured, append([]message(nil), msgs...))
			calls++
			if calls == 1 {
				return chatResponse{Message: message{Role: "assistant", ToolCalls: []toolCall{{
					Function: toolFunctionCall{Name: "run_unix_command", Arguments: map[string]any{"command": "ls", "args": []any{"-1"}}},
				}}}}, nil
			}
			return chatResponse{Message: message{Role: "assistant", Content: "done"}}, nil
		}
		shim := localToolShim{allowlist: map[string]struct{}{"ls": {}}}
		final, updated, err := runToolLoop(context.Background(), testAIConfig("http://example.invalid", "model"), "list files", history, shim)
		if err != nil {
			t.Fatalf("runToolLoop returned error: %v", err)
		}
		if final != "done" {
			t.Fatalf("expected final answer %q, got %q", "done", final)
		}
		if calls != 2 {
			t.Fatalf("expected 2 chat calls, got %d", calls)
		}
		return captured, updated
	}

	check := func(t *testing.T, label string, payloads [][]message, returned []message) {
		t.Helper()
		if len(payloads) == 0 {
			t.Fatalf("%s: no request payloads captured", label)
		}
		for _, payload := range payloads {
			assertNoSystemAfterNonSystemTurn(t, label+" payload", payload)
		}
		assertNoSystemAfterNonSystemTurn(t, label+" returned history", returned)
		assertNoSystemRole(t, label+" stripped history", stripSystemMessage(returned))
	}

	t.Run("native tools", func(t *testing.T) {
		payloads, returned := run(t, "off", []message{
			{Role: "system", Content: "you are ash"},
			{Role: "user", Content: "list files"},
		})
		check(t, "native tools", payloads, returned)
	})

	t.Run("prompt tools forced", func(t *testing.T) {
		payloads, returned := run(t, "force", []message{
			{Role: "system", Content: "you are ash"},
			{Role: "user", Content: "list files"},
		})
		check(t, "prompt tools forced", payloads, returned)
		sawCatalog := false
		for _, payload := range payloads {
			for _, msg := range payload {
				if msg.Role == "user" && strings.Contains(msg.Content, promptToolCallFence) {
					sawCatalog = true
				}
			}
		}
		if !sawCatalog {
			t.Fatalf("expected the prompt tool catalog to be injected as a user-role message, got %#v", payloads)
		}
	})

	t.Run("legacy history with stale system message", func(t *testing.T) {
		payloads, returned := run(t, "off", []message{
			{Role: "system", Content: "you are ash"},
			{Role: "user", Content: "list files"},
			{Role: "system", Content: "stale mid-history injection"},
			{Role: "assistant", Content: "earlier answer"},
			{Role: "user", Content: "list files"},
		})
		check(t, "legacy history", payloads, returned)
		for _, payload := range payloads {
			for _, msg := range payload {
				if strings.Contains(msg.Content, "stale mid-history injection") {
					t.Fatalf("stale system-role message leaked into the payload: %#v", payload)
				}
			}
		}
	})
}
