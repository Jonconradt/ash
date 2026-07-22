package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/glamour"
)

func TestParseAI(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		wantBaseURL    string
		wantModel      string
		wantHistoryKey string
		wantErr        string
	}{
		{
			name:           "valid with default port",
			input:          "ollama://localhost/llama3.1",
			wantBaseURL:    "http://localhost:11434",
			wantModel:      "llama3.1",
			wantHistoryKey: "http://localhost:11434/llama3.1",
		},
		{
			name:           "valid with explicit port",
			input:          "ollama://example.com:1234/mistral",
			wantBaseURL:    "http://example.com:1234",
			wantModel:      "mistral",
			wantHistoryKey: "http://example.com:1234/mistral",
		},
		{
			name:    "invalid scheme",
			input:   "http://localhost/llama3.1",
			wantErr: "scheme must be ollama",
		},
		{
			name:    "missing host",
			input:   "ollama:///llama3.1",
			wantErr: "host is required",
		},
		{
			name:    "missing model",
			input:   "ollama://localhost",
			wantErr: "model is required in path",
		},
		{
			name:    "invalid URI parse",
			input:   "://bad",
			wantErr: "missing protocol scheme",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseURL, model, historyKey, err := parseAI(tt.input)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("parseAI returned unexpected error: %v", err)
			}
			if baseURL != tt.wantBaseURL {
				t.Fatalf("baseURL mismatch: got %q want %q", baseURL, tt.wantBaseURL)
			}
			if model != tt.wantModel {
				t.Fatalf("model mismatch: got %q want %q", model, tt.wantModel)
			}
			if historyKey != tt.wantHistoryKey {
				t.Fatalf("historyKey mismatch: got %q want %q", historyKey, tt.wantHistoryKey)
			}
		})
	}
}

func TestReadSystemPrompt(t *testing.T) {
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalCwd)
	})

	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}

	homePrompt := "home prompt"
	if err := os.WriteFile(filepath.Join(home, systemFileName), []byte(homePrompt), 0o600); err != nil {
		t.Fatalf("write home prompt: %v", err)
	}

	prompt, err := readSystemPrompt()
	if err != nil {
		t.Fatalf("readSystemPrompt error: %v", err)
	}
	if prompt != homePrompt {
		t.Fatalf("expected home prompt, got %q", prompt)
	}

	cwdPrompt := "cwd prompt"
	if err := os.WriteFile(filepath.Join(cwd, systemFileName), []byte(cwdPrompt), 0o600); err != nil {
		t.Fatalf("write cwd prompt: %v", err)
	}

	prompt, err = readSystemPrompt()
	if err != nil {
		t.Fatalf("readSystemPrompt error: %v", err)
	}
	if prompt != cwdPrompt {
		t.Fatalf("expected cwd prompt, got %q", prompt)
	}
}

func TestReadSystemPromptExpandsEnvironmentVariables(t *testing.T) {
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalCwd)
	})

	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ASH_TEST_ONE", "first")
	t.Setenv("ASH_TEST_TWO", "second")

	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}

	content := "one=$ASH_TEST_ONE two=${ASH_TEST_TWO} missing=$ASH_TEST_MISSING"
	if err := os.WriteFile(filepath.Join(cwd, systemFileName), []byte(content), 0o600); err != nil {
		t.Fatalf("write cwd prompt: %v", err)
	}

	prompt, err := readSystemPrompt()
	if err != nil {
		t.Fatalf("readSystemPrompt error: %v", err)
	}

	want := "one=first two=second missing="
	if prompt != want {
		t.Fatalf("expanded prompt mismatch: got %q want %q", prompt, want)
	}
}

func TestReadSystemPromptExpandsUname(t *testing.T) {
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalCwd)
	})

	origLookPath := execLookPath
	origCommandOutput := execCommandOutput
	t.Cleanup(func() {
		execLookPath = origLookPath
		execCommandOutput = origCommandOutput
	})

	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("UNAME", "env-uname")

	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}

	if err := os.WriteFile(filepath.Join(cwd, systemFileName), []byte("host=$UNAME"), 0o600); err != nil {
		t.Fatalf("write cwd prompt: %v", err)
	}

	execLookPath = func(file string) (string, error) {
		if file != "uname" {
			t.Fatalf("unexpected lookpath query: %q", file)
		}
		return "/usr/bin/uname", nil
	}
	execCommandOutput = func(name string, args ...string) ([]byte, error) {
		if name != "uname" {
			t.Fatalf("unexpected command name: %q", name)
		}
		if len(args) != 1 || args[0] != "-a" {
			t.Fatalf("unexpected command args: %#v", args)
		}
		return []byte("Test Kernel 1.0\n"), nil
	}

	prompt, err := readSystemPrompt()
	if err != nil {
		t.Fatalf("readSystemPrompt error: %v", err)
	}

	want := "host=Test Kernel 1.0"
	if prompt != want {
		t.Fatalf("expanded prompt mismatch: got %q want %q", prompt, want)
	}
}

