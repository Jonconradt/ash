// Copyright (c) 2026 Jonathan L. Conradt
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestContinueEnabled(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  bool
	}{
		{"unset defaults to fresh", "", false},
		{"one", "1", true},
		{"true", "true", true},
		{"yes", "yes", true},
		{"on", "on", true},
		{"enabled", "enabled", true},
		{"false fails closed fresh", "false", false},
		{"zero fails closed fresh", "0", false},
		{"invalid fails closed fresh", "maybe", false},
		{"case insensitive", "YES", true},
		{"whitespace trimmed", " true ", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ASH_CONTINUE", tc.value)
			if got := continueEnabled(); got != tc.want {
				t.Fatalf("continueEnabled() = %v, want %v (ASH_CONTINUE=%q)", got, tc.want, tc.value)
			}
		})
	}
}

func TestParseContinueFlags(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		want    []string
		wantSet bool
	}{
		{"long flag", []string{"--continue", "hello"}, []string{"hello"}, true},
		{"short flag", []string{"-c", "hello"}, []string{"hello"}, true},
		{"no flag", []string{"hello"}, []string{"hello"}, false},
		{"flag only", []string{"--continue"}, []string{}, true},
		{"double flag", []string{"--continue", "-c", "hello"}, []string{"hello"}, true},
		{"prompt mentioning -c preserved", []string{"explain", "-c"}, []string{"explain", "-c"}, false},
		{"dashdash preserves flag text", []string{"--", "--continue", "hello"}, []string{"--", "--continue", "hello"}, false},
		{"after attach pair", []string{"--attach", "x.png", "--continue", "hello"}, []string{"--attach", "x.png", "hello"}, true},
		{"empty", []string{}, []string{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, set := parseContinueFlags(tc.args)
			if set != tc.wantSet {
				t.Fatalf("parseContinueFlags(%v) set = %v, want %v", tc.args, set, tc.wantSet)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parseContinueFlags(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

// setupHistoryToolHome writes a history file with the given conversation for
// the fixed session/key and returns the shim wired to read it.
func setupHistoryToolHome(t *testing.T, conversation []message) localToolShim {
	t.Helper()
	home := t.TempDir()
	origHome := osUserHomeDir
	t.Cleanup(func() { osUserHomeDir = origHome })
	osUserHomeDir = func() (string, error) { return home, nil }
	t.Setenv("ASH_SESSION_ID", "histool1")
	t.Setenv("ASH_SCHEDULED_TASK", "")

	path := filepath.Join(home, ".local", "state", "ash", historyDirName, "histool1.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir history dir: %v", err)
	}
	data := historyData{
		SchemaVersion: historySchemaVersion,
		Version:       executionDashboardVersion(),
		Conversations: map[string][]message{"key1": conversation},
	}
	if err := saveHistory(path, data); err != nil {
		t.Fatalf("saveHistory: %v", err)
	}
	return localToolShim{historyKey: "key1"}
}

func historyFixture(t *testing.T) []message {
	t.Helper()
	msgs := []message{
		{Role: "user", Content: "first question about apples"},
		{Role: "assistant", Content: "answer one about APPLES"},
		{Role: "user", Content: "second question about oranges"},
		{Role: "assistant", Content: "answer two about oranges"},
		{Role: "user", Content: "third question about bananas"},
		{Role: "assistant", Content: "answer three about bananas"},
		{Role: "user", Content: "fourth question about apples"},
		{Role: "assistant", Content: "answer four about apples"},
	}
	return msgs
}

func TestCallReadConversationHistoryLastNPrompts(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       map[string]any
		wantPieces []string
		wantAbsent []string
		wantCount  int // -1 means skip count check
	}{
		{"default last 5 prompts covers all fixture", map[string]any{}, []string{"first question", "fourth question"}, nil, 8},
		{"last 1 prompt", map[string]any{"last_n_prompts": 1}, []string{"fourth question", "answer four"}, []string{"first question"}, 2},
		{"last 2 prompts", map[string]any{"last_n_prompts": 2}, []string{"third question", "fourth question"}, []string{"first question"}, 4},
		{"last 0 prompts returns no match note", map[string]any{"last_n_prompts": 0}, []string{"no matching history messages"}, nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shim := setupHistoryToolHome(t, historyFixture(t))
			result := shim.callReadConversationHistory(tc.args)
			if !result.OK {
				t.Fatalf("expected OK, got error %q", result.Error)
			}
			for _, piece := range tc.wantPieces {
				if !strings.Contains(result.Stdout, piece) {
					t.Fatalf("stdout missing %q: %q", piece, result.Stdout)
				}
			}
			for _, piece := range tc.wantAbsent {
				if strings.Contains(result.Stdout, piece) {
					t.Fatalf("stdout should not contain %q: %q", piece, result.Stdout)
				}
			}
			if tc.wantCount >= 0 {
				count := strings.Count(result.Stdout, "---") + 1
				if result.Stdout == "no matching history messages" {
					count = 0
				}
				if count != tc.wantCount {
					t.Fatalf("message count = %d, want %d: %q", count, tc.wantCount, result.Stdout)
				}
			}
		})
	}
}

func TestCallReadConversationHistoryKeyword(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       map[string]any
		wantPieces []string
		wantAbsent []string
	}{
		{"keyword searches all history", map[string]any{"keyword": "apples"}, []string{"first question", "fourth question"}, []string{"oranges", "bananas"}},
		{"keyword case insensitive", map[string]any{"keyword": "APPLES"}, []string{"first question", "fourth question"}, []string{"oranges"}},
		{"keyword within window intersection", map[string]any{"last_n_prompts": 1, "keyword": "apples"}, []string{"fourth question"}, []string{"first question"}},
		{"keyword no match", map[string]any{"keyword": "kiwi"}, []string{"no matching history messages"}, nil},
		{"keyword empty treated as absent", map[string]any{"keyword": "  "}, []string{"first question"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shim := setupHistoryToolHome(t, historyFixture(t))
			result := shim.callReadConversationHistory(tc.args)
			if !result.OK {
				t.Fatalf("expected OK, got error %q", result.Error)
			}
			for _, piece := range tc.wantPieces {
				if !strings.Contains(result.Stdout, piece) {
					t.Fatalf("stdout missing %q: %q", piece, result.Stdout)
				}
			}
			for _, piece := range tc.wantAbsent {
				if strings.Contains(result.Stdout, piece) {
					t.Fatalf("stdout should not contain %q: %q", piece, result.Stdout)
				}
			}
		})
	}
}

func TestCallReadConversationHistoryPaging(t *testing.T) {
	shim := setupHistoryToolHome(t, historyFixture(t))
	result := shim.callReadConversationHistory(map[string]any{"offset": 2, "limit": 2})
	if !result.OK {
		t.Fatalf("expected OK, got %q", result.Error)
	}
	if !strings.Contains(result.Stdout, "second question") || !strings.Contains(result.Stdout, "answer two") {
		t.Fatalf("paging slice mismatch: %q", result.Stdout)
	}
	if strings.Contains(result.Stdout, "first question") || strings.Contains(result.Stdout, "third question") {
		t.Fatalf("paging slice should exclude outside-window messages: %q", result.Stdout)
	}

	offsetResult := shim.callReadConversationHistory(map[string]any{"offset": 100})
	if !offsetResult.OK || offsetResult.Stdout != "no matching history messages" {
		t.Fatalf("offset beyond range should return no-match note, got OK=%v %q", offsetResult.OK, offsetResult.Stdout)
	}

	clamped := shim.callReadConversationHistory(map[string]any{"limit": 50, "last_n_prompts": 100})
	if !clamped.OK {
		t.Fatalf("generous limit should clamp, got %q", clamped.Error)
	}
}

func TestCallReadConversationHistoryHardCap(t *testing.T) {
	msgs := make([]message, 0, 40)
	for i := 0; i < 20; i++ {
		msgs = append(msgs,
			message{Role: "user", Content: "prompt " + strings.Repeat("x", 1)},
			message{Role: "assistant", Content: "reply"},
		)
	}
	shim := setupHistoryToolHome(t, msgs)
	result := shim.callReadConversationHistory(map[string]any{"keyword": "prompt", "limit": 100})
	if !result.OK {
		t.Fatalf("expected OK, got %q", result.Error)
	}
	count := strings.Count(result.Stdout, "---") + 1
	if count != historyToolMaxReturned {
		t.Fatalf("returned %d messages, hard cap is %d", count, historyToolMaxReturned)
	}
}

func TestCallReadConversationHistoryTruncates(t *testing.T) {
	long := strings.Repeat("a", historyToolTruncateBytes+500)
	shim := setupHistoryToolHome(t, []message{{Role: "user", Content: long}})
	result := shim.callReadConversationHistory(map[string]any{})
	if !result.OK {
		t.Fatalf("expected OK, got %q", result.Error)
	}
	if !strings.Contains(result.Stdout, "...truncated...") {
		t.Fatalf("expected truncation marker: %q", result.Stdout[len(result.Stdout)-40:])
	}
	if len(result.Stdout) > historyToolTruncateBytes+100 {
		t.Fatalf("stdout too long after truncation: %d bytes", len(result.Stdout))
	}
}

func TestCallReadConversationHistoryErrors(t *testing.T) {
	t.Run("empty history", func(t *testing.T) {
		shim := setupHistoryToolHome(t, nil)
		result := shim.callReadConversationHistory(map[string]any{})
		if result.OK {
			t.Fatalf("expected OK=false for empty history: %+v", result)
		}
	})
	t.Run("missing key", func(t *testing.T) {
		shim := setupHistoryToolHome(t, historyFixture(t))
		shim.historyKey = "other-key"
		result := shim.callReadConversationHistory(map[string]any{})
		if result.OK || !strings.Contains(result.Error, "no conversation history") {
			t.Fatalf("expected no-history error for wrong key, got %+v", result)
		}
	})
	t.Run("unconfigured key", func(t *testing.T) {
		shim := localToolShim{}
		result := shim.CallTool(t.Context(), "read_conversation_history", map[string]any{})
		var decoded map[string]any
		if err := json.Unmarshal([]byte(result), &decoded); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if decoded["ok"] != false {
			t.Fatalf("expected ok=false, got %s", result)
		}
	})
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"negative last_n", map[string]any{"last_n_prompts": -1}},
		{"string last_n", map[string]any{"last_n_prompts": "two"}},
		{"non-string keyword", map[string]any{"keyword": 42}},
		{"negative offset", map[string]any{"offset": -1}},
		{"zero limit", map[string]any{"limit": 0}},
		{"negative limit", map[string]any{"limit": -2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shim := setupHistoryToolHome(t, historyFixture(t))
			result := shim.callReadConversationHistory(tc.args)
			if result.OK {
				t.Fatalf("expected OK=false for %v, got stdout %q", tc.args, result.Stdout)
			}
		})
	}
}

