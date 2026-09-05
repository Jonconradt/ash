package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain keeps the suite from building real virtualenvs during install/upgrade tests.
func TestMain(m *testing.M) {
	provisionPythonEnv = func(io.Writer) {}
	restartStaleBrokerDaemons = func(string) {}
	os.Exit(m.Run())
}

type stubToolShim struct {
	tools []toolDefinition
}

func (s stubToolShim) ListTools() []toolDefinition { return s.tools }
func (s stubToolShim) CallTool(ctx context.Context, name string, args map[string]any) string {
	return `{"ok":true,"command":"` + name + `","exit_code":0,"stdout":"ok"}`
}

func testAIConfig(baseURL, model string) aiConfig {
	return aiConfig{BaseURL: baseURL, Model: model, HistoryKey: baseURL + "/" + model, Provider: providerOllama, UseNativeCaching: true}
}

func TestRun(t *testing.T) {
	t.Setenv("ASH_VERBOSE", "")
	// This suite exercises the legacy Ollama chat-format test doubles below, so it
	// explicitly disables both flags rather than relying on their (now-on) defaults.
	t.Setenv("ASH_STREAM", "0")
	t.Setenv(ashEnvAlwaysOpenAIAPI, "0")
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalCwd)
	})

	t.Run("missing args", func(t *testing.T) {
		origStdinInteractive := stdinIsInteractive
		t.Cleanup(func() { stdinIsInteractive = origStdinInteractive })
		stdinIsInteractive = func() bool { return true }

		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := run(nil, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
		if !strings.Contains(stderr.String(), "usage: ash") {
			t.Fatalf("expected usage message, got %q", stderr.String())
		}
		if strings.Contains(stderr.String(), "[EID=") {
			t.Fatalf("expected usage output without EIDs, got %q", stderr.String())
		}
	})

	t.Run("no args reads piped stdin", func(t *testing.T) {
		origStdinInteractive := stdinIsInteractive
		origReadPromptFromStdin := readPromptFromStdin
		t.Cleanup(func() {
			stdinIsInteractive = origStdinInteractive
			readPromptFromStdin = origReadPromptFromStdin
		})
		stdinIsInteractive = func() bool { return false }
		readPromptFromStdin = func() (string, error) { return "  prompt from stdin  ", nil }

		var gotReq chatRequest
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotReq)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"ok"}}`))
		}))
		defer srv.Close()

		t.Setenv("AI", "")
		t.Setenv("AI_ENDPOINT", srv.URL)
		t.Setenv("AI_MODEL", "llama3.1")

		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := run(nil, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected exit code 0, got %d stderr=%q", code, stderr.String())
		}
		if len(gotReq.Messages) == 0 {
			t.Fatalf("expected request messages")
		}
		last := gotReq.Messages[len(gotReq.Messages)-1]
		if last.Role != "user" || last.Content != "prompt from stdin" {
			t.Fatalf("expected stdin prompt as final user message, got role=%q content=%q", last.Role, last.Content)
		}
	})

	t.Run("no args with empty piped stdin returns empty input", func(t *testing.T) {
		origStdinInteractive := stdinIsInteractive
		origReadPromptFromStdin := readPromptFromStdin
		t.Cleanup(func() {
			stdinIsInteractive = origStdinInteractive
			readPromptFromStdin = origReadPromptFromStdin
		})
		stdinIsInteractive = func() bool { return false }
		readPromptFromStdin = func() (string, error) { return "   \n\t", nil }

		t.Setenv("AI", "")
		t.Setenv("AI_ENDPOINT", "http://localhost:11434")
		t.Setenv("AI_MODEL", "llama3.1")

		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := run(nil, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
		if !strings.Contains(stderr.String(), "empty input") {
			t.Fatalf("expected empty input error, got %q", stderr.String())
		}
	})

	t.Run("args take priority over piped stdin", func(t *testing.T) {
		origReadPromptFromStdin := readPromptFromStdin
		t.Cleanup(func() { readPromptFromStdin = origReadPromptFromStdin })
		readPromptFromStdin = func() (string, error) {
			t.Fatalf("stdin should not be read when args are provided")
			return "", nil
		}

		var gotReq chatRequest
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotReq)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"ok"}}`))
		}))
		defer srv.Close()

		t.Setenv("AI", "")
		t.Setenv("AI_ENDPOINT", srv.URL)
		t.Setenv("AI_MODEL", "llama3.1")

		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := run([]string{"prompt", "from", "args"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected exit code 0, got %d stderr=%q", code, stderr.String())
		}
		if len(gotReq.Messages) == 0 {
			t.Fatalf("expected request messages")
		}
		last := gotReq.Messages[len(gotReq.Messages)-1]
		if last.Role != "user" || last.Content != "prompt from args" {
			t.Fatalf("expected argv prompt as final user message, got role=%q content=%q", last.Role, last.Content)
		}
	})

	t.Run("missing AI env", func(t *testing.T) {
		t.Setenv("AI", "")
		t.Setenv("AI_ENDPOINT", "")
		t.Setenv("AI_MODEL", "")
		t.Setenv("SHELL", "/bin/bash")
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := run([]string{"hello"}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
		if !strings.Contains(stderr.String(), "Please add these lines to your ~/.bashrc file") {
			t.Fatalf("expected AI env error, got %q", stderr.String())
		}
	})

	t.Run("legacy AI env is rejected", func(t *testing.T) {
		t.Setenv("AI", "ollama://localhost/llama3.1")
		t.Setenv("AI_ENDPOINT", "")
		t.Setenv("AI_MODEL", "")
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := run([]string{"hello"}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
		if !strings.Contains(stderr.String(), "AI is no longer supported") {
			t.Fatalf("expected invalid AI error, got %q", stderr.String())
		}
	})

	t.Run("empty input", func(t *testing.T) {
		t.Setenv("AI", "")
		t.Setenv("AI_ENDPOINT", "http://localhost:11434")
		t.Setenv("AI_MODEL", "llama3.1")
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := run([]string{"   "}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
		if !strings.Contains(stderr.String(), "empty input") {
			t.Fatalf("expected empty input error, got %q", stderr.String())
		}
	})

	t.Run("load history error", func(t *testing.T) {
		home := t.TempDir()
		cwd := t.TempDir()
		t.Setenv("HOME", home)
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("Chdir failed: %v", err)
		}
		t.Setenv("SESSION_ID", "historyError")
		path := filepath.Join(home, ashWorkspaceDirName, historyDirName, "historyError.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("mkdir history dir: %v", err)
		}
		if err := os.WriteFile(path, []byte("not-json"), 0o600); err != nil {
			t.Fatalf("write bad history: %v", err)
		}

		t.Setenv("AI", "")
		t.Setenv("AI_ENDPOINT", "http://localhost:11434")
		t.Setenv("AI_MODEL", "llama3.1")
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := run([]string{"hello"}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
		if !strings.Contains(stderr.String(), "failed to load history") {
			t.Fatalf("expected load history error, got %q", stderr.String())
		}
	})

	t.Run("chat request error", func(t *testing.T) {
		home := t.TempDir()
		cwd := t.TempDir()
		t.Setenv("HOME", home)
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("Chdir failed: %v", err)
		}

		t.Setenv("AI", "")
		t.Setenv("AI_ENDPOINT", "http://127.0.0.1:1")
		t.Setenv("AI_MODEL", "llama3.1")
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := run([]string{"hello"}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
		if !strings.Contains(stderr.String(), "ollama request failed") {
			t.Fatalf("expected request failure, got %q", stderr.String())
		}
	})

	t.Run("cloud 503 shows playful busy message", func(t *testing.T) {
		home := t.TempDir()
		cwd := t.TempDir()
		t.Setenv("HOME", home)
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("Chdir failed: %v", err)
		}

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "upstream overloaded", http.StatusServiceUnavailable)
		}))
		defer srv.Close()

		origPicker := pickCloudBusy503Message
		t.Cleanup(func() { pickCloudBusy503Message = origPicker })
		pickCloudBusy503Message = func() string { return "cloud test fallback message" }

		t.Setenv("AI", "")
		t.Setenv("AI_ENDPOINT", srv.URL)
		t.Setenv("AI_MODEL", "llama3.1")
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := run([]string{"hello"}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
		if !strings.Contains(stderr.String(), "cloud test fallback message") {
			t.Fatalf("expected playful 503 fallback, got %q", stderr.String())
		}
		if strings.Contains(stderr.String(), "ollama request failed") {
			t.Fatalf("expected dedicated 503 message, got %q", stderr.String())
		}
	})

	t.Run("cloud 500 shows playful server message", func(t *testing.T) {
		home := t.TempDir()
		cwd := t.TempDir()
		t.Setenv("HOME", home)
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("Chdir failed: %v", err)
		}

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}))
		defer srv.Close()

		origPicker := pickCloudServer500Message
		t.Cleanup(func() { pickCloudServer500Message = origPicker })
		pickCloudServer500Message = func() string { return "cloud 500 test fallback message" }

		t.Setenv("AI", "")
		t.Setenv("AI_ENDPOINT", srv.URL)
		t.Setenv("AI_MODEL", "llama3.1")
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := run([]string{"hello"}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
		if !strings.Contains(stderr.String(), "cloud 500 test fallback message") {
			t.Fatalf("expected playful 500 fallback, got %q", stderr.String())
		}
		if strings.Contains(stderr.String(), "ollama request failed") {
			t.Fatalf("expected dedicated 500 message, got %q", stderr.String())
		}
	})

	t.Run("cloud 429 shows playful rate limit message", func(t *testing.T) {
		home := t.TempDir()
		cwd := t.TempDir()
		t.Setenv("HOME", home)
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("Chdir failed: %v", err)
		}

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "quota exceeded", http.StatusTooManyRequests)
		}))
		defer srv.Close()

		origPicker := pickCloudRateLimit429Message
		t.Cleanup(func() { pickCloudRateLimit429Message = origPicker })
		pickCloudRateLimit429Message = func() string { return "cloud 429 test fallback message" }

		t.Setenv("AI", "")
		t.Setenv("AI_ENDPOINT", srv.URL)
		t.Setenv("AI_MODEL", "llama3.1")
		t.Setenv("ASH_RETRY_BASE_DELAY", "1ms")
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := run([]string{"hello"}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
		if !strings.Contains(stderr.String(), "cloud 429 test fallback message") {
			t.Fatalf("expected playful 429 fallback, got %q", stderr.String())
		}
		if !strings.Contains(stderr.String(), "server said: 429") {
			t.Fatalf("expected status detail in 429 message, got %q", stderr.String())
		}
		if !strings.Contains(stderr.String(), "quota exceeded") {
			t.Fatalf("expected server body in 429 message, got %q", stderr.String())
		}
		if strings.Contains(stderr.String(), "ollama request failed") {
			t.Fatalf("expected dedicated 429 message, got %q", stderr.String())
		}
	})

	t.Run("success stores raw history", func(t *testing.T) {
		home := t.TempDir()
		cwd := t.TempDir()
		t.Setenv("HOME", home)
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("Chdir failed: %v", err)
		}

		assistantRaw := "**bold** 🙂"
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"` + assistantRaw + `"}}`))
		}))
		defer srv.Close()

		t.Setenv("AI", "")
		t.Setenv("AI_ENDPOINT", srv.URL)
		t.Setenv("AI_MODEL", "llama3.1")

		originalRenderer := markdownRenderer
		t.Cleanup(func() { markdownRenderer = originalRenderer })
		markdownRenderer = func(input string) (string, error) {
			if input != assistantRaw {
				t.Fatalf("renderer input mismatch: got %q want %q", input, assistantRaw)
			}
			return "\x1b[1mbold 🙂\x1b[0m", nil
		}

		t.Setenv("SESSION_ID", "historySuccess")

		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := run([]string{"show", "files"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected exit code 0, got %d stderr=%q", code, stderr.String())
		}
		if stdout.String() != assistantRaw+"\n" {
			t.Fatalf("expected raw markdown on non-terminal stdout, got %q", stdout.String())
		}

		content, err := os.ReadFile(filepath.Join(home, ashWorkspaceDirName, historyDirName, "historySuccess.json"))
		if err != nil {
			t.Fatalf("read history file: %v", err)
		}
		if strings.Contains(string(content), "\x1b[1m") {
			t.Fatalf("history should not include ANSI escapes: %q", string(content))
		}
		if !strings.Contains(string(content), assistantRaw) {
			t.Fatalf("history should keep raw assistant markdown, got %q", string(content))
		}
	})

	t.Run("save history warning does not fail run", func(t *testing.T) {
		home := t.TempDir()
		cwd := t.TempDir()
		t.Setenv("HOME", home)
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("Chdir failed: %v", err)
		}

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"ok"}}`))
		}))
		defer srv.Close()

		t.Setenv("AI", "")
		t.Setenv("AI_ENDPOINT", srv.URL)
		t.Setenv("AI_MODEL", "llama3.1")

		originalWrite := osWriteFile
		t.Cleanup(func() { osWriteFile = originalWrite })
		osWriteFile = func(string, []byte, os.FileMode) error { return errors.New("disk full") }

		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := run([]string{"hello"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected exit code 0, got %d", code)
		}
		if !strings.Contains(stderr.String(), "warning: failed to save history") {
			t.Fatalf("expected save warning, got %q", stderr.String())
		}
	})

	t.Run("read system prompt failure", func(t *testing.T) {
		home := t.TempDir()
		cwd := t.TempDir()
		t.Setenv("HOME", home)
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("Chdir failed: %v", err)
		}

		origRead := osReadFile
		t.Cleanup(func() { osReadFile = origRead })
		osReadFile = func(path string) ([]byte, error) {
			if strings.HasSuffix(path, systemFileName) {
				return nil, errors.New("permission denied")
			}
			return origRead(path)
		}

		t.Setenv("AI", "")
		t.Setenv("AI_ENDPOINT", "http://localhost:11434")
		t.Setenv("AI_MODEL", "llama3.1")
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := run([]string{"hello"}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
		if !strings.Contains(stderr.String(), "failed to read") {
			t.Fatalf("expected read failure, got %q", stderr.String())
		}
	})

	t.Run("resolve history path failure", func(t *testing.T) {
		home := t.TempDir()
		cwd := t.TempDir()
		t.Setenv("HOME", home)
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("Chdir failed: %v", err)
		}
		if err := os.WriteFile(filepath.Join(cwd, systemFileName), []byte("system"), 0o600); err != nil {
			t.Fatalf("write system file: %v", err)
		}

		origHome := osUserHomeDir
		t.Cleanup(func() { osUserHomeDir = origHome })
		osUserHomeDir = func() (string, error) { return "", errors.New("no home") }

		t.Setenv("AI", "")
		t.Setenv("AI_ENDPOINT", "http://localhost:11434")
		t.Setenv("AI_MODEL", "llama3.1")
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := run([]string{"hello"}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
		if !strings.Contains(stderr.String(), "failed to resolve history path") {
			t.Fatalf("expected history path failure, got %q", stderr.String())
		}
	})

	t.Run("system prompt is sent in chat request", func(t *testing.T) {
		origTimeNow := timeNow
		timeNow = func() time.Time {
			return time.Date(2026, time.July, 24, 7, 0, 0, 0, time.FixedZone("PDT", -7*3600))
		}
		t.Cleanup(func() { timeNow = origTimeNow })

		home := t.TempDir()
		cwd := t.TempDir()
		t.Setenv("HOME", home)
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("Chdir failed: %v", err)
		}
		if err := os.WriteFile(filepath.Join(cwd, systemFileName), []byte("sys-msg"), 0o600); err != nil {
			t.Fatalf("write system file: %v", err)
		}

		var gotReq chatRequest
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotReq)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"ok"}}`))
		}))
		defer srv.Close()

		t.Setenv("AI", "")
		t.Setenv("AI_ENDPOINT", srv.URL)
		t.Setenv("AI_MODEL", "llama3.1")

		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := run([]string{"hello"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected exit code 0, got %d stderr=%q", code, stderr.String())
		}
		if len(gotReq.Messages) == 0 || gotReq.Messages[0].Role != "system" {
			t.Fatalf("expected first message to be system prompt, got %#v", gotReq.Messages)
		}
		if !strings.Contains(gotReq.Messages[0].Content, "sys-msg") {
			t.Fatalf("expected sys-msg in system prompt, got %q", gotReq.Messages[0].Content)
		}
		if !strings.HasSuffix(gotReq.Messages[0].Content, "sys-msg") {
			t.Fatalf("expected original system prompt suffix, got %q", gotReq.Messages[0].Content)
		}
	})
}

func TestMainHelperProcess(t *testing.T) {
	if os.Getenv("ASH_MAIN_HELPER") != "1" {
		return
	}
	os.Args = []string{"ash", "hello"}
	os.Exit(Run(os.Args[1:], os.Stdout, os.Stderr))
}

func TestMainEntrypoint(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, systemFileName), []byte("sys"), 0o600); err != nil {
		t.Fatalf("write system file: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"ok"}}`))
	}))
	defer srv.Close()

	cmd := exec.CommandContext(context.Background(), os.Args[0], "-test.run=TestMainHelperProcess")
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(),
		"ASH_MAIN_HELPER=1",
		"AI=",
		"AI_ENDPOINT="+srv.URL,
		"AI_MODEL=llama3.1",
		"HOME="+home,
	)

	if err := cmd.Run(); err != nil {
		t.Fatalf("main helper process failed: %v", err)
	}
}

func FuzzEnsureSingleTrailingNewline(f *testing.F) {
	f.Add("")
	f.Add("hello")
	f.Add("hello\n\n")
	f.Add("🙂 markdown **bold**")
	f.Add("# hidden\nvisible\n  # preserved\n")

	f.Fuzz(func(t *testing.T, input string) {
		out := ensureSingleTrailingNewline(input)
		if !strings.HasSuffix(out, "\n") {
			t.Fatalf("expected trailing newline for %q", input)
		}
		if strings.HasSuffix(out, "\n\n") {
			t.Fatalf("expected exactly one trailing newline for %q, got %q", input, out)
		}
		for _, line := range strings.Split(stripSystemPromptComments(input), "\n") {
			if strings.HasPrefix(line, "#") {
				t.Fatalf("comment line was not stripped: %q", line)
			}
		}
	})
}