func TestReadSystemPromptErrors(t *testing.T) {
	origGetwd := osGetwd
	origHome := osUserHomeDir
	origReadFile := osReadFile
	t.Cleanup(func() {
		osGetwd = origGetwd
		osUserHomeDir = origHome
		osReadFile = origReadFile
	})

	t.Run("getwd error", func(t *testing.T) {
		osGetwd = func() (string, error) { return "", errors.New("cwd fail") }
		_, err := readSystemPrompt()
		if err == nil || !strings.Contains(err.Error(), "cwd fail") {
			t.Fatalf("expected cwd fail error, got %v", err)
		}
		osGetwd = origGetwd
	})

	t.Run("cwd read unexpected error", func(t *testing.T) {
		osGetwd = func() (string, error) { return "/tmp", nil }
		osReadFile = func(string) ([]byte, error) { return nil, errors.New("read fail") }
		_, err := readSystemPrompt()
		if err == nil || !strings.Contains(err.Error(), "read fail") {
			t.Fatalf("expected read fail error, got %v", err)
		}
		osGetwd = origGetwd
		osReadFile = origReadFile
	})

	t.Run("home dir error", func(t *testing.T) {
		osGetwd = func() (string, error) { return "/tmp", nil }
		osReadFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
		osUserHomeDir = func() (string, error) { return "", errors.New("home fail") }
		_, err := readSystemPrompt()
		if err == nil || !strings.Contains(err.Error(), "home fail") {
			t.Fatalf("expected home fail error, got %v", err)
		}
		osGetwd = origGetwd
		osReadFile = origReadFile
		osUserHomeDir = origHome
	})

	t.Run("home read unexpected error", func(t *testing.T) {
		calls := 0
		osGetwd = func() (string, error) { return "/tmp", nil }
		osUserHomeDir = func() (string, error) { return "/home/test", nil }
		osReadFile = func(path string) ([]byte, error) {
			calls++
			if calls == 1 {
				return nil, os.ErrNotExist
			}
			return nil, errors.New("home read fail")
		}
		_, err := readSystemPrompt()
		if err == nil || !strings.Contains(err.Error(), "home read fail") {
			t.Fatalf("expected home read fail error, got %v", err)
		}
		osGetwd = origGetwd
		osReadFile = origReadFile
		osUserHomeDir = origHome
	})
}

func TestGetHistoryPath(t *testing.T) {
	origHome := osUserHomeDir
	t.Cleanup(func() { osUserHomeDir = origHome })

	home := t.TempDir()
	osUserHomeDir = func() (string, error) { return home, nil }

	path, err := getHistoryPath()
	if err != nil {
		t.Fatalf("getHistoryPath returned error: %v", err)
	}

	want := filepath.Join(home, historyFileName)
	if path != want {
		t.Fatalf("path mismatch: got %q want %q", path, want)
	}
}

func TestGetHistoryPathError(t *testing.T) {
	origHome := osUserHomeDir
	t.Cleanup(func() { osUserHomeDir = origHome })

	osUserHomeDir = func() (string, error) { return "", errors.New("no home") }
	_, err := getHistoryPath()
	if err == nil || !strings.Contains(err.Error(), "no home") {
		t.Fatalf("expected no home error, got %v", err)
	}
}

func TestAITimeout(t *testing.T) {
	t.Run("configured duration", func(t *testing.T) {
		t.Setenv("AI_TIMEOUT", "45s")
		if got := aiTimeout(); got != 45*time.Second {
			t.Fatalf("aiTimeout mismatch: got %s want %s", got, 45*time.Second)
		}
	})

	t.Run("invalid falls back", func(t *testing.T) {
		t.Setenv("AI_TIMEOUT", "not-a-duration")
		if got := aiTimeout(); got != defaultAITimeout {
			t.Fatalf("aiTimeout fallback mismatch: got %s want %s", got, defaultAITimeout)
		}
	})
}

