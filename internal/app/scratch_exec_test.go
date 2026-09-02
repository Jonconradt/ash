package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCallToolWriteScratchFileRecordsScratchWriteMetric(t *testing.T) {
	shim := localToolShim{}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SESSION_ID", "session_123")

	metrics := newExecutionMetrics(time.Now())
	ctx := withExecutionMetrics(context.Background(), metrics)

	result := shim.CallTool(ctx, "ash_write_scratch_file", map[string]any{
		"path":    "plan/notes.txt",
		"content": "alpha",
	})
	if !strings.Contains(result, `"ok":true`) {
		t.Fatalf("expected successful scratch write, got %s", result)
	}

	snap := metrics.snapshot()
	if got := snap.ScratchWrites["plan/notes.txt"]; got != 1 {
		t.Fatalf("expected scratch write recorded for plan/notes.txt, got counts %v", snap.ScratchWrites)
	}
}

func TestCallToolAppendScratchFileRecordsScratchWriteMetric(t *testing.T) {
	shim := localToolShim{}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SESSION_ID", "session_123")

	metrics := newExecutionMetrics(time.Now())
	ctx := withExecutionMetrics(context.Background(), metrics)

	shim.CallTool(ctx, "ash_write_scratch_file", map[string]any{"path": "plan/notes.txt", "content": "alpha"})
	result := shim.CallTool(ctx, "ash_append_scratch_file", map[string]any{"path": "plan/notes.txt", "content": " beta"})
	if !strings.Contains(result, `"ok":true`) {
		t.Fatalf("expected successful scratch append, got %s", result)
	}

	snap := metrics.snapshot()
	if got := snap.ScratchWrites["plan/notes.txt"]; got != 2 {
		t.Fatalf("expected 2 recorded scratch writes for plan/notes.txt (write + append), got counts %v", snap.ScratchWrites)
	}
}

func TestCallToolEditScratchFileRecordsScratchWriteMetric(t *testing.T) {
	shim := localToolShim{}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SESSION_ID", "session_123")

	metrics := newExecutionMetrics(time.Now())
	ctx := withExecutionMetrics(context.Background(), metrics)

	shim.CallTool(ctx, "ash_write_scratch_file", map[string]any{"path": "plan/notes.txt", "content": "alpha"})
	result := shim.CallTool(ctx, "ash_edit_scratch_file", map[string]any{"path": "plan/notes.txt", "content": "gamma"})
	if !strings.Contains(result, `"ok":true`) {
		t.Fatalf("expected successful scratch edit, got %s", result)
	}

	snap := metrics.snapshot()
	if got := snap.ScratchWrites["plan/notes.txt"]; got != 2 {
		t.Fatalf("expected 2 recorded scratch writes for plan/notes.txt (write + edit), got counts %v", snap.ScratchWrites)
	}
}

