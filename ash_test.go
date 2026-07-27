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
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/glamour"
)

func testAIConfig(baseURL, model string) aiConfig {
	return aiConfig{BaseURL: baseURL, Model: model, HistoryKey: baseURL + "/" + model}
}

func TestParseAIConfigFromEnv(t *testing.T) {
	tests := []struct {
		name           string
		env            map[string]string
		wantBaseURL    string
		wantModel      string
		wantHistoryKey string
		wantAuth       string
		wantErr        string
	}{
		{
			name: "local endpoint without auth",
			env: map[string]string{
				"AI_ENDPOINT": "http://localhost:11434",
				"AI_MODEL":    "llama3.1",
			},
			wantBaseURL:    "http://localhost:11434",
			wantModel:      "llama3.1",
			wantHistoryKey: "http://localhost:11434/llama3.1",
		},
		{
			name: "cloud endpoint with bearer auth",
			env: map[string]string{
				"AI_ENDPOINT":   "https://api.example.com/ollama",
				"AI_MODEL":      "mistral",
				"AI_AUTH_TYPE":  "bearer",
				"AI_AUTH_TOKEN": "abc123",
			},
			wantBaseURL:    "https://api.example.com/ollama",
			wantModel:      "mistral",
			wantHistoryKey: "https://api.example.com/ollama/mistral",
			wantAuth:       "Bearer abc123",
		},
		{
			name: "legacy AI env rejected",
			env: map[string]string{
				"AI": "ollama://localhost/llama3.1",
			},
			wantErr: "AI is no longer supported",
		},
		{
			name:    "missing endpoint",
			env:     map[string]string{"AI_MODEL": "llama3.1"},
			wantErr: "AI_ENDPOINT is required",
		},
		{
			name:    "missing model",
			env:     map[string]string{"AI_ENDPOINT": "http://localhost:11434"},
			wantErr: "AI_MODEL is required",
		},
		{
			name: "token without auth type",
			env: map[string]string{
				"AI_ENDPOINT":   "http://localhost:11434",
				"AI_MODEL":      "llama3.1",
				"AI_AUTH_TOKEN": "abc",
			},
			wantErr: "AI_AUTH_TYPE is required when AI_AUTH_TOKEN is set",
		},
		{
			name: "invalid auth type",
			env: map[string]string{
				"AI_ENDPOINT":  "http://localhost:11434",
				"AI_MODEL":     "llama3.1",
				"AI_AUTH_TYPE": "basic",
			},
			wantErr: "AI_AUTH_TYPE must be bearer",
		},
		{
			name: "cloud endpoint requires https",
			env: map[string]string{
				"AI_ENDPOINT":   "http://api.example.com",
				"AI_MODEL":      "llama3.1",
				"AI_AUTH_TYPE":  "bearer",
				"AI_AUTH_TOKEN": "abc",
			},
			wantErr: "AI_ENDPOINT must use https for cloud endpoints",
		},
		{
			name: "cloud endpoint requires auth",
			env: map[string]string{
				"AI_ENDPOINT": "https://api.example.com",
				"AI_MODEL":    "llama3.1",
			},
			wantErr: "cloud endpoints require AI_AUTH_TYPE=bearer and AI_AUTH_TOKEN",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AI", "")
			t.Setenv("AI_ENDPOINT", "")
			t.Setenv("AI_MODEL", "")
			t.Setenv("AI_AUTH_TYPE", "")
			t.Setenv("AI_AUTH_TOKEN", "")
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			cfg, err := parseAIConfigFromEnv()
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
				t.Fatalf("parseAIConfigFromEnv returned unexpected error: %v", err)
			}
			if cfg.BaseURL != tt.wantBaseURL {
				t.Fatalf("baseURL mismatch: got %q want %q", cfg.BaseURL, tt.wantBaseURL)
			}
			if cfg.Model != tt.wantModel {
				t.Fatalf("model mismatch: got %q want %q", cfg.Model, tt.wantModel)
			}
			if cfg.HistoryKey != tt.wantHistoryKey {
				t.Fatalf("historyKey mismatch: got %q want %q", cfg.HistoryKey, tt.wantHistoryKey)
			}
			if cfg.Authorization != tt.wantAuth {
				t.Fatalf("authorization mismatch: got %q want %q", cfg.Authorization, tt.wantAuth)
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

func TestBuildSystemPrompt(t *testing.T) {
	now := time.Date(2026, time.July, 24, 9, 15, 30, 0, time.FixedZone("PDT", -7*3600))

	t.Run("header only when empty prompt", func(t *testing.T) {
		got := buildSystemPrompt("", now)
		want := "Current local datetime: 2026-07-24T09:15:30-07:00"
		if got != want {
			t.Fatalf("unexpected prompt: got %q want %q", got, want)
		}
	})

	t.Run("header plus prompt body", func(t *testing.T) {
		got := buildSystemPrompt("sys-msg", now)
		if !strings.HasPrefix(got, "Current local datetime: 2026-07-24T09:15:30-07:00\n\n") {
			t.Fatalf("expected datetime prefix, got %q", got)
		}
		if !strings.HasSuffix(got, "sys-msg") {
			t.Fatalf("expected user prompt suffix, got %q", got)
		}
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

		got, err := chat(context.Background(), testAIConfig(srv.URL, "model"), []message{{Role: "user", Content: "hi"}}, nil)
		if err != nil {
			t.Fatalf("chat returned error: %v", err)
		}
		if got.Message.Content != "ok" {
			t.Fatalf("chat content mismatch: got %q want %q", got.Message.Content, "ok")
		}
	})

	t.Run("status error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", http.StatusBadRequest)
		}))
		defer srv.Close()

		_, err := chat(context.Background(), testAIConfig(srv.URL, "model"), []message{{Role: "user", Content: "hi"}}, nil)
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

		_, err := chat(context.Background(), testAIConfig(srv.URL, "model"), []message{{Role: "user", Content: "hi"}}, nil)
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

		_, err := chat(context.Background(), testAIConfig(srv.URL, "model"), []message{{Role: "user", Content: "hi"}}, nil)
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
			_, err := chat(ctx, testAIConfig(srv.URL, "model"), []message{{Role: "user", Content: "hi"}}, nil)
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

		_, err := chat(context.Background(), testAIConfig(srv.URL, "model"), []message{{Role: "user", Content: "hi"}}, nil)
		if err == nil {
			t.Fatalf("expected timeout error, got nil")
		}
		if timeoutSeen.Load() != int64(20*time.Millisecond) {
			t.Fatalf("expected client timeout %s, got %s", 20*time.Millisecond, time.Duration(timeoutSeen.Load()))
		}
		newHTTPClient = origClientFactory
	})
}

