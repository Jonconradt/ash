package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestToolExecutionAndWorkspaceHelpers(t *testing.T) {
	t.Run("sanitizeJSONError and blocked argument", func(t *testing.T) {
		if got := sanitizeJSONError("line\n\"quoted\""); got != "line 'quoted'" {
			t.Fatalf("unexpected sanitized output: %q", got)
		}
		if !isBlockedArgument("a && b") {
			t.Fatalf("expected blocking pattern to match")
		}
	})

	t.Run("hasBlockedDotSegment default vs strict", func(t *testing.T) {
		t.Setenv("ASH_STRICT", "")
		if !hasBlockedDotSegment(".env") {
			t.Fatalf("expected bare dotfile name to be blocked")
		}
		if !hasBlockedDotSegment("foo/.env") {
			t.Fatalf("expected nested dotfile basename to be blocked")
		}
		if hasBlockedDotSegment("a/.git/config") {
			t.Fatalf("expected nested hidden directory to be allowed by default")
		}
		if hasBlockedDotSegment("./file.txt") || hasBlockedDotSegment("../file.txt") {
			t.Fatalf("expected '.' and '..' segments to never be treated as dotfiles")
		}

		t.Setenv("ASH_STRICT", "1")
		if !hasBlockedDotSegment("a/.git/config") {
			t.Fatalf("expected ASH_STRICT to block any hidden path segment")
		}
		if hasBlockedDotSegment("./file.txt") {
			t.Fatalf("expected '.' segment to never be treated as a dotfile even under ASH_STRICT")
		}
	})

	t.Run("runToolCommand uses exit errors and timeouts", func(t *testing.T) {
		ctx := context.Background()
		result := runToolCommand(ctx, "sh", []string{"-c", "exit 3"}, time.Second, 128)
		if result.OK || result.ExitCode != 3 {
			t.Fatalf("expected exit code 3, got %+v", result)
		}

		result = runToolCommand(ctx, "sh", []string{"-c", "sleep 0.2"}, 10*time.Millisecond, 128)
		if result.OK || result.ExitCode != -1 || !strings.Contains(result.Error, "status") && !strings.Contains(result.Error, "timed out") {
			t.Fatalf("expected command failure, got %+v", result)
		}
	})

	t.Run("workspace inventory and path resolution", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatalf("mkdir root: %v", err)
		}
		if err := updateWorkspaceInventory(root, "notes.txt", "scratch"); err != nil {
			t.Fatalf("update inventory: %v", err)
		}
		content, err := os.ReadFile(filepath.Join(root, inventoryFileName))
		if err != nil {
			t.Fatalf("read inventory: %v", err)
		}
		if !strings.Contains(string(content), "notes.txt | scratch") {
			t.Fatalf("inventory content mismatch: %q", string(content))
		}

		abs, rel, err := resolveWorkspacePath(root, "subdir/../file.txt")
		if err != nil {
			t.Fatalf("resolve path: %v", err)
		}
		if rel != "file.txt" {
			t.Fatalf("unexpected relative path: %q", rel)
		}
		if abs != filepath.Join(root, "file.txt") {
			t.Fatalf("unexpected absolute path: %q", abs)
		}
	})

	t.Run("resolveWorkspacePath rejects hidden dotfiles", func(t *testing.T) {
		t.Setenv("ASH_STRICT", "")
		root := t.TempDir()

		if _, _, err := resolveWorkspacePath(root, ".ash_env"); err == nil || !strings.Contains(err.Error(), "hidden dotfile") {
			t.Fatalf("expected top-level dotfile rejection, got %v", err)
		}
		if _, _, err := resolveWorkspacePath(root, "plan/.hidden"); err == nil || !strings.Contains(err.Error(), "hidden dotfile") {
			t.Fatalf("expected nested dotfile rejection, got %v", err)
		}
		if _, _, err := resolveWorkspacePath(root, "a/.git/config"); err != nil {
			t.Fatalf("expected nested hidden directory to be allowed by default, got %v", err)
		}

		t.Setenv("ASH_STRICT", "1")
		if _, _, err := resolveWorkspacePath(root, "a/.git/config"); err == nil || !strings.Contains(err.Error(), "hidden dotfile") {
			t.Fatalf("expected ASH_STRICT to reject any hidden path segment, got %v", err)
		}
	})

	t.Run("future schedule parsing", func(t *testing.T) {
		now := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
		parsed, err := parseFutureScheduleTime("now + 5 minutes", now)
		if err != nil {
			t.Fatalf("parse future schedule: %v", err)
		}
		if parsed.Sub(now) != 5*time.Minute {
			t.Fatalf("unexpected parsed time: %v", parsed)
		}
	})
}