func TestReadConversationHistoryToolRegistered(t *testing.T) {
	shim := localToolShim{}
	var found bool
	for _, tool := range shim.ListTools() {
		if tool.Function.Name == "read_conversation_history" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("read_conversation_history missing from ListTools")
	}
	// CallTool dispatch is covered by the unconfigured-key case above; verify
	// an unknown tool still fails closed.
	raw := shim.CallTool(t.Context(), "read_conversation_history", map[string]any{"last_n_prompts": "bad"})
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded["ok"] != false {
		t.Fatalf("expected ok=false for bad arg via dispatch: %s", raw)
	}
}

// TestRunContext verifies that fresh requests get only the immediately prior
// turn as reference context, while --continue includes the full conversation.
func TestRunContext(t *testing.T) {
	for _, tc := range []struct {
		name               string
		args               []string
		envContinue        string
		wantEarlierHistory bool
	}{
		{"latest turn by default", []string{"second prompt"}, "", false},
		{"continue flag includes all history", []string{"--continue", "second prompt"}, "", true},
		{"continue env includes all history", []string{"second prompt"}, "1", true},
		{"invalid env includes only latest turn", []string{"second prompt"}, "maybe", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			cwd := t.TempDir()
			originalCwd, err := os.Getwd()
			if err != nil {
				t.Fatalf("Getwd: %v", err)
			}
			t.Cleanup(func() {
				if err := os.Chdir(originalCwd); err != nil {
					t.Errorf("restore working directory: %v", err)
				}
			})
			t.Setenv("HOME", home)
			origHome := osUserHomeDir
			t.Cleanup(func() { osUserHomeDir = origHome })
			osUserHomeDir = func() (string, error) { return home, nil }
			if err := os.Chdir(cwd); err != nil {
				t.Fatalf("Chdir: %v", err)
			}
			t.Setenv("ASH_SESSION_ID", "freshctx1")
			t.Setenv("ASH_SCHEDULED_TASK", "")
			t.Setenv("ASH_CONTINUE", tc.envContinue)
			t.Setenv("ASH_STREAM", "0")
			t.Setenv(ashEnvAlwaysOpenAIAPI, "0")
			t.Setenv("ASH_VERBOSE", "")
			t.Setenv("ASH_AI_ENDPOINT", "http://localhost:11434")
			t.Setenv("ASH_AI_MODEL", "llama3.1")

			var gotMessages []message
			originalExecutor := chatStreamExecutor
			t.Cleanup(func() { chatStreamExecutor = originalExecutor })
			chatStreamExecutor = func(_ context.Context, _ aiConfig, msgs []message, _ []toolDefinition, _ func(streamDelta)) (chatResponse, error) {
				gotMessages = append([]message(nil), msgs...)
				return chatResponse{Message: message{Role: "assistant", Content: "ok"}}, nil
			}

			// Seed persisted history for this session/key.
			historyKey := "http://localhost:11434/llama3.1"
			path := filepath.Join(home, ".local", "state", "ash", historyDirName, "freshctx1.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			prior := []message{
				{Role: "user", Content: "first older question"},
				{Role: "assistant", Content: "first older answer"},
				{Role: "user", Content: "latest prior question"},
				{Role: "assistant", ToolCalls: []toolCall{{Function: toolFunctionCall{Name: "run_python3"}}}},
				{Role: "tool", ToolName: "run_python3", Content: "Prime check result: no primes found"},
				{Role: "assistant", Content: "latest prior answer based on the script execution"},
			}
			data := historyData{
				SchemaVersion: historySchemaVersion,
				Version:       executionDashboardVersion(),
				Conversations: map[string][]message{historyKey: prior},
			}
			if err := saveHistory(path, data); err != nil {
				t.Fatalf("saveHistory: %v", err)
			}

			var stdout, stderr bytes.Buffer
			if code := run(tc.args, &stdout, &stderr); code != 0 {
				t.Fatalf("run exit %d stderr=%q", code, stderr.String())
			}
			var joinedBuf strings.Builder
			for _, m := range gotMessages {
				_, _ = joinedBuf.WriteString(m.Content)
				_ = joinedBuf.WriteByte('\n')
			}
			joined := joinedBuf.String()
			hasLatest := strings.Contains(joined, "latest prior question") &&
				strings.Contains(joined, "latest prior answer based on the script execution") &&
				strings.Contains(joined, "Prime check result: no primes found")
			hasToolCall := strings.Contains(joined, "assistant requested tools: run_python3")
			for _, m := range gotMessages {
				for _, call := range m.ToolCalls {
					hasToolCall = hasToolCall || call.Function.Name == "run_python3"
				}
			}
			hasLatest = hasLatest && hasToolCall
			if !hasLatest {
				t.Fatalf("request omitted the latest prior turn; messages=%q", joined)
			}
			hasEarlier := strings.Contains(joined, "first older question")
			if hasEarlier != tc.wantEarlierHistory {
				t.Fatalf("request includes older history = %v, want %v; messages=%q", hasEarlier, tc.wantEarlierHistory, joined)
			}
			if !strings.Contains(joined, "second prompt") {
				t.Fatalf("request missing current prompt: %q", joined)
			}

			// Persisted history must still contain the prior turns in both modes.
			saved, err := loadHistory(path)
			if err != nil {
				t.Fatalf("loadHistory: %v", err)
			}
			var savedBuf strings.Builder
			for _, m := range saved.Conversations[historyKey] {
				_, _ = savedBuf.WriteString(m.Content)
				_ = savedBuf.WriteByte('\n')
			}
			savedJoined := savedBuf.String()
			if !strings.Contains(savedJoined, "first older question") || !strings.Contains(savedJoined, "latest prior question") || !strings.Contains(savedJoined, "second prompt") {
				t.Fatalf("persisted history should retain prior + current turns: %q", savedJoined)
			}
		})
	}
}