func TestChatIncludesToolsAndParsesToolCalls(t *testing.T) {
	var gotReq chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotReq)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"run_unix_command","arguments":{"command":"ls"}}}]}}`))
	}))
	defer srv.Close()

	tools := []toolDefinition{{
		Type: "function",
		Function: toolFunctionDefinition{
			Name:        "run_unix_command",
			Description: "run command",
			Parameters:  map[string]any{"type": "object"},
		},
	}}

	resp, err := chat(context.Background(), testAIConfig(srv.URL, "model"), []message{{Role: "user", Content: "hi"}}, tools)
	if err != nil {
		t.Fatalf("chat returned error: %v", err)
	}

	if len(gotReq.Tools) != 1 || gotReq.Tools[0].Function.Name != "run_unix_command" {
		t.Fatalf("expected tools in request, got %#v", gotReq.Tools)
	}

	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %#v", resp.Message.ToolCalls)
	}

	if resp.Message.ToolCalls[0].Function.Name != "run_unix_command" {
		t.Fatalf("unexpected tool call name: %#v", resp.Message.ToolCalls)
	}
}

func TestChatAddsAuthorizationHeader(t *testing.T) {
	wantAuth := "Bearer secret-token"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != wantAuth {
			t.Fatalf("authorization header mismatch: got %q want %q", got, wantAuth)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"ok"}}`))
	}))
	defer srv.Close()

	cfg := testAIConfig(srv.URL, "model")
	cfg.Authorization = wantAuth
	_, err := chat(context.Background(), cfg, []message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("chat returned error: %v", err)
	}
}