func TestLoadHistoryNotFound(t *testing.T) {
	origReadFile := osReadFile
	t.Cleanup(func() { osReadFile = origReadFile })

	path := filepath.Join(t.TempDir(), "missing.json")
	osReadFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	data, err := loadHistory(path)
	if err != nil {
		t.Fatalf("loadHistory returned error: %v", err)
	}
	if data.Conversations == nil {
		t.Fatalf("expected initialized conversations map")
	}
	if len(data.Conversations) != 0 {
		t.Fatalf("expected empty conversations map, got %d entries", len(data.Conversations))
	}
}

func TestLoadHistoryReadError(t *testing.T) {
	origReadFile := osReadFile
	t.Cleanup(func() { osReadFile = origReadFile })

	osReadFile = func(string) ([]byte, error) { return nil, errors.New("read failed") }
	_, err := loadHistory("ignored")
	if err == nil || !strings.Contains(err.Error(), "read failed") {
		t.Fatalf("expected read failed error, got %v", err)
	}
}

func TestLoadHistoryUnmarshalError(t *testing.T) {
	origReadFile := osReadFile
	t.Cleanup(func() { osReadFile = origReadFile })

	osReadFile = func(string) ([]byte, error) { return []byte("not-json"), nil }
	_, err := loadHistory("ignored")
	if err == nil {
		t.Fatalf("expected unmarshal error")
	}
}

func TestLoadHistoryInitializesNilMap(t *testing.T) {
	origReadFile := osReadFile
	t.Cleanup(func() { osReadFile = origReadFile })

	osReadFile = func(string) ([]byte, error) { return []byte(`{"conversations":null}`), nil }
	data, err := loadHistory("ignored")
	if err != nil {
		t.Fatalf("loadHistory returned error: %v", err)
	}
	if data.Conversations == nil {
		t.Fatalf("expected non-nil conversations map")
	}
}

func TestSaveAndLoadHistoryRoundTrip(t *testing.T) {
	origReadFile := osReadFile
	origWriteFile := osWriteFile
	t.Cleanup(func() {
		osReadFile = origReadFile
		osWriteFile = origWriteFile
	})

	path := filepath.Join(t.TempDir(), "history.json")
	input := historyData{
		Conversations: map[string][]message{
			"k": {
				{Role: "user", Content: "u"},
				{Role: "assistant", Content: "a"},
			},
		},
	}

	if err := saveHistory(path, input); err != nil {
		t.Fatalf("saveHistory returned error: %v", err)
	}

	output, err := loadHistory(path)
	if err != nil {
		t.Fatalf("loadHistory returned error: %v", err)
	}

	if !reflect.DeepEqual(input, output) {
		inJSON, _ := json.Marshal(input)
		outJSON, _ := json.Marshal(output)
		t.Fatalf("history mismatch: got %s want %s", outJSON, inJSON)
	}
}

func TestSaveHistoryWriteError(t *testing.T) {
	origWriteFile := osWriteFile
	t.Cleanup(func() { osWriteFile = origWriteFile })

	osWriteFile = func(string, []byte, os.FileMode) error { return errors.New("write failed") }
	err := saveHistory("ignored", historyData{Conversations: map[string][]message{}})
	if err == nil || !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("expected write failed error, got %v", err)
	}
}

func TestHistoryLimit(t *testing.T) {
	t.Setenv("ASH_HISTORY_MAX", "")
	if got := historyLimit(); got != defaultHistoryMax {
		t.Fatalf("default limit mismatch: got %d want %d", got, defaultHistoryMax)
	}

	t.Setenv("ASH_HISTORY_MAX", "80")
	if got := historyLimit(); got != 80 {
		t.Fatalf("env limit mismatch: got %d want 80", got)
	}

	t.Setenv("ASH_HISTORY_MAX", "not-a-number")
	if got := historyLimit(); got != defaultHistoryMax {
		t.Fatalf("invalid env should fallback: got %d want %d", got, defaultHistoryMax)
	}

	t.Setenv("ASH_HISTORY_MAX", "0")
	if got := historyLimit(); got != defaultHistoryMax {
		t.Fatalf("non-positive env should fallback: got %d want %d", got, defaultHistoryMax)
	}
}

func TestKeepRecentMessages(t *testing.T) {
	messages := []message{
		{Role: "1", Content: "1"},
		{Role: "2", Content: "2"},
		{Role: "3", Content: "3"},
	}

	keptAll := keepRecentMessages(messages, 5)
	if !reflect.DeepEqual(keptAll, messages) {
		t.Fatalf("expected all messages to be kept")
	}

	trimmed := keepRecentMessages(messages, 2)
	want := []message{
		{Role: "2", Content: "2"},
		{Role: "3", Content: "3"},
	}
	if !reflect.DeepEqual(trimmed, want) {
		t.Fatalf("trimmed mismatch: got %#v want %#v", trimmed, want)
	}
}