func TestLoadAllowlistedCommands(t *testing.T) {
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	originalLookPath := execLookPath
	t.Cleanup(func() {
		_ = os.Chdir(originalCwd)
		execLookPath = originalLookPath
	})
	t.Setenv("ASH_STRICT", "")
	t.Setenv("ASH_PYTHON", "python3")
	t.Setenv("ASH_ALLOW", "")
	t.Setenv("ASH_DENY", "")
	execLookPath = func(string) (string, error) { return "", errors.New("not found") }

	t.Run("env override", func(t *testing.T) {
		t.Setenv("ASH_ALLOW", "ls, ps,python3")
		allowed, err := loadAllowlistedCommands()
		if err != nil {
			t.Fatalf("loadAllowlistedCommands error: %v", err)
		}
		if _, ok := allowed["ls"]; !ok {
			t.Fatalf("expected ls in allowlist: %#v", allowed)
		}
		if _, ok := allowed["ps"]; !ok {
			t.Fatalf("expected ps in allowlist: %#v", allowed)
		}
		if _, ok := allowed["python3"]; !ok {
			t.Fatalf("expected python3 in allowlist when Python execution is unavailable: %#v", allowed)
		}
	})

	t.Run("denylist subtraction", func(t *testing.T) {
		t.Setenv("ASH_ALLOW", "ls, ps, what_time_is_it")
		t.Setenv("ASH_DENY", "ps, what_time_is_it")
		allowed, err := loadAllowlistedCommands()
		if err != nil {
			t.Fatalf("loadAllowlistedCommands error: %v", err)
		}
		if _, ok := allowed["ls"]; !ok {
			t.Fatalf("expected ls in allowlist: %#v", allowed)
		}
		if _, ok := allowed["ps"]; ok {
			t.Fatalf("expected ps to be denied: %#v", allowed)
		}
		if _, ok := allowed["what_time_is_it"]; ok {
			t.Fatalf("expected what_time_is_it to be denied: %#v", allowed)
		}
	})

	t.Run("dot-prefixed entries are dropped", func(t *testing.T) {
		if got := normalizeToolName(".secret"); got != "" {
			t.Fatalf("expected dot-prefixed tool name to be rejected, got %q", got)
		}
		if got := normalizeToolName("ls"); got != "ls" {
			t.Fatalf("expected bare tool name to pass through, got %q", got)
		}

		t.Setenv("ASH_ALLOW", "ls,.secret")
		allowed, err := loadAllowlistedCommands()
		if err != nil {
			t.Fatalf("loadAllowlistedCommands error: %v", err)
		}
		if _, ok := allowed[".secret"]; ok {
			t.Fatalf("expected dot-prefixed entry to be dropped: %#v", allowed)
		}
		if _, ok := allowed["ls"]; !ok {
			t.Fatalf("expected ls to remain allowlisted: %#v", allowed)
		}
	})

	t.Run("python3 dropped when run_python3 is available", func(t *testing.T) {
		original := execLookPath
		t.Cleanup(func() { execLookPath = original })
		execLookPath = func(string) (string, error) { return "/usr/bin/python3", nil }

		t.Setenv("ASH_ALLOW", "ls, ps,python3")
		allowed, err := loadAllowlistedCommands()
		if err != nil {
			t.Fatalf("loadAllowlistedCommands error: %v", err)
		}
		if _, ok := allowed["python3"]; ok {
			t.Fatalf("expected python3 to be dropped when run_python3 is available: %#v", allowed)
		}
		if _, ok := allowed["ls"]; !ok {
			t.Fatalf("expected other allowlisted commands to remain: %#v", allowed)
		}
	})

	t.Run("python3 kept when Python execution unavailable", func(t *testing.T) {
		original := execLookPath
		t.Cleanup(func() { execLookPath = original })
		execLookPath = func(string) (string, error) { return "", errors.New("not found") }

		t.Setenv("ASH_ALLOW", "python3")
		allowed, err := loadAllowlistedCommands()
		if err != nil {
			t.Fatalf("loadAllowlistedCommands error: %v", err)
		}
		if _, ok := allowed["python3"]; !ok {
			t.Fatalf("expected python3 to remain allowlisted as a fallback: %#v", allowed)
		}
	})

	t.Run("cwd file wins over home", func(t *testing.T) {
		t.Setenv("ASH_ALLOW", "")
		home := t.TempDir()
		cwd := t.TempDir()
		t.Setenv("HOME", home)

		if err := os.WriteFile(filepath.Join(home, allowFileName), []byte("ls\n"), 0o600); err != nil {
			t.Fatalf("write home allow file: %v", err)
		}
		if err := os.WriteFile(filepath.Join(cwd, allowFileName), []byte("ps\n"), 0o600); err != nil {
			t.Fatalf("write cwd allow file: %v", err)
		}
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("Chdir failed: %v", err)
		}

		allowed, err := loadAllowlistedCommands()
		if err != nil {
			t.Fatalf("loadAllowlistedCommands error: %v", err)
		}
		if len(allowed) != 1 {
			t.Fatalf("expected one allowlisted command, got %#v", allowed)
		}
		if _, ok := allowed["ps"]; !ok {
			t.Fatalf("expected cwd allowlist to win, got %#v", allowed)
		}
	})

	t.Run("canonical file wins over cwd and home", func(t *testing.T) {
		t.Setenv("ASH_ALLOW", "")
		home := t.TempDir()
		cwd := t.TempDir()
		t.Setenv("HOME", home)

		if err := os.MkdirAll(filepath.Join(home, ashWorkspaceDirName), 0o700); err != nil {
			t.Fatalf("mkdir canonical workspace: %v", err)
		}
		if err := os.WriteFile(filepath.Join(home, ashWorkspaceDirName, allowFileName), []byte("say\n"), 0o600); err != nil {
			t.Fatalf("write canonical allow file: %v", err)
		}
		if err := os.WriteFile(filepath.Join(home, allowFileName), []byte("ls\n"), 0o600); err != nil {
			t.Fatalf("write home allow file: %v", err)
		}
		if err := os.WriteFile(filepath.Join(cwd, allowFileName), []byte("ps\n"), 0o600); err != nil {
			t.Fatalf("write cwd allow file: %v", err)
		}
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("Chdir failed: %v", err)
		}

		allowed, err := loadAllowlistedCommands()
		if err != nil {
			t.Fatalf("loadAllowlistedCommands error: %v", err)
		}
		if len(allowed) != 1 {
			t.Fatalf("expected one allowlisted command, got %#v", allowed)
		}
		if _, ok := allowed["say"]; !ok {
			t.Fatalf("expected canonical allowlist to win, got %#v", allowed)
		}
	})
}