func TestLoadAllowlistedCommands(t *testing.T) {
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalCwd)
	})

	t.Run("env override", func(t *testing.T) {
		t.Setenv("ASH_TOOL_ALLOWLIST", "ls, ps,python3")
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
			t.Fatalf("expected python3 in allowlist: %#v", allowed)
		}
	})

	t.Run("cwd file wins over home", func(t *testing.T) {
		t.Setenv("ASH_TOOL_ALLOWLIST", "")
		home := t.TempDir()
		cwd := t.TempDir()
		t.Setenv("HOME", home)

		if err := os.WriteFile(filepath.Join(home, toolsFileName), []byte("ls\n"), 0o600); err != nil {
			t.Fatalf("write home tools file: %v", err)
		}
		if err := os.WriteFile(filepath.Join(cwd, toolsFileName), []byte("ps\n"), 0o600); err != nil {
			t.Fatalf("write cwd tools file: %v", err)
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
}

func TestLocalToolShimRunUnixCommandPolicy(t *testing.T) {
	originalRunner := toolCommandRunner
	t.Cleanup(func() { toolCommandRunner = originalRunner })

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
}

func TestLocalToolShimIncludesSchedulingAndWorkspaceTools(t *testing.T) {
	shim := localToolShim{}
	tools := shim.ListTools()
	names := map[string]struct{}{}
	for _, tool := range tools {
		names[tool.Function.Name] = struct{}{}
	}
	for _, required := range []string{
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

func TestBuildScheduledInvocationScript(t *testing.T) {
	origExecutable := osExecutable
	origGetwd := osGetwd
	t.Cleanup(func() {
		osExecutable = origExecutable
		osGetwd = origGetwd
	})

	osExecutable = func() (string, error) { return "/usr/local/bin/ash", nil }
	osGetwd = func() (string, error) { return "/tmp/project", nil }
	t.Setenv("AI_ENDPOINT", "http://localhost:11434")
	t.Setenv("AI_MODEL", "llama3.1")
	t.Setenv("HOME", "/Users/tester")
	t.Setenv("PATH", "/usr/bin:/bin")

	got, err := buildScheduledInvocationScript("summarize git status", "")
	if err != nil {
		t.Fatalf("buildScheduledInvocationScript error: %v", err)
	}
	if !strings.Contains(got, "cd '/tmp/project'") {
		t.Fatalf("expected cwd cd command, got %q", got)
	}
	if !strings.Contains(got, "AI_ENDPOINT='http://localhost:11434'") {
		t.Fatalf("expected AI_ENDPOINT env assignment, got %q", got)
	}
	if !strings.Contains(got, "AI_MODEL='llama3.1'") {
		t.Fatalf("expected AI_MODEL env assignment, got %q", got)
	}
	if !strings.Contains(got, "ASH_VERBOSE='1'") {
		t.Fatalf("expected verbose logging to be enabled, got %q", got)
	}
	if !strings.Contains(got, "ASH_LOG_FILE='") || !strings.Contains(got, "/.ash/logs/scheduler.log'") {
		t.Fatalf("expected scheduler log file assignment, got %q", got)
	}
	if !strings.Contains(got, "ASH_LOG_FORMAT='json'") {
		t.Fatalf("expected JSON log format, got %q", got)
	}
	if !strings.Contains(got, "ASH_LOG_MAX_BYTES='1048576'") {
		t.Fatalf("expected 1 MB log rotation default, got %q", got)
	}
	if !strings.Contains(got, "'/usr/local/bin/ash' 'summarize git status'") {
		t.Fatalf("expected ash invocation, got %q", got)
	}
}

func TestNormalizeFutureScheduleTime(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "from now minutes", input: "2 minutes from now", want: "now + 2 minutes"},
		{name: "from now hours", input: "1 hour from now", want: "now + 1 hour"},
		{name: "in minutes", input: "in 3 minutes", want: "now + 3 minutes"},
		{name: "already valid", input: "now + 5 minutes", want: "now + 5 minutes"},
		{name: "unchanged fallback", input: "tomorrow", want: "tomorrow"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeFutureScheduleTime(tt.input); got != tt.want {
				t.Fatalf("normalizeFutureScheduleTime(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseFutureScheduleTime(t *testing.T) {
	now := time.Date(2026, time.July, 24, 10, 0, 0, 0, time.Local)

	t.Run("relative offset", func(t *testing.T) {
		got, err := parseFutureScheduleTime("in 5 minutes", now)
		if err != nil {
			t.Fatalf("parseFutureScheduleTime returned error: %v", err)
		}
		want := now.Add(5 * time.Minute)
		if !got.Equal(want) {
			t.Fatalf("parseFutureScheduleTime returned %v, want %v", got, want)
		}
	})

	t.Run("rfc3339", func(t *testing.T) {
		future := now.Add(1 * time.Hour).Format(time.RFC3339)
		got, err := parseFutureScheduleTime(future, now)
		if err != nil {
			t.Fatalf("parseFutureScheduleTime returned error: %v", err)
		}
		want := now.Add(1 * time.Hour)
		if !got.Equal(want) {
			t.Fatalf("unexpected parsed time: got %v want %v", got, want)
		}
	})

	t.Run("reject past", func(t *testing.T) {
		if _, err := parseFutureScheduleTime("in 0 minutes", now); err == nil {
			t.Fatalf("expected error for non-future schedule")
		}
	})
}

func TestSchedulerDebugLoggingRotatesJSON(t *testing.T) {
	origWriter := debugWriter
	origJSON := debugJSONLogging
	t.Cleanup(func() {
		debugWriter = origWriter
		debugJSONLogging = origJSON
	})

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ASH_VERBOSE", "1")
	t.Setenv("ASH_LOG_FILE", filepath.Join(home, ".ash", "logs", "scheduler.log"))
	t.Setenv("ASH_LOG_FORMAT", "json")
	t.Setenv("ASH_LOG_MAX_BYTES", "220")

	configureDebugLogging()
	if debugWriter == nil {
		t.Fatalf("expected debug writer to be configured")
	}

	debugLogf("first %s", strings.Repeat("a", 120))
	debugLogf("second %s", strings.Repeat("b", 120))

	currentPath := filepath.Join(home, ".ash", "logs", "scheduler.log")
	rotatedPath := currentPath + ".1"

	currentData, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatalf("read current log: %v", err)
	}
	if !strings.Contains(string(currentData), `"level":"debug"`) {
		t.Fatalf("expected JSON debug entry, got %q", string(currentData))
	}

	rotatedData, err := os.ReadFile(rotatedPath)
	if err != nil {
		t.Fatalf("read rotated log: %v", err)
	}
	if !strings.Contains(string(rotatedData), `"first `) {
		t.Fatalf("expected first entry in rotated log, got %q", string(rotatedData))
	}
}

func TestRecurringJobLineRoundTrip(t *testing.T) {
	origTimeNow := timeNow
	origExecutable := osExecutable
	timeNow = func() time.Time {
		return time.Date(2026, time.July, 24, 10, 0, 0, 0, time.UTC)
	}
	osExecutable = func() (string, error) { return "/usr/local/bin/ash", nil }
	t.Cleanup(func() {
		timeNow = origTimeNow
		osExecutable = origExecutable
	})

	t.Setenv("AI_ENDPOINT", "http://localhost:11434")
	t.Setenv("AI_MODEL", "llama3.1")
	t.Setenv("HOME", "/Users/tester")
	t.Setenv("PATH", "/usr/bin:/bin")

	meta, line, err := buildRecurringJobLine("weekly review", "0 7 * * 1", "/Users/tester/work", "monday summary", "job-fixed")
	if err != nil {
		t.Fatalf("buildRecurringJobLine error: %v", err)
	}
	if !strings.Contains(line, "# ash:job job-fixed ") {
		t.Fatalf("expected recurring marker in line, got %q", line)
	}

	parsed, err := parseRecurringJobs(line + "\n")
	if err != nil {
		t.Fatalf("parseRecurringJobs error: %v", err)
	}
	if len(parsed) != 1 {
		t.Fatalf("expected one parsed job, got %d", len(parsed))
	}
	if parsed[0].Meta.ID != "job-fixed" || parsed[0].Meta.Prompt != "weekly review" {
		t.Fatalf("unexpected parsed metadata: %#v", parsed[0].Meta)
	}
	if parsed[0].Meta.Cron != "0 7 * * 1" {
		t.Fatalf("unexpected cron: %#v", parsed[0].Meta)
	}
	if meta.ID != parsed[0].Meta.ID {
		t.Fatalf("round-trip id mismatch: %q vs %q", meta.ID, parsed[0].Meta.ID)
	}
}

func TestWorkspaceReadWriteTools(t *testing.T) {
	shim := localToolShim{}
	home := t.TempDir()
	t.Setenv("HOME", home)

	writeResult := shim.CallTool(context.Background(), "ash_write_workspace_file", map[string]any{
		"path":    "state/counter.txt",
		"content": "41",
		"purpose": "Counter state used for invocation tracking",
	})
	if !strings.Contains(writeResult, `"ok":true`) {
		t.Fatalf("expected successful write, got %s", writeResult)
	}

	workspaceFile := filepath.Join(home, ".ash", "state", "counter.txt")
	data, err := os.ReadFile(workspaceFile)
	if err != nil {
		t.Fatalf("read workspace file: %v", err)
	}
	if string(data) != "41" {
		t.Fatalf("unexpected workspace content: %q", string(data))
	}

	inventory, err := os.ReadFile(filepath.Join(home, ".ash", "inventory.md"))
	if err != nil {
		t.Fatalf("read inventory file: %v", err)
	}
	if !strings.Contains(string(inventory), "state/counter.txt | Counter state used for invocation tracking") {
		t.Fatalf("expected inventory entry, got %q", string(inventory))
	}

	readResult := shim.CallTool(context.Background(), "ash_read_workspace_file", map[string]any{"path": "state/counter.txt"})
	if !strings.Contains(readResult, `"ok":true`) || !strings.Contains(readResult, "41") {
		t.Fatalf("expected successful read, got %s", readResult)
	}
}

func TestWorkspaceWriteRejectsPathTraversal(t *testing.T) {
	shim := localToolShim{}
	home := t.TempDir()
	t.Setenv("HOME", home)

	result := shim.CallTool(context.Background(), "ash_write_workspace_file", map[string]any{
		"path":    "../escape.txt",
		"content": "bad",
		"purpose": "invalid",
	})
	if !strings.Contains(result, "inside ~/.ash") {
		t.Fatalf("expected containment error, got %s", result)
	}
}

func TestRunToolLoop(t *testing.T) {
	originalRunner := toolCommandRunner
	t.Cleanup(func() { toolCommandRunner = originalRunner })

	toolCommandRunner = func(ctx context.Context, name string, args []string, timeout time.Duration, outputMax int) toolCommandResult {
		return toolCommandResult{OK: true, Command: "ls -1", ExitCode: 0, Stdout: "a\nb\n"}
	}

	requestCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		var req chatRequest
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)

		w.Header().Set("Content-Type", "application/json")
		if requestCount == 1 {
			if len(req.Tools) == 0 {
				t.Fatalf("expected tools list in first request")
			}
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"","tool_calls":[{"type":"function","function":{"index":0,"name":"run_unix_command","arguments":{"command":"ls","args":["-1"]}}}]}}`))
			return
		}

		if len(req.Messages) == 0 || req.Messages[len(req.Messages)-1].Role != "tool" {
			t.Fatalf("expected tool message in follow-up request, got %#v", req.Messages)
		}
		if req.Messages[len(req.Messages)-1].ToolName != "run_unix_command" {
			t.Fatalf("expected tool_name in follow-up request, got %#v", req.Messages[len(req.Messages)-1])
		}

		assistantCallPreserved := false
		for _, msg := range req.Messages {
			if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
				continue
			}
			call := msg.ToolCalls[0]
			if call.Function.Name != "run_unix_command" {
				continue
			}
			if call.Type != "function" {
				t.Fatalf("expected assistant tool call type=function, got %#v", call)
			}
			if call.Function.Index == nil || *call.Function.Index != 0 {
				t.Fatalf("expected assistant tool call index=0, got %#v", call.Function.Index)
			}
			assistantCallPreserved = true
			break
		}
		if !assistantCallPreserved {
			t.Fatalf("expected assistant tool call metadata in follow-up request, got %#v", req.Messages)
		}

		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"done"}}`))
	}))
	defer srv.Close()

	shim := localToolShim{allowlist: map[string]struct{}{"ls": {}}}
	final, updated, err := runToolLoop(context.Background(), testAIConfig(srv.URL, "model"), "list files", []message{{Role: "user", Content: "list files"}}, shim)
	if err != nil {
		t.Fatalf("runToolLoop returned error: %v", err)
	}

	if final != "done" {
		t.Fatalf("expected final assistant reply, got %q", final)
	}

	if len(updated) < 3 {
		t.Fatalf("expected tool loop messages, got %#v", updated)
	}
}

