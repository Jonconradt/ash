package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPythonToolVisibility(t *testing.T) {
	originalLookPath := execLookPath
	t.Cleanup(func() { execLookPath = originalLookPath })
	tests := []struct {
		name      string
		strict    string
		available bool
		want      bool
	}{
		{name: "available", available: true, want: true},
		{name: "interpreter unavailable", want: false},
		{name: "strict mode", strict: "1", available: true, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("ASH_STRICT", test.strict)
			t.Setenv("ASH_PYTHON", "python3")
			execLookPath = func(string) (string, error) {
				if test.available {
					return "/usr/bin/python3", nil
				}
				return "", errors.New("not found")
			}
			_, got := toolNames(localToolShim{}.ListTools())["run_python3"]
			if got != test.want {
				t.Fatalf("run_python3 visibility = %t, want %t", got, test.want)
			}
		})
	}
}

func TestCallPython3(t *testing.T) {
	originalLookPath := execLookPath
	originalRunner := toolCommandRunner
	t.Cleanup(func() {
		execLookPath = originalLookPath
		toolCommandRunner = originalRunner
	})
	execLookPath = func(string) (string, error) { return "/usr/bin/python3", nil }
	t.Setenv("ASH_PYTHON", "python3")
	t.Setenv("ASH_STRICT", "")

	var gotName string
	var gotArgs []string
	toolCommandRunner = func(_ context.Context, name string, args []string, _ time.Duration, _ int) toolCommandResult {
		gotName = name
		gotArgs = append([]string(nil), args...)
		return toolCommandResult{OK: true, Command: name, ExitCode: 0}
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SESSION_ID", "python_session")
	root := filepath.Join(home, ".ash", "scratch", "python_session")
	scriptPath := filepath.Join(root, "work.py")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("mkdir scratch root: %v", err)
	}
	if err := os.WriteFile(scriptPath, []byte("print('ok')\n"), 0o600); err != nil {
		t.Fatalf("write scratch script: %v", err)
	}

	tests := []struct {
		name     string
		strict   string
		args     map[string]any
		wantOK   bool
		wantArgs []string
	}{
		{name: "inline code", args: map[string]any{"code": "print('ok')", "argv": []any{"one"}}, wantOK: true, wantArgs: []string{"-c", "print('ok')", "one"}},
		{name: "scratch script", args: map[string]any{"script_path": scriptPath, "argv": []any{"one"}}, wantOK: true, wantArgs: []string{scriptPath, "one"}},
		{name: "missing mode", args: map[string]any{}, wantOK: false},
		{name: "ambiguous mode", args: map[string]any{"code": "print('ok')", "script_path": scriptPath}, wantOK: false},
		{name: "outside scratch", args: map[string]any{"script_path": "/tmp/outside.py"}, wantOK: false},
		{name: "non python script", args: map[string]any{"script_path": filepath.Join(root, "work.sh")}, wantOK: false},
		{name: "strict mode", strict: "1", args: map[string]any{"code": "print('ok')"}, wantOK: false},
		{name: "argv references hidden dotfile", args: map[string]any{"code": "print('ok')", "argv": []any{".env"}}, wantOK: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("ASH_STRICT", test.strict)
			gotName = ""
			gotArgs = nil
			result := localToolShim{}.CallTool(context.Background(), "run_python3", test.args)
			if strings.Contains(result, `"ok":true`) != test.wantOK {
				t.Fatalf("CallTool result = %s, want OK %t", result, test.wantOK)
			}
			if test.wantOK && (gotName != "python3" || strings.Join(gotArgs, "\x00") != strings.Join(test.wantArgs, "\x00")) {
				t.Fatalf("runner got %q %#v, want python3 %#v", gotName, gotArgs, test.wantArgs)
			}
		})
	}
}