func TestEnsureSingleTrailingNewline(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: "\n"},
		{name: "no newline", in: "hello", want: "hello\n"},
		{name: "one newline", in: "hello\n", want: "hello\n"},
		{name: "many newlines", in: "hello\n\n", want: "hello\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ensureSingleTrailingNewline(tt.in)
			if got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestFormatAssistantOutputUsesRenderer(t *testing.T) {
	original := markdownRenderer
	t.Cleanup(func() { markdownRenderer = original })

	markdownRenderer = func(input string) (string, error) {
		if input != "# title" {
			t.Fatalf("unexpected renderer input: %q", input)
		}
		return "styled\n\n", nil
	}

	got := formatAssistantOutput("# title")
	if got != "styled\n" {
		t.Fatalf("output mismatch: got %q want %q", got, "styled\\n")
	}
}

func TestFormatAssistantOutputFallbackOnRendererError(t *testing.T) {
	original := markdownRenderer
	t.Cleanup(func() { markdownRenderer = original })

	markdownRenderer = func(string) (string, error) {
		return "", errors.New("boom")
	}

	got := formatAssistantOutput("**raw** 🙂")
	want := "**raw** 🙂\n"
	if got != want {
		t.Fatalf("fallback mismatch: got %q want %q", got, want)
	}
}

func TestChat(t *testing.T) {
	origClientFactory := newHTTPClient
	t.Cleanup(func() { newHTTPClient = origClientFactory })

	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/chat" {
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"ok"}}`))
		}))
		defer srv.Close()

		got, err := chat(context.Background(), srv.URL, "model", []message{{Role: "user", Content: "hi"}})
		if err != nil {
			t.Fatalf("chat returned error: %v", err)
		}
		if got != "ok" {
			t.Fatalf("chat content mismatch: got %q want %q", got, "ok")
		}
	})

	t.Run("status error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", http.StatusBadRequest)
		}))
		defer srv.Close()

		_, err := chat(context.Background(), srv.URL, "model", []message{{Role: "user", Content: "hi"}})
		if err == nil {
			t.Fatalf("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "status 400") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("api error field", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":""},"error":"model overloaded"}`))
		}))
		defer srv.Close()

		_, err := chat(context.Background(), srv.URL, "model", []message{{Role: "user", Content: "hi"}})
		if err == nil {
			t.Fatalf("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "model overloaded") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{not-json`))
		}))
		defer srv.Close()

		_, err := chat(context.Background(), srv.URL, "model", []message{{Role: "user", Content: "hi"}})
		if err == nil {
			t.Fatalf("expected error, got nil")
		}
	})

	t.Run("context canceled", func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(started)
			<-release
		}))
		defer srv.Close()

		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() {
			_, err := chat(ctx, srv.URL, "model", []message{{Role: "user", Content: "hi"}})
			result <- err
		}()

		<-started
		cancel()
		close(release)

		err := <-result
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	})

	t.Run("client timeout", func(t *testing.T) {
		var timeoutSeen atomic.Int64
		newHTTPClient = func(timeout time.Duration) *http.Client {
			timeoutSeen.Store(int64(timeout))
			return &http.Client{Timeout: timeout}
		}
		t.Setenv("AI_TIMEOUT", "20ms")

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(100 * time.Millisecond)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"slow"}}`))
		}))
		defer srv.Close()

		_, err := chat(context.Background(), srv.URL, "model", []message{{Role: "user", Content: "hi"}})
		if err == nil {
			t.Fatalf("expected timeout error, got nil")
		}
		if timeoutSeen.Load() != int64(20*time.Millisecond) {
			t.Fatalf("expected client timeout %s, got %s", 20*time.Millisecond, time.Duration(timeoutSeen.Load()))
		}
		newHTTPClient = origClientFactory
	})
}

func TestStartThinkingIndicator(t *testing.T) {
	var output bytes.Buffer
	stop := startThinkingIndicator(&output)
	time.Sleep(150 * time.Millisecond)
	stop()

	got := output.String()
	if !strings.Contains(got, "Thinking...") {
		t.Fatalf("expected thinking indicator output, got %q", got)
	}
	if !strings.Contains(got, "\r") {
		t.Fatalf("expected carriage return output, got %q", got)
	}
}

func TestRenderMarkdownWithGlamourEmojiPassthrough(t *testing.T) {
	originalFactory := newTermRenderer
	t.Cleanup(func() { newTermRenderer = originalFactory })

	out, err := renderMarkdownWithGlamour("**bold** 🙂")
	if err != nil {
		t.Fatalf("renderMarkdownWithGlamour returned error: %v", err)
	}
	if !strings.Contains(out, "🙂") {
		t.Fatalf("expected emoji passthrough, output: %q", out)
	}
}

func TestRenderMarkdownWithGlamourFactoryError(t *testing.T) {
	originalFactory := newTermRenderer
	t.Cleanup(func() { newTermRenderer = originalFactory })

	newTermRenderer = func(...glamour.TermRendererOption) (*glamour.TermRenderer, error) {
		return nil, fmt.Errorf("factory failed")
	}

	_, err := renderMarkdownWithGlamour("x")
	if err == nil || !strings.Contains(err.Error(), "factory failed") {
		t.Fatalf("expected factory failed error, got %v", err)
	}
}

func TestRun(t *testing.T) {
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalCwd)
	})

	t.Run("missing args", func(t *testing.T) {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := run(nil, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
		if !strings.Contains(stderr.String(), "usage: ash") {
			t.Fatalf("expected usage message, got %q", stderr.String())
		}
	})

	t.Run("missing AI env", func(t *testing.T) {
		t.Setenv("AI", "")
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := run([]string{"hello"}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
		if !strings.Contains(stderr.String(), "AI environment variable is required") {
			t.Fatalf("expected AI env error, got %q", stderr.String())
		}
	})

	t.Run("invalid AI env", func(t *testing.T) {
		t.Setenv("AI", "http://localhost/llama3.1")
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := run([]string{"hello"}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
		if !strings.Contains(stderr.String(), "invalid AI value") {
			t.Fatalf("expected invalid AI error, got %q", stderr.String())
		}
	})

	t.Run("empty input", func(t *testing.T) {
		t.Setenv("AI", "ollama://localhost/llama3.1")
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
		path := filepath.Join(home, historyFileName)
		if err := os.WriteFile(path, []byte("not-json"), 0o600); err != nil {
			t.Fatalf("write bad history: %v", err)
		}

		t.Setenv("AI", "ollama://localhost/llama3.1")
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

		t.Setenv("AI", "ollama://127.0.0.1:1/llama3.1")
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

		u, err := url.Parse(srv.URL)
		if err != nil {
			t.Fatalf("parse server url: %v", err)
		}
		t.Setenv("AI", "ollama://"+u.Host+"/llama3.1")

		originalRenderer := markdownRenderer
		t.Cleanup(func() { markdownRenderer = originalRenderer })
		markdownRenderer = func(input string) (string, error) {
			if input != assistantRaw {
				t.Fatalf("renderer input mismatch: got %q want %q", input, assistantRaw)
			}
			return "\x1b[1mbold 🙂\x1b[0m", nil
		}

		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := run([]string{"show", "files"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected exit code 0, got %d stderr=%q", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "\x1b[1m") {
			t.Fatalf("expected ANSI output, got %q", stdout.String())
		}

		content, err := os.ReadFile(filepath.Join(home, historyFileName))
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

		u, err := url.Parse(srv.URL)
		if err != nil {
			t.Fatalf("parse server url: %v", err)
		}
		t.Setenv("AI", "ollama://"+u.Host+"/llama3.1")

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

		t.Setenv("AI", "ollama://localhost/llama3.1")
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

		t.Setenv("AI", "ollama://localhost/llama3.1")
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

		u, err := url.Parse(srv.URL)
		if err != nil {
			t.Fatalf("parse server url: %v", err)
		}
		t.Setenv("AI", "ollama://"+u.Host+"/llama3.1")

		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := run([]string{"hello"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected exit code 0, got %d stderr=%q", code, stderr.String())
		}
		if len(gotReq.Messages) == 0 || gotReq.Messages[0].Role != "system" || gotReq.Messages[0].Content != "sys-msg" {
			t.Fatalf("expected first message to be system prompt, got %#v", gotReq.Messages)
		}
	})
}

func TestMainHelperProcess(t *testing.T) {
	if os.Getenv("ASH_MAIN_HELPER") != "1" {
		return
	}
	os.Args = []string{"ash", "hello"}
	main()
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

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestMainHelperProcess")
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(),
		"ASH_MAIN_HELPER=1",
		"AI=ollama://"+u.Host+"/llama3.1",
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

	f.Fuzz(func(t *testing.T, input string) {
		out := ensureSingleTrailingNewline(input)
		if !strings.HasSuffix(out, "\n") {
			t.Fatalf("expected trailing newline for %q", input)
		}
		if strings.HasSuffix(out, "\n\n") {
			t.Fatalf("expected exactly one trailing newline for %q, got %q", input, out)
		}
	})
}