func TestRunToolLoopRetriesExecutionPrompt(t *testing.T) {
	originalRunner := toolCommandRunner
	t.Cleanup(func() { toolCommandRunner = originalRunner })

	toolCommandRunner = func(ctx context.Context, name string, args []string, timeout time.Duration, outputMax int) toolCommandResult {
		if name != "python3" {
			t.Fatalf("unexpected command: %q", name)
		}
		return toolCommandResult{OK: true, Command: "python3 -c ...", ExitCode: 0, Stdout: "Hello World\n"}
	}

	requestCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "application/json")

		switch requestCount {
		case 1:
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"Here is python code: print(\"Hello World\")"}}`))
		case 2:
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"python3","arguments":{"code":"print(\"Hello World\")"}}}]}}`))
		default:
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"Executed successfully"}}`))
		}
	}))
	defer srv.Close()

	shim := localToolShim{allowlist: map[string]struct{}{}}
	final, updated, err := runToolLoop(
		context.Background(),
		testAIConfig(srv.URL, "model"),
		"Use python to print hello world.",
		[]message{{Role: "user", Content: "Use python to print hello world."}},
		shim,
	)
	if err != nil {
		t.Fatalf("runToolLoop returned error: %v", err)
	}

	if final != "Executed successfully" {
		t.Fatalf("unexpected final reply: %q", final)
	}

	hasToolResult := false
	for _, m := range updated {
		if m.Role == "tool" && m.ToolName == "python3" && strings.Contains(m.Content, "Hello World") {
			hasToolResult = true
			break
		}
	}
	if !hasToolResult {
		t.Fatalf("expected python3 tool result in message history, got %#v", updated)
	}
}

func TestBuildExecutionTasks(t *testing.T) {
	tasks := buildExecutionTasks("What directory am I in and are there any executable files?", 6)
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d: %#v", len(tasks), tasks)
	}

	if tasks[0].ID != 1 || tasks[0].Status != taskStatusPending {
		t.Fatalf("unexpected first task: %#v", tasks[0])
	}

	if !strings.Contains(strings.ToLower(tasks[0].Goal), "what directory am i in") {
		t.Fatalf("unexpected first task goal: %q", tasks[0].Goal)
	}

	if !strings.Contains(strings.ToLower(tasks[1].Goal), "executable") {
		t.Fatalf("unexpected second task goal: %q", tasks[1].Goal)
	}
}

func TestBuildExecutionStateMessageUsesRelevanceWindow(t *testing.T) {
	tasks := []executionTask{
		{ID: 1, Goal: "Get current directory", Status: taskStatusDone},
		{ID: 2, Goal: "Find executable files", Status: taskStatusPending},
	}
	observations := []toolObservation{
		{Command: "pwd", OK: true, Summary: "/tmp/demo"},
		{Command: "ls", OK: true, Summary: "a\nb"},
		{Command: "find", OK: false, Summary: "command is not allowlisted"},
	}

	msg := buildExecutionStateMessage("directory and executables", tasks, observations, 2)
	if msg.Role != "system" {
		t.Fatalf("expected system role, got %q", msg.Role)
	}

	if !strings.Contains(msg.Content, "Execution task list") {
		t.Fatalf("expected task list marker in state message: %q", msg.Content)
	}

	if strings.Contains(msg.Content, "pwd") {
		t.Fatalf("expected oldest observation to be trimmed by relevance window: %q", msg.Content)
	}

	if !strings.Contains(msg.Content, "ls") || !strings.Contains(msg.Content, "find") {
		t.Fatalf("expected newest observations to be included: %q", msg.Content)
	}
}

func TestRunToolLoopInjectsExecutionStateMessage(t *testing.T) {
	originalRunner := toolCommandRunner
	t.Cleanup(func() { toolCommandRunner = originalRunner })

	toolCommandRunner = func(ctx context.Context, name string, args []string, timeout time.Duration, outputMax int) toolCommandResult {
		if name != "pwd" {
			t.Fatalf("unexpected command: %q", name)
		}
		return toolCommandResult{OK: true, Command: "pwd", ExitCode: 0, Stdout: "/tmp/demo\n"}
	}

	requestCount := 0
	sawStateMessage := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		var req chatRequest
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)

		for _, m := range req.Messages {
			if m.Role == "system" && strings.Contains(m.Content, "Execution task list") {
				sawStateMessage = true
				break
			}
		}

		w.Header().Set("Content-Type", "application/json")
		switch requestCount {
		case 1:
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"run_unix_command","arguments":{"command":"pwd"}}}]}}`))
		default:
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"done"}}`))
		}
	}))
	defer srv.Close()

	shim := localToolShim{allowlist: map[string]struct{}{"pwd": {}}}
	final, _, err := runToolLoop(context.Background(), testAIConfig(srv.URL, "model"), "what directory am i in and list executables", []message{{Role: "user", Content: "what directory am i in and list executables"}}, shim)
	if err != nil {
		t.Fatalf("runToolLoop returned error: %v", err)
	}

	if final != "done" {
		t.Fatalf("expected final reply, got %q", final)
	}

	if !sawStateMessage {
		t.Fatalf("expected execution state system message to be injected")
	}
}

func TestVerboseLoggingEnabled(t *testing.T) {
	t.Setenv("ASH_VERBOSE", "")
	if verboseLoggingEnabled() {
		t.Fatalf("expected verbose logging disabled by default")
	}

	t.Setenv("ASH_VERBOSE", "1")
	if !verboseLoggingEnabled() {
		t.Fatalf("expected verbose logging enabled for ASH_VERBOSE=1")
	}

	t.Setenv("ASH_VERBOSE", "true")
	if !verboseLoggingEnabled() {
		t.Fatalf("expected verbose logging enabled for ASH_VERBOSE=true")
	}
}

func TestChatVerboseLogsPayload(t *testing.T) {
	origDebugWriter := debugWriter
	t.Cleanup(func() { debugWriter = origDebugWriter })

	t.Setenv("ASH_VERBOSE", "1")
	var logOutput bytes.Buffer
	debugWriter = &logOutput

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"ok"}}`))
	}))
	defer srv.Close()

	tools := []toolDefinition{{
		Type: "function",
		Function: toolFunctionDefinition{
			Name:        "run_unix_command",
			Description: "run command",
			Parameters:  map[string]any{"type": "object"},
		},
	}}

	_, err := chat(context.Background(), testAIConfig(srv.URL, "model"), []message{{Role: "user", Content: "hi"}}, tools)
	if err != nil {
		t.Fatalf("chat returned error: %v", err)
	}

	logs := logOutput.String()
	if !strings.Contains(logs, "AI request payload") {
		t.Fatalf("expected payload debug log, got %q", logs)
	}
	if !strings.Contains(logs, `"tools":[`) {
		t.Fatalf("expected tool schema in payload logs, got %q", logs)
	}
	if !strings.Contains(logs, "AI response: status=200") {
		t.Fatalf("expected response debug log, got %q", logs)
	}
}