func TestCallPython3WithStdin(t *testing.T) {
	originalLookPath := execLookPath
	originalRunner := toolCommandRunner
	originalInputRunner := toolCommandWithInputRunner
	t.Cleanup(func() {
		execLookPath = originalLookPath
		toolCommandRunner = originalRunner
		toolCommandWithInputRunner = originalInputRunner
	})
	execLookPath = func(string) (string, error) { return "/usr/bin/python3", nil }
	t.Setenv("ASH_PYTHON", "python3")
	t.Setenv("ASH_STRICT", "")

	t.Run("stdin routes through the stdin-aware runner", func(t *testing.T) {
		var gotName, gotStdin string
		var gotArgs []string
		plainRunnerCalled := false
		toolCommandWithInputRunner = func(_ context.Context, name string, args []string, stdin string, _ time.Duration, _ int) toolCommandResult {
			gotName = name
			gotArgs = append([]string(nil), args...)
			gotStdin = stdin
			return toolCommandResult{OK: true, Command: name, ExitCode: 0, Stdout: "3"}
		}
		toolCommandRunner = func(_ context.Context, name string, args []string, _ time.Duration, _ int) toolCommandResult {
			plainRunnerCalled = true
			return toolCommandResult{OK: true, Command: name, ExitCode: 0}
		}

		result := localToolShim{}.CallTool(context.Background(), "run_python3", map[string]any{
			"code":  "import sys; print(len(sys.stdin.read()))",
			"stdin": "abc",
		})

		if !strings.Contains(result, `"ok":true`) {
			t.Fatalf("expected success, got %s", result)
		}
		if plainRunnerCalled {
			t.Fatalf("expected the stdin-aware runner to be used, not the plain one")
		}
		if gotName != "python3" {
			t.Fatalf("unexpected runner name %q", gotName)
		}
		if gotStdin != "abc" {
			t.Fatalf("expected stdin %q, got %q", "abc", gotStdin)
		}
		wantArgs := []string{"-c", "import sys; print(len(sys.stdin.read()))"}
		if strings.Join(gotArgs, "\x00") != strings.Join(wantArgs, "\x00") {
			t.Fatalf("unexpected args %#v, want %#v", gotArgs, wantArgs)
		}
	})

	t.Run("empty stdin falls back to the plain runner", func(t *testing.T) {
		inputRunnerCalled := false
		toolCommandWithInputRunner = func(_ context.Context, name string, args []string, stdin string, _ time.Duration, _ int) toolCommandResult {
			inputRunnerCalled = true
			return toolCommandResult{OK: true, Command: name, ExitCode: 0}
		}
		toolCommandRunner = func(_ context.Context, name string, args []string, _ time.Duration, _ int) toolCommandResult {
			return toolCommandResult{OK: true, Command: name, ExitCode: 0}
		}

		result := localToolShim{}.CallTool(context.Background(), "run_python3", map[string]any{
			"code":  "print('ok')",
			"stdin": "",
		})

		if !strings.Contains(result, `"ok":true`) {
			t.Fatalf("expected success, got %s", result)
		}
		if inputRunnerCalled {
			t.Fatalf("expected empty stdin to use the plain runner")
		}
	})

	t.Run("non UTF-8 stdin is rejected", func(t *testing.T) {
		result := localToolShim{}.CallTool(context.Background(), "run_python3", map[string]any{
			"code":  "print('ok')",
			"stdin": string([]byte{0xff, 0xfe}),
		})
		if strings.Contains(result, `"ok":true`) {
			t.Fatalf("expected failure for invalid UTF-8 stdin, got %s", result)
		}
	})
}

func TestCallUnixCommandBlocksManagedPythonToolWhenStrict(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ASH_STRICT", "1")
	toolsDir := filepath.Join(home, ".ash", "tools")
	if err := os.MkdirAll(toolsDir, 0o700); err != nil {
		t.Fatalf("mkdir tools: %v", err)
	}
	if err := os.WriteFile(filepath.Join(toolsDir, "example.py"), []byte("print('ok')\n"), 0o600); err != nil {
		t.Fatalf("write Python tool: %v", err)
	}

	result := localToolShim{allowlist: map[string]struct{}{"example.py": {}}}.CallTool(context.Background(), "run_unix_command", map[string]any{"command": "example.py"})
	if !strings.Contains(result, "Python execution is not available") {
		t.Fatalf("expected strict mode to block managed Python tool, got %s", result)
	}
}