func TestLocalToolShimRunUnixCommandPolicy(t *testing.T) {
	originalRunner := toolCommandRunner
	originalPipelineRunner := toolPipelineRunner
	t.Cleanup(func() {
		toolCommandRunner = originalRunner
		toolPipelineRunner = originalPipelineRunner
	})

	shim := localToolShim{allowlist: map[string]struct{}{"ls": {}}}

	t.Run("reject not allowlisted", func(t *testing.T) {
		resultJSON := shim.CallTool(context.Background(), "run_unix_command", map[string]any{"command": "cat"})
		if !strings.Contains(resultJSON, "not allowlisted") {
			t.Fatalf("expected allowlist failure, got %s", resultJSON)
		}
	})

	t.Run("reject blocked arg", func(t *testing.T) {
		resultJSON := shim.CallTool(context.Background(), "run_unix_command", map[string]any{
			"command": "ls",
			"args":    []any{"foo;bar"},
		})
		if !strings.Contains(resultJSON, "blocked shell control pattern") {
			t.Fatalf("expected blocked arg failure, got %s", resultJSON)
		}
	})

	t.Run("reject hidden dotfile arg", func(t *testing.T) {
		t.Setenv("ASH_STRICT", "")
		resultJSON := shim.CallTool(context.Background(), "run_unix_command", map[string]any{
			"command": "ls",
			"args":    []any{".env"},
		})
		if !strings.Contains(resultJSON, "hidden dotfile") {
			t.Fatalf("expected hidden dotfile rejection, got %s", resultJSON)
		}

		toolCommandRunner = func(ctx context.Context, name string, args []string, timeout time.Duration, outputMax int) toolCommandResult {
			return toolCommandResult{OK: true, Command: name, ExitCode: 0}
		}
		nestedOK := shim.CallTool(context.Background(), "run_unix_command", map[string]any{
			"command": "ls",
			"args":    []any{"a/.git/config"},
		})
		if !strings.Contains(nestedOK, `"ok":true`) {
			t.Fatalf("expected nested hidden directory arg to be allowed by default, got %s", nestedOK)
		}

		t.Setenv("ASH_STRICT", "1")
		nestedBlocked := shim.CallTool(context.Background(), "run_unix_command", map[string]any{
			"command": "ls",
			"args":    []any{"a/.git/config"},
		})
		if !strings.Contains(nestedBlocked, "hidden dotfile") {
			t.Fatalf("expected ASH_STRICT to reject nested hidden directory arg, got %s", nestedBlocked)
		}
	})

	t.Run("success", func(t *testing.T) {
		toolCommandRunner = func(ctx context.Context, name string, args []string, timeout time.Duration, outputMax int) toolCommandResult {
			if name != "ls" {
				t.Fatalf("unexpected command %q", name)
			}
			if len(args) != 1 || args[0] != "-l" {
				t.Fatalf("unexpected args %#v", args)
			}
			return toolCommandResult{OK: true, Command: "ls -l", ExitCode: 0, Stdout: "file\n"}
		}

		resultJSON := shim.CallTool(context.Background(), "run_unix_command", map[string]any{
			"command": "ls",
			"args":    []any{"-l"},
		})
		if !strings.Contains(resultJSON, `"ok":true`) {
			t.Fatalf("expected success, got %s", resultJSON)
		}
	})

	t.Run("drops empty args", func(t *testing.T) {
		toolCommandRunner = func(ctx context.Context, name string, args []string, timeout time.Duration, outputMax int) toolCommandResult {
			if name != "ls" {
				t.Fatalf("unexpected command %q", name)
			}
			if len(args) != 1 || args[0] != "-l" {
				t.Fatalf("unexpected args %#v", args)
			}
			return toolCommandResult{OK: true, Command: "ls -l", ExitCode: 0, Stdout: "file\n"}
		}

		resultJSON := shim.CallTool(context.Background(), "run_unix_command", map[string]any{
			"command": "ls",
			"args":    []any{"", "-l", ""},
		})
		if !strings.Contains(resultJSON, `"ok":true`) {
			t.Fatalf("expected success, got %s", resultJSON)
		}
	})

	t.Run("supports inline command args", func(t *testing.T) {
		inlineShim := localToolShim{allowlist: map[string]struct{}{"find": {}}}
		toolCommandRunner = func(ctx context.Context, name string, args []string, timeout time.Duration, outputMax int) toolCommandResult {
			if name != "find" {
				t.Fatalf("unexpected command %q", name)
			}
			if len(args) != 3 || args[0] != "-maxdepth" || args[1] != "2" || args[2] != "-type" {
				t.Fatalf("unexpected args %#v", args)
			}
			return toolCommandResult{OK: true, Command: "find -maxdepth 2 -type", ExitCode: 0}
		}

		resultJSON := inlineShim.CallTool(context.Background(), "run_unix_command", map[string]any{
			"command": "find -maxdepth 2",
			"args":    []any{"-type"},
		})
		if !strings.Contains(resultJSON, `"ok":true`) {
			t.Fatalf("expected success, got %s", resultJSON)
		}
	})

	t.Run("supports safe clipboard pipeline", func(t *testing.T) {
		pipelineShim := localToolShim{allowlist: map[string]struct{}{"ls": {}, "pbcopy": {}}}
		toolPipelineRunner = func(ctx context.Context, commands [][]string, display string, timeout time.Duration, outputMax int) toolCommandResult {
			if !reflect.DeepEqual(commands, [][]string{{"ls"}, {"pbcopy"}}) {
				t.Fatalf("unexpected pipeline commands: %#v", commands)
			}
			if display != "ls | pbcopy" {
				t.Fatalf("unexpected pipeline display %q", display)
			}
			return toolCommandResult{OK: true, Command: display, ExitCode: 0}
		}

		resultJSON := pipelineShim.CallTool(context.Background(), "run_unix_pipeline", map[string]any{
			"pipeline": "ls | pbcopy",
		})
		if !strings.Contains(resultJSON, `"ok":true`) {
			t.Fatalf("expected pipeline success, got %s", resultJSON)
		}
	})

	t.Run("supports sixteen command pipeline", func(t *testing.T) {
		pipelineShim := localToolShim{allowlist: map[string]struct{}{"cat": {}}}
		toolPipelineRunner = func(ctx context.Context, commands [][]string, display string, timeout time.Duration, outputMax int) toolCommandResult {
			if len(commands) != 16 {
				t.Fatalf("expected 16 pipeline commands, got %d", len(commands))
			}
			return toolCommandResult{OK: true, Command: display, ExitCode: 0}
		}

		stages := make([]string, 16)
		for i := range stages {
			stages[i] = "cat"
		}
		resultJSON := pipelineShim.CallTool(context.Background(), "run_unix_pipeline", map[string]any{
			"pipeline": strings.Join(stages, " | "),
		})
		if !strings.Contains(resultJSON, `"ok":true`) {
			t.Fatalf("expected pipeline success, got %s", resultJSON)
		}
	})

	t.Run("rejects seventeen command pipeline", func(t *testing.T) {
		stages := make([]string, 17)
		for i := range stages {
			stages[i] = "ls"
		}
		resultJSON := shim.CallTool(context.Background(), "run_unix_pipeline", map[string]any{
			"pipeline": strings.Join(stages, " | "),
		})
		if !strings.Contains(resultJSON, "between 2 and 16") {
			t.Fatalf("expected pipeline length failure, got %s", resultJSON)
		}
	})
}