func TestRunToolLoopVerboseLogsToolInvocation(t *testing.T) {
	originalRunner := toolCommandRunner
	origDebugWriter := debugWriter
	t.Cleanup(func() {
		toolCommandRunner = originalRunner
		debugWriter = origDebugWriter
	})

	t.Setenv("ASH_VERBOSE", "1")
	var logOutput bytes.Buffer
	debugWriter = &logOutput

	toolCommandRunner = func(ctx context.Context, name string, args []string, timeout time.Duration, outputMax int) toolCommandResult {
		return toolCommandResult{OK: true, Command: "ls", ExitCode: 0, Stdout: "a\n"}
	}

	requestCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		if requestCount == 1 {
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"run_unix_command","arguments":{"command":"ls"}}}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"done"}}`))
	}))
	defer srv.Close()

	shim := localToolShim{allowlist: map[string]struct{}{"ls": {}}}
	_, _, err := runToolLoop(context.Background(), testAIConfig(srv.URL, "model"), "list files", []message{{Role: "user", Content: "list files"}}, shim)
	if err != nil {
		t.Fatalf("runToolLoop returned error: %v", err)
	}

	logs := logOutput.String()
	if !strings.Contains(logs, "Tool invocation requested: name=run_unix_command") {
		t.Fatalf("expected tool invocation debug log, got %q", logs)
	}
	if !strings.Contains(logs, "Tool invocation result: name=run_unix_command") {
		t.Fatalf("expected tool result debug log, got %q", logs)
	}
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

func TestParseInstallArgs(t *testing.T) {
	t.Run("empty args", func(t *testing.T) {
		shellName, dryRun, err := parseInstallArgs(nil)
		if err != nil {
			t.Fatalf("parseInstallArgs returned error: %v", err)
		}
		if shellName != "" || dryRun {
			t.Fatalf("unexpected parse result: shell=%q dryRun=%v", shellName, dryRun)
		}
	})

	t.Run("shell and dry run", func(t *testing.T) {
		shellName, dryRun, err := parseInstallArgs([]string{"--shell", "zsh", "--dry-run"})
		if err != nil {
			t.Fatalf("parseInstallArgs returned error: %v", err)
		}
		if shellName != "zsh" || !dryRun {
			t.Fatalf("unexpected parse result: shell=%q dryRun=%v", shellName, dryRun)
		}
	})

	t.Run("missing shell value", func(t *testing.T) {
		_, _, err := parseInstallArgs([]string{"--shell"})
		if err == nil || !strings.Contains(err.Error(), "--shell requires a value") {
			t.Fatalf("expected missing value error, got %v", err)
		}
	})

	t.Run("unknown arg", func(t *testing.T) {
		_, _, err := parseInstallArgs([]string{"--wat"})
		if err == nil || !strings.Contains(err.Error(), "unknown install argument") {
			t.Fatalf("expected unknown argument error, got %v", err)
		}
	})
}

func TestInstallRecommendation(t *testing.T) {
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalCwd)
	})

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/bash")

	reco, err := installRecommendation()
	if err != nil {
		t.Fatalf("installRecommendation returned error: %v", err)
	}
	if !strings.Contains(reco, "ash install --shell bash") {
		t.Fatalf("expected recommendation for bash install, got %q", reco)
	}

	rcPath := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(rcPath, []byte(installBlockForShell("bash")), 0o600); err != nil {
		t.Fatalf("write rc file: %v", err)
	}

	reco, err = installRecommendation()
	if err != nil {
		t.Fatalf("installRecommendation returned error: %v", err)
	}
	if reco != "" {
		t.Fatalf("expected no recommendation when installed, got %q", reco)
	}
}

func TestRunInstall(t *testing.T) {
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalCwd)
	})

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/bash")

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"install", "--shell", "bash"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d stderr=%q", code, stderr.String())
	}

	rcPath := filepath.Join(home, ".bashrc")
	rcContent, err := os.ReadFile(rcPath)
	if err != nil {
		t.Fatalf("read rc file: %v", err)
	}

	content := string(rcContent)
	if !strings.Contains(content, installStartMarker) || !strings.Contains(content, installEndMarker) {
		t.Fatalf("expected install block markers in rc file, got %q", content)
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"install", "--shell", "bash"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected second install to succeed, got %d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "already present") {
		t.Fatalf("expected idempotent install message, got %q", stdout.String())
	}

	rcContentAfter, err := os.ReadFile(rcPath)
	if err != nil {
		t.Fatalf("read rc file after second install: %v", err)
	}
	if strings.Count(string(rcContentAfter), installStartMarker) != 1 {
		t.Fatalf("expected single install block, got %d", strings.Count(string(rcContentAfter), installStartMarker))
	}
}

func TestRunInstallDryRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/zsh")

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"install", "--shell", "zsh", "--dry-run"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected dry-run exit code 0, got %d stderr=%q", code, stderr.String())
	}

	if !strings.Contains(stdout.String(), "[dry-run]") {
		t.Fatalf("expected dry-run output, got %q", stdout.String())
	}

	rcPath := filepath.Join(home, ".zshrc")
	if _, err := os.Stat(rcPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no rc file write in dry-run, stat err=%v", err)
	}
}

var conservativeOperandPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func shouldRouteToAshConservative(command string, args []string) bool {
	// Rule A: no args => delegate.
	if len(args) == 0 {
		return false
	}

	// Rule B: flag-style args => delegate.
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			return false
		}
	}

	// Rule C: path-like args => delegate.
	for _, arg := range args {
		if strings.Contains(arg, "/") || strings.HasPrefix(arg, "./") || strings.HasPrefix(arg, "../") {
			return false
		}
	}

	// Rule D: builtin/keyword single-operand forms => delegate.
	switch strings.ToLower(command) {
	case "time", "test", "type":
		if len(args) == 1 && conservativeOperandPattern.MatchString(args[0]) {
			return false
		}
	}

	full := command
	if len(args) > 0 {
		full += " " + strings.Join(args, " ")
	}

	// Rule E: trailing question mark with enough tokens => ash.
	if strings.HasSuffix(full, "?") && len(args) >= 2 {
		return true
	}

	// Rule F: interrogative/auxiliary first arg with enough tokens => ash.
	first := strings.ToLower(args[0])
	switch first {
	case "is", "are", "am", "do", "does", "did", "can", "could", "should", "would", "will", "why", "how", "when", "where", "who":
		return len(args) >= 2
	}

	// Rule F2: for natural-language wrappers, allow early auxiliary/interrogative
	// tokens beyond the first word (for example "What directory am I in ...").
	switch strings.ToLower(command) {
	case "what", "which", "who", "where":
		if len(args) >= 3 {
			limit := 4
			if len(args) < limit {
				limit = len(args)
			}
			for i := 1; i < limit; i++ {
				token := strings.ToLower(strings.Trim(args[i], "?!.:,;"))
				switch token {
				case "is", "are", "am", "do", "does", "did", "can", "could", "should", "would", "will", "why", "how", "when", "where", "who", "if":
					return true
				}
			}
		}
	}

	// Rule G: default => delegate.
	return false
}

func TestShouldRouteToAshConservative(t *testing.T) {
	tests := []struct {
		name    string
		command string
		args    []string
		want    bool
	}{
		{name: "rule A no args", command: "what", args: nil, want: false},
		{name: "rule B flag arg", command: "what", args: []string{"-s", "file"}, want: false},
		{name: "rule C path arg", command: "what", args: []string{"/usr/bin/what"}, want: false},
		{name: "rule D builtin single operand", command: "test", args: []string{"foo"}, want: false},
		{name: "rule E trailing question", command: "What", args: []string{"time", "is", "it?"}, want: true},
		{name: "rule F interrogative first arg", command: "what", args: []string{"is", "awk"}, want: true},
		{name: "rule F2 natural language mid auxiliary", command: "What", args: []string{"directory", "am", "I", "in", "and", "are", "there", "any", "executeable", "files", "Run", "multiple", "tools", "if", "necessary"}, want: true},
		{name: "rule G default delegate", command: "which", args: []string{"ls"}, want: false},
		{name: "precedence B over E", command: "what", args: []string{"-n", "what?"}, want: false},
		{name: "precedence C over F", command: "what", args: []string{"who", "./path"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldRouteToAshConservative(tt.command, tt.args); got != tt.want {
				t.Fatalf("route decision mismatch: got %v want %v", got, tt.want)
			}
		})
	}
}

func runShellCollisionFixture(t *testing.T, shell, fixture, invocation string) string {
	t.Helper()

	shellPath, err := exec.LookPath(shell)
	if err != nil {
		t.Skipf("%s not available: %v", shell, err)
	}

	fixturePath := filepath.Join("testdata", fixture)
	command := fmt.Sprintf("source %q; %s", fixturePath, invocation)
	execCmd := exec.Command(shellPath, "-c", command)
	output, err := execCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s fixture invocation failed: %v\noutput=%s", shell, err, output)
	}

	return strings.TrimSpace(string(output))
}

func TestBashCollisionWrappers(t *testing.T) {
	tests := []struct {
		name       string
		invocation string
		want       string
	}{
		{name: "title case what routed", invocation: "What time is it?", want: "ASH:What time is it?"},
		{name: "lower case what routed", invocation: "what time is it?", want: "ASH:what time is it?"},
		{name: "what interrogative routed", invocation: "what is awk", want: "ASH:what is awk"},
		{name: "what mid auxiliary routed", invocation: "What directory am I in and are there any executeable files Run multiple tools if necessary", want: "ASH:What directory am I in and are there any executeable files Run multiple tools if necessary"},
		{name: "what path delegates", invocation: "what /usr/bin/what", want: "DELEGATE:what:/usr/bin/what"},
		{name: "what flag delegates", invocation: "what -s file", want: "DELEGATE:what:-s file"},
		{name: "title case time routed", invocation: "Time is it late?", want: "ASH:Time is it late?"},
		{name: "test question routed", invocation: "test should I use jq", want: "ASH:test should I use jq"},
		{name: "test flag delegates", invocation: "test -f /etc/hosts", want: "DELEGATE:test:-f /etc/hosts"},
		{name: "type question routed", invocation: "type why is grep slow?", want: "ASH:type why is grep slow?"},
		{name: "type command form delegates", invocation: "type ls", want: "DELEGATE:type:ls"},
		{name: "which question routed", invocation: "which should I use ripgrep or grep", want: "ASH:which should I use ripgrep or grep"},
		{name: "which command form delegates", invocation: "which ls", want: "DELEGATE:which:ls"},
		{name: "who question routed", invocation: "who am I?", want: "ASH:who am I?"},
		{name: "who no args delegates", invocation: "who", want: "DELEGATE:who:"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runShellCollisionFixture(t, "bash", "collision_wrappers.bash", tt.invocation)
			if got != tt.want {
				t.Fatalf("bash fixture output mismatch: got %q want %q", got, tt.want)
			}
		})
	}
}

func TestZshCollisionWrappers(t *testing.T) {
	tests := []struct {
		name       string
		invocation string
		want       string
	}{
		{name: "title case what routed", invocation: "What time is it?", want: "ASH:What time is it?"},
		{name: "lower case what routed", invocation: "what time is it?", want: "ASH:what time is it?"},
		{name: "what mid auxiliary routed", invocation: "What directory am I in and are there any executeable files Run multiple tools if necessary", want: "ASH:What directory am I in and are there any executeable files Run multiple tools if necessary"},
		{name: "what path delegates", invocation: "what /usr/bin/what", want: "DELEGATE:what:/usr/bin/what"},
		{name: "title case time routed", invocation: "Time is it late?", want: "ASH:Time is it late?"},
		{name: "where question routed", invocation: "where should logs go", want: "ASH:where should logs go"},
		{name: "where command form delegates", invocation: "where ls", want: "DELEGATE:where:ls"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runShellCollisionFixture(t, "zsh", "collision_wrappers.zsh", tt.invocation)
			if got != tt.want {
				t.Fatalf("zsh fixture output mismatch: got %q want %q", got, tt.want)
			}
		})
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
		t.Setenv("AI_ENDPOINT", "")
		t.Setenv("AI_MODEL", "")
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := run([]string{"hello"}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
		if !strings.Contains(stderr.String(), "AI_ENDPOINT is required") {
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
		path := filepath.Join(home, historyFileName)
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
		if !strings.Contains(gotReq.Messages[0].Content, "Current local datetime: 2026-07-24T07:00:00-07:00") {
			t.Fatalf("expected datetime in system prompt, got %q", gotReq.Messages[0].Content)
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

	cmd := exec.Command(os.Args[0], "-test.run=TestMainHelperProcess")
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
