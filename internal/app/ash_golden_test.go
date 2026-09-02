package app

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestFormatAssistantOutputGolden(t *testing.T) {
	original := markdownRenderer
	t.Cleanup(func() { markdownRenderer = original })

	markdownRenderer = func(string) (string, error) {
		return "\x1b[1mbold 🙂\x1b[0m\n\n", nil
	}

	got := formatAssistantOutput("ignored")
	gotQuoted := strconv.QuoteToASCII(got) + "\n"

	goldenPath := filepath.Join("testdata", "formatAssistantOutput.golden")
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden file: %v", err)
	}

	if string(want) != gotQuoted {
		t.Fatalf("golden mismatch\nwant: %s\ngot:  %s", strings.TrimSpace(string(want)), strings.TrimSpace(gotQuoted))
	}
}

func TestRunGoldenFailurePromptDirectoryAndExecutables(t *testing.T) {
	t.Setenv("ASH_VERBOSE", "")
	// This test's fake server speaks the legacy Ollama chat format, so it
	// explicitly disables both flags rather than relying on their (now-on) defaults.
	t.Setenv("ASH_STREAM", "0")
	t.Setenv(ashEnvAlwaysOpenAIAPI, "0")
	originalRunner := toolCommandRunner
	originalRenderer := markdownRenderer
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	t.Cleanup(func() {
		toolCommandRunner = originalRunner
		markdownRenderer = originalRenderer
		_ = os.Chdir(originalCwd)
	})

	markdownRenderer = func(input string) (string, error) {
		return input, nil
	}

	toolCommandRunner = func(ctx context.Context, name string, args []string, timeout time.Duration, outputMax int) toolCommandResult {
		switch name {
		case "pwd":
			return toolCommandResult{OK: true, Command: "pwd", ExitCode: 0, Stdout: "/workspace/demo\n"}
		case "find":
			return toolCommandResult{OK: true, Command: "find -maxdepth 1 -type f -perm -111", ExitCode: 0, Stdout: "./ash\n./scripts/release/next_version.sh\n"}
		default:
			return toolCommandResult{OK: false, Command: name, ExitCode: 127, Error: "unexpected command in golden test", EID: "ekYOiDsB"}
		}
	}

	requestCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "application/json")

		switch requestCount {
		case 1:
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"run_unix_command","arguments":{"command":"pwd"}}}]}}`))
		case 2:
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"run_unix_command","arguments":{"command":"find","args":["-maxdepth","1","-type","f","-perm","-111"]}}}]}}`))
		default:
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"You are currently in:\n/workspace/demo\n\nExecutable files in this directory:\n- ./ash\n- ./scripts/release/next_version.sh"}}`))
		}
	}))
	defer srv.Close()

	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}

	if err := os.WriteFile(filepath.Join(cwd, systemFileName), []byte("sys"), 0o600); err != nil {
		t.Fatalf("write system file: %v", err)
	}

	t.Setenv("AI", "")
	t.Setenv("AI_ENDPOINT", srv.URL)
	t.Setenv("AI_MODEL", "llama3.1")
	t.Setenv("ASH_TOOL_ALLOWLIST", "pwd,find")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"What", "directory", "am", "I", "in", "and", "are", "there", "any", "executeable", "files?"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d stderr=%q", code, stderr.String())
	}

	if requestCount < 3 {
		t.Fatalf("expected at least 3 model turns, got %d", requestCount)
	}

	goldenPath := filepath.Join(originalCwd, "testdata", "failure_prompt_directory_executables_e2e.golden")
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden file: %v", err)
	}

	if stdout.String() != string(want) {
		t.Fatalf("golden mismatch\nwant:\n%s\n\ngot:\n%s", string(want), stdout.String())
	}

	if strings.Contains(stderr.String(), "cannot provide") {
		t.Fatalf("regression: refusal text should not appear, got stderr=%q", stderr.String())
	}
}