// TestRunUnixCommandScratchPathArgCountedAsScratchExec is the positive case: an absolute
// scratch-file path passed as an argv entry to an allowlisted interpreter is recorded.
func TestRunUnixCommandScratchPathArgCountedAsScratchExec(t *testing.T) {
	originalRunner := toolCommandRunner
	t.Cleanup(func() { toolCommandRunner = originalRunner })
	toolCommandRunner = func(ctx context.Context, name string, args []string, timeout time.Duration, outputMax int) toolCommandResult {
		return toolCommandResult{OK: true, Command: name, ExitCode: 0}
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SESSION_ID", "session_123")

	scriptPath := filepath.Join(home, ".ash", "scratch", "session_123", "plan", "run.sh")
	shim := localToolShim{allowlist: map[string]struct{}{"bash": {}}}
	metrics := newExecutionMetrics(time.Now())
	ctx := withExecutionMetrics(context.Background(), metrics)

	result := shim.CallTool(ctx, "run_unix_command", map[string]any{
		"command": "bash",
		"args":    []any{scriptPath},
	})
	if !strings.Contains(result, `"ok":true`) {
		t.Fatalf("expected successful command, got %s", result)
	}

	snap := metrics.snapshot()
	if got := snap.ScratchExecs["plan/run.sh"]; got != 1 {
		t.Fatalf("expected scratch exec recorded for plan/run.sh, got counts %v", snap.ScratchExecs)
	}
}

// TestRunUnixCommandOutsideScratchRootNotCountedAsScratchExec is the negative case: executing
// a non-scratch path must not create any scratch-exec metric entry.
func TestRunUnixCommandOutsideScratchRootNotCountedAsScratchExec(t *testing.T) {
	originalRunner := toolCommandRunner
	t.Cleanup(func() { toolCommandRunner = originalRunner })
	toolCommandRunner = func(ctx context.Context, name string, args []string, timeout time.Duration, outputMax int) toolCommandResult {
		return toolCommandResult{OK: true, Command: name, ExitCode: 0}
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SESSION_ID", "session_123")

	shim := localToolShim{allowlist: map[string]struct{}{"echo": {}}}
	metrics := newExecutionMetrics(time.Now())
	ctx := withExecutionMetrics(context.Background(), metrics)

	result := shim.CallTool(ctx, "run_unix_command", map[string]any{
		"command": "echo",
		"args":    []any{"hello"},
	})
	if !strings.Contains(result, `"ok":true`) {
		t.Fatalf("expected successful command, got %s", result)
	}

	snap := metrics.snapshot()
	if len(snap.ScratchExecs) != 0 {
		t.Fatalf("expected no scratch exec entries for a non-scratch argument, got %v", snap.ScratchExecs)
	}
}

// TestRunUnixCommandRelativeScratchPathStillCountedAsScratchExec guards against
// path-normalization bugs: a relative path that resolves into the scratch root from cwd
// must still be counted.
func TestRunUnixCommandRelativeScratchPathStillCountedAsScratchExec(t *testing.T) {
	originalRunner := toolCommandRunner
	originalGetwd := osGetwd
	t.Cleanup(func() {
		toolCommandRunner = originalRunner
		osGetwd = originalGetwd
	})
	toolCommandRunner = func(ctx context.Context, name string, args []string, timeout time.Duration, outputMax int) toolCommandResult {
		return toolCommandResult{OK: true, Command: name, ExitCode: 0}
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SESSION_ID", "session_123")

	scratchSessionDir := filepath.Join(home, ".ash", "scratch", "session_123", "plan")
	osGetwd = func() (string, error) { return scratchSessionDir, nil }

	shim := localToolShim{allowlist: map[string]struct{}{"bash": {}}}
	metrics := newExecutionMetrics(time.Now())
	ctx := withExecutionMetrics(context.Background(), metrics)

	result := shim.CallTool(ctx, "run_unix_command", map[string]any{
		"command": "bash",
		"args":    []any{"run.sh"},
	})
	if !strings.Contains(result, `"ok":true`) {
		t.Fatalf("expected successful command, got %s", result)
	}

	snap := metrics.snapshot()
	if got := snap.ScratchExecs["plan/run.sh"]; got != 1 {
		t.Fatalf("expected scratch exec recorded for plan/run.sh via relative path, got counts %v", snap.ScratchExecs)
	}
}

func TestScratchFileToolsRejectHiddenDotfilePaths(t *testing.T) {
	shim := localToolShim{}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SESSION_ID", "session_123")
	t.Setenv("ASH_STRICT", "")
	ctx := context.Background()

	writeResult := shim.CallTool(ctx, "ash_write_scratch_file", map[string]any{
		"path":    "notes/.secret.txt",
		"content": "alpha",
	})
	if !strings.Contains(writeResult, "hidden dotfile") {
		t.Fatalf("expected ash_write_scratch_file to reject dotfile path, got %s", writeResult)
	}

	_ = shim.CallTool(ctx, "ash_write_scratch_file", map[string]any{
		"path":    "plan/notes.txt",
		"content": "alpha",
	})
	readResult := shim.CallTool(ctx, "ash_read_scratch_file", map[string]any{
		"path": ".env",
	})
	if !strings.Contains(readResult, "hidden dotfile") {
		t.Fatalf("expected ash_read_scratch_file to reject dotfile path, got %s", readResult)
	}
}