func TestSanitizeCommandArgs(t *testing.T) {
	t.Run("copies safe command", func(t *testing.T) {
		input := []string{"ls", "-l"}
		got, err := sanitizeCommandArgs(input)
		if err != nil {
			t.Fatalf("sanitizeCommandArgs returned error: %v", err)
		}
		if !reflect.DeepEqual(got, input) {
			t.Fatalf("unexpected sanitized command: %#v", got)
		}
		got[0] = "changed"
		if input[0] != "ls" {
			t.Fatalf("sanitizer mutated input: %#v", input)
		}
	})

	tests := []struct {
		name    string
		command []string
	}{
		{name: "empty command", command: nil},
		{name: "empty executable", command: []string{""}},
		{name: "blocked argument", command: []string{"ls", "x;echo unsafe"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := sanitizeCommandArgs(test.command); err == nil {
				t.Fatal("sanitizeCommandArgs accepted unsafe command")
			}
		})
	}
}

func TestLocalToolShimIncludesSchedulingAndWorkspaceTools(t *testing.T) {
	shim := localToolShim{}
	tools := shim.ListTools()
	names := map[string]struct{}{}
	for _, tool := range tools {
		names[tool.Function.Name] = struct{}{}
	}
	for _, required := range []string{
		"run_unix_pipeline",
		"schedule_future_prompt",
		"schedule_recurring_prompt",
		"manage_recurring_jobs",
		"ash_read_workspace_file",
		"ash_write_workspace_file",
	} {
		if _, ok := names[required]; !ok {
			t.Fatalf("expected tool %q to be published", required)
		}
	}
}

func TestLocalToolShimSubAgentToolVisibility(t *testing.T) {
	t.Setenv(childAgentEnvName, "")
	parentNames := toolNames(localToolShim{}.ListTools())
	if _, ok := parentNames["run_sub_agent"]; !ok {
		t.Fatal("expected parent tool list to include run_sub_agent")
	}

	t.Setenv(childAgentEnvName, childAgentEnvValue)
	childNames := toolNames(localToolShim{}.ListTools())
	if _, ok := childNames["run_sub_agent"]; ok {
		t.Fatal("expected child tool list to omit run_sub_agent")
	}
}
