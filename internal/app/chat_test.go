package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestChatRetriesTransientFailures(t *testing.T) {
	t.Setenv(brokerSocketEnv, "")
	t.Setenv(brokerTokenEnv, "")

	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch attempts.Add(1) {
		case 1, 2:
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"busy"}`))
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"ok"}}`))
		}
	}))
	defer srv.Close()

	t.Setenv("AI_ENDPOINT", srv.URL)
	t.Setenv("AI_MODEL", "test-model")
	t.Setenv("ASH_RETRY_MAX_ATTEMPTS", "3")
	t.Setenv("ASH_RETRY_BASE_DELAY", "0s")
	t.Setenv("ASH_RETRY_MAX_DELAY", "0s")

	cfg := aiConfig{BaseURL: srv.URL, Model: "test-model", HistoryKey: srv.URL + "/test-model", Provider: providerOllama, UseNativeCaching: true}
	resp, err := chat(context.Background(), cfg, []message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("chat returned error: %v", err)
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}
	if resp.Message.Content != "ok" {
		t.Fatalf("expected assistant content ok, got %q", resp.Message.Content)
	}
}

func TestBackoffDelay(t *testing.T) {
	tests := []struct {
		name    string
		attempt int
		base    time.Duration
		max     time.Duration
		want    time.Duration
	}{
		{name: "attempt below one has no delay", attempt: 0, base: time.Second, max: 0, want: 0},
		{name: "first attempt uses base delay", attempt: 1, base: time.Second, max: 0, want: time.Second},
		{name: "second attempt doubles base delay", attempt: 2, base: time.Second, max: 0, want: 2 * time.Second},
		{name: "third attempt quadruples base delay", attempt: 3, base: time.Second, max: 0, want: 4 * time.Second},
		{name: "delay clamps to max", attempt: 10, base: time.Second, max: 5 * time.Second, want: 5 * time.Second},
		{name: "large attempt count still clamps to max", attempt: 1000, base: time.Second, max: 30 * time.Second, want: 30 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := backoffDelay(tt.attempt, tt.base, tt.max); got != tt.want {
				t.Fatalf("backoffDelay(%d, %v, %v) = %v, want %v", tt.attempt, tt.base, tt.max, got, tt.want)
			}
		})
	}
}

func TestChat(t *testing.T) {
	t.Setenv(brokerSocketEnv, "")
	t.Setenv(brokerTokenEnv, "")

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

func TestChatOpenAIResponsesAdapter(t *testing.T) {
	originalIsRealOpenAIHost := isRealOpenAIHost
	isRealOpenAIHost = func(string) bool { return true }
	t.Cleanup(func() { isRealOpenAIHost = originalIsRealOpenAIHost })

	var gotPath string
	var gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]},{"type":"function_call","call_id":"call_test_1","name":"run_unix_command","arguments":"{\"command\":\"ls\"}"}]}`))
	}))
	defer srv.Close()

	cfg := aiConfig{
		BaseURL:          srv.URL + "/v1",
		Model:            "gpt-4.1-mini",
		Authorization:    "Bearer test-key",
		AuthToken:        "test-key",
		Provider:         providerOpenAI,
		UseNativeCaching: true,
	}
	resp, err := chat(context.Background(), cfg, []message{{Role: "user", Content: "list files"}}, nil)
	if err != nil {
		t.Fatalf("chat returned error: %v", err)
	}

	if gotPath != "/v1/responses" {
		t.Fatalf("unexpected path: got %q", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("unexpected auth header: got %q", gotAuth)
	}
	if resp.Message.Content != "done" {
		t.Fatalf("unexpected content: got %q", resp.Message.Content)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %d", len(resp.Message.ToolCalls))
	}
	if resp.Message.ToolCalls[0].ID != "call_test_1" {
		t.Fatalf("unexpected tool call id: got %q", resp.Message.ToolCalls[0].ID)
	}
	if resp.Message.ToolCalls[0].Function.Name != "run_unix_command" {
		t.Fatalf("unexpected tool call name: got %q", resp.Message.ToolCalls[0].Function.Name)
	}
}

func TestChatOpenAIChatCompletionsAdapterForNonOpenAIHost(t *testing.T) {
	var gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok","tool_calls":[{"id":"call_1","type":"function","function":{"name":"run_unix_command","arguments":"{\"command\":\"pwd\"}"}}]}}],"usage":{"prompt_tokens":5,"completion_tokens":3}}`))
	}))
	defer srv.Close()

	cfg := aiConfig{
		BaseURL:  srv.URL + "/v1",
		Model:    "llama3.1",
		Provider: providerOpenAI,
	}
	resp, err := chat(context.Background(), cfg, []message{{Role: "user", Content: "where am i"}}, nil)
	if err != nil {
		t.Fatalf("chat returned error: %v", err)
	}

	if gotPath != "/v1/chat/completions" {
		t.Fatalf("unexpected path: got %q", gotPath)
	}
	if resp.Message.Content != "ok" {
		t.Fatalf("unexpected content: got %q", resp.Message.Content)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].ID != "call_1" {
		t.Fatalf("unexpected tool calls: %+v", resp.Message.ToolCalls)
	}
	if !resp.Usage.Available || resp.Usage.InputTokens != 5 || resp.Usage.OutputTokens != 3 {
		t.Fatalf("unexpected usage: %+v", resp.Usage)
	}
}

func TestAlwaysUseOpenAIAPIForOllamaOverridesProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(aiEnvEndpoint, "http://localhost:11434")
	t.Setenv(aiEnvModel, "llama3.1")
	t.Setenv(aiEnvAuthToken, "")
	t.Setenv(aiEnvProvider, "")

	t.Setenv("ASH_ALWAYS_OPENAI_API", "")
	cfg, err := parseAIConfigFromEnv()
	if err != nil {
		t.Fatalf("parseAIConfigFromEnv returned error: %v", err)
	}
	if cfg.Provider != providerOpenAI {
		t.Fatalf("expected providerOpenAI by default, got %q", cfg.Provider)
	}

	t.Setenv("ASH_ALWAYS_OPENAI_API", "0")
	cfg, err = parseAIConfigFromEnv()
	if err != nil {
		t.Fatalf("parseAIConfigFromEnv returned error: %v", err)
	}
	if cfg.Provider != providerOllama {
		t.Fatalf("expected providerOllama when ASH_ALWAYS_OPENAI_API=0, got %q", cfg.Provider)
	}

	t.Setenv("ASH_ALWAYS_OPENAI_API", "1")
	cfg, err = parseAIConfigFromEnv()
	if err != nil {
		t.Fatalf("parseAIConfigFromEnv returned error: %v", err)
	}
	if cfg.Provider != providerOpenAI {
		t.Fatalf("expected providerOpenAI when ASH_ALWAYS_OPENAI_API=1, got %q", cfg.Provider)
	}
}

func TestChatOpenAIAdapterMapsErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key","type":"invalid_request_error","code":"invalid_api_key"}}`))
	}))
	defer srv.Close()

	cfg := aiConfig{
		BaseURL:       srv.URL + "/v1",
		Model:         "gpt-4.1-mini",
		Authorization: "Bearer bad-key",
		AuthToken:     "bad-key",
		Provider:      providerOpenAI,
	}
	t.Setenv("ASH_RETRY_MAX_ATTEMPTS", "1")
	_, err := chat(context.Background(), cfg, []message{{Role: "user", Content: "hi"}}, nil)
	if err == nil {
		t.Fatal("expected an error for a 401 response")
	}
	var statusErr chatStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected chatStatusError, got %T: %v", err, err)
	}
	if statusErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("StatusCode = %d, want 401", statusErr.StatusCode)
	}
}

func TestChatOpenAIAdapterCancelsPromptly(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	cfg := aiConfig{
		BaseURL:       srv.URL + "/v1",
		Model:         "gpt-4.1-mini",
		Authorization: "Bearer test-key",
		AuthToken:     "test-key",
		Provider:      providerOpenAI,
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := chat(ctx, cfg, []message{{Role: "user", Content: "hi"}}, nil)
		result <- err
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-result:
		if err == nil {
			t.Fatal("expected an error after context cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("chat did not return promptly after context cancellation")
	}
}

func TestChatStreamDisabledFallsBackToChatExecutor(t *testing.T) {
	t.Setenv("ASH_STREAM", "0")
	originalExecutor := chatExecutor
	t.Cleanup(func() { chatExecutor = originalExecutor })
	chatExecutor = func(ctx context.Context, aiCfg aiConfig, messages []message, tools []toolDefinition) (chatResponse, error) {
		return chatResponse{Message: message{Role: "assistant", Content: "buffered answer"}}, nil
	}

	var deltas []string
	resp, err := chatStream(context.Background(), aiConfig{Provider: providerOpenAI}, []message{{Role: "user", Content: "hi"}}, nil, func(d streamDelta) {
		deltas = append(deltas, d.TextDelta)
	})
	if err != nil {
		t.Fatalf("chatStream returned error: %v", err)
	}
	if resp.Message.Content != "buffered answer" {
		t.Fatalf("unexpected content: got %q", resp.Message.Content)
	}
	if len(deltas) != 1 || deltas[0] != "buffered answer" {
		t.Fatalf("expected one delta with the full buffered content, got %v", deltas)
	}
}

func TestChatStreamOpenAIRealSSE(t *testing.T) {
	originalIsRealOpenAIHost := isRealOpenAIHost
	isRealOpenAIHost = func(string) bool { return true }
	t.Cleanup(func() { isRealOpenAIHost = originalIsRealOpenAIHost })
	t.Setenv("ASH_STREAM", "1")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		frames := []string{
			`{"type":"response.output_text.delta","delta":"Hello"}`,
			`{"type":"response.output_text.delta","delta":", world"}`,
			`{"type":"response.completed","response":{"id":"resp_1","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello, world"}]}],"usage":{"input_tokens":3,"output_tokens":2}}}`,
		}
		for _, frame := range frames {
			_, _ = w.Write([]byte("data: " + frame + "\n\n"))
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer srv.Close()

	cfg := aiConfig{
		BaseURL:       srv.URL + "/v1",
		Model:         "gpt-4.1-mini",
		Authorization: "Bearer test-key",
		AuthToken:     "test-key",
		Provider:      providerOpenAI,
	}
	var deltas []string
	metrics := newExecutionMetrics(time.Now())
	ctx := withExecutionMetrics(context.Background(), metrics)
	resp, err := chatStream(ctx, cfg, []message{{Role: "user", Content: "hi"}}, nil, func(d streamDelta) {
		deltas = append(deltas, d.TextDelta)
	})
	if err != nil {
		t.Fatalf("chatStream returned error: %v", err)
	}
	if strings.Join(deltas, "") != "Hello, world" {
		t.Fatalf("unexpected accumulated deltas: %v", deltas)
	}
	if resp.Message.Content != "Hello, world" {
		t.Fatalf("unexpected final content: got %q", resp.Message.Content)
	}
	if !resp.Usage.Available || resp.Usage.InputTokens != 3 || resp.Usage.OutputTokens != 2 {
		t.Fatalf("unexpected usage: %+v", resp.Usage)
	}
	snapshot := metrics.snapshot()
	if !snapshot.InputTokensAvailable || snapshot.InputTokens != 3 || snapshot.OutputTokens != 2 {
		t.Fatalf("streaming path did not record token usage in metrics: %+v", snapshot)
	}
	if metrics.stageDuration(metricsStageAIProcessing) <= 0 {
		t.Fatal("streaming path did not record AI processing duration")
	}
}

func TestChatSDKPathRecordsProcessingAndTokenMetrics(t *testing.T) {
	originalIsRealOpenAIHost := isRealOpenAIHost
	isRealOpenAIHost = func(string) bool { return true }
	t.Cleanup(func() { isRealOpenAIHost = originalIsRealOpenAIHost })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_1","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":11,"output_tokens":7}}`))
	}))
	defer srv.Close()

	cfg := aiConfig{
		BaseURL:       srv.URL + "/v1",
		Model:         "gpt-4.1-mini",
		Authorization: "Bearer test-key",
		AuthToken:     "test-key",
		Provider:      providerOpenAI,
	}
	metrics := newExecutionMetrics(time.Now())
	ctx := withExecutionMetrics(context.Background(), metrics)
	if _, err := chat(ctx, cfg, []message{{Role: "user", Content: "hi"}}, nil); err != nil {
		t.Fatalf("chat returned error: %v", err)
	}
	snapshot := metrics.snapshot()
	if !snapshot.InputTokensAvailable || snapshot.InputTokens != 11 || snapshot.OutputTokens != 7 {
		t.Fatalf("SDK path did not record token usage in metrics: %+v", snapshot)
	}
	if metrics.stageDuration(metricsStageAIProcessing) <= 0 {
		t.Fatal("SDK path did not record AI processing duration")
	}
}

func TestVerboseSessionLogReportsOllamaOpenAIAPIAndStreamRequest(t *testing.T) {
	// Thoroughly exercises all four combinations of ASH_ALWAYS_OPENAI_API and
	// ASH_STREAM, since both now default to on.
	tests := []struct {
		name          string
		alwaysOpenAI  string
		stream        string
		wantProvider  aiProvider
		wantOllamaAPI bool
		wantStreamReq bool
	}{
		{name: "both on", alwaysOpenAI: "on", stream: "yes", wantProvider: providerOpenAI, wantOllamaAPI: true, wantStreamReq: true},
		{name: "both off", alwaysOpenAI: "0", stream: "0", wantProvider: providerOllama, wantOllamaAPI: false, wantStreamReq: false},
		{name: "openai api on, stream off", alwaysOpenAI: "1", stream: "off", wantProvider: providerOpenAI, wantOllamaAPI: true, wantStreamReq: false},
		{name: "openai api off, stream on", alwaysOpenAI: "false", stream: "1", wantProvider: providerOllama, wantOllamaAPI: false, wantStreamReq: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("SESSION_ID", "")
			t.Setenv("ASH_VERBOSE", "true")
			t.Setenv(ashEnvAlwaysOpenAIAPI, tt.alwaysOpenAI)
			t.Setenv("ASH_STREAM", tt.stream)
			t.Setenv(aiEnvEndpoint, "http://localhost:11434")
			t.Setenv(aiEnvModel, "llama3.1")

			originalChatStreamExecutor := chatStreamExecutor
			chatStreamExecutor = func(context.Context, aiConfig, []message, []toolDefinition, func(streamDelta)) (chatResponse, error) {
				return chatResponse{Message: message{Role: "assistant", Content: "ok"}}, nil
			}
			t.Cleanup(func() { chatStreamExecutor = originalChatStreamExecutor })

			var stdout bytes.Buffer
			var stderr syncBuffer
			if code := run([]string{"hello"}, &stdout, &stderr); code != 0 {
				t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
			}

			record := findDebugLogRecord(t, stderr.String(), "ash session started")
			if record["provider"] != string(tt.wantProvider) {
				t.Fatalf("expected provider %q, got %#v", tt.wantProvider, record["provider"])
			}
			if record["ollama_openai_api"] != tt.wantOllamaAPI {
				t.Fatalf("expected ollama_openai_api=%v, got %#v", tt.wantOllamaAPI, record["ollama_openai_api"])
			}
			if record["stream_requested"] != tt.wantStreamReq {
				t.Fatalf("expected stream_requested=%v, got %#v", tt.wantStreamReq, record["stream_requested"])
			}
		})
	}
}

func TestChatStreamVerboseLogReportsUnsupportedFallback(t *testing.T) {
	originalWriter := debugWriter
	originalJSON := debugJSONLogging
	originalChatExecutor := chatExecutor
	t.Cleanup(func() {
		debugWriter = originalWriter
		debugJSONLogging = originalJSON
		chatExecutor = originalChatExecutor
	})

	var logs bytes.Buffer
	debugWriter = &logs
	debugJSONLogging = true
	t.Setenv("ASH_VERBOSE", "1")
	t.Setenv("ASH_STREAM", "1")
	chatExecutor = func(context.Context, aiConfig, []message, []toolDefinition) (chatResponse, error) {
		return chatResponse{Message: message{Role: "assistant", Content: "ok"}}, nil
	}
	configureDebugLogging()

	_, err := chatStream(context.Background(), aiConfig{Provider: providerGoogle}, nil, nil, nil)
	if err != nil {
		t.Fatalf("chatStream returned error: %v", err)
	}

	record := findDebugLogRecord(t, logs.String(), "AI streaming mode")
	if record["stream_requested"] != true || record["stream_used"] != false || record["reason"] != "adapter_unsupported" {
		t.Fatalf("unexpected streaming fallback record: %#v", record)
	}
}

func findDebugLogRecord(t *testing.T, logs, message string) map[string]any {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err == nil && record["message"] == message {
			return record
		}
	}
	t.Fatalf("debug log record %q not found in %q", message, logs)
	return nil
}

func TestChatGoogleAdapter(t *testing.T) {
	var gotPath string
	var gotAPIKeyHeader string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAPIKeyHeader = r.Header.Get("x-goog-api-key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"},{"functionCall":{"id":"call_google_1","name":"run_unix_command","args":{"command":"pwd"}}}]}}],"usageMetadata":{"promptTokenCount":9,"candidatesTokenCount":4}}`))
	}))
	defer srv.Close()

	cfg := aiConfig{
		BaseURL:   srv.URL,
		Model:     "gemini-2.5-flash",
		AuthToken: "test-key",
		Provider:  providerGoogle,
	}
	resp, err := chat(context.Background(), cfg, []message{{Role: "user", Content: "where am i"}}, nil)
	if err != nil {
		t.Fatalf("chat returned error: %v", err)
	}

	if !strings.Contains(gotPath, "gemini-2.5-flash") || !strings.Contains(gotPath, "generateContent") {
		t.Fatalf("unexpected path: got %q", gotPath)
	}
	if gotAPIKeyHeader != "test-key" {
		t.Fatalf("expected API key header, got %q", gotAPIKeyHeader)
	}
	if resp.Message.Content != "ok" {
		t.Fatalf("unexpected content: got %q", resp.Message.Content)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %d", len(resp.Message.ToolCalls))
	}
	if resp.Message.ToolCalls[0].ID != "call_google_1" {
		t.Fatalf("unexpected tool call id: got %q", resp.Message.ToolCalls[0].ID)
	}
}

func TestChatGoogleAdapterMapsErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":429,"message":"quota exceeded","status":"RESOURCE_EXHAUSTED"}}`))
	}))
	defer srv.Close()

	cfg := aiConfig{
		BaseURL:   srv.URL,
		Model:     "gemini-2.5-flash",
		AuthToken: "test-key",
		Provider:  providerGoogle,
	}
	t.Setenv("ASH_RETRY_MAX_ATTEMPTS", "1")
	_, err := chat(context.Background(), cfg, []message{{Role: "user", Content: "hi"}}, nil)
	if err == nil {
		t.Fatal("expected an error for a 429 response")
	}
	var statusErr chatStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected chatStatusError, got %T: %v", err, err)
	}
	if statusErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("StatusCode = %d, want 429", statusErr.StatusCode)
	}
}

func TestChatGoogleAdapterCancelsPromptly(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	cfg := aiConfig{
		BaseURL:   srv.URL,
		Model:     "gemini-2.5-flash",
		AuthToken: "test-key",
		Provider:  providerGoogle,
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := chat(ctx, cfg, []message{{Role: "user", Content: "hi"}}, nil)
		result <- err
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-result:
		if err == nil {
			t.Fatal("expected an error after context cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("chat did not return promptly after context cancellation")
	}
}

func TestChatAnthropicAdapter(t *testing.T) {
	var gotPath string
	var gotAPIKey string
	var gotVersion string
	var gotBody string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAPIKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		payload, _ := io.ReadAll(r.Body)
		gotBody = string(payload)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ready"},{"type":"tool_use","id":"toolu_1","name":"run_unix_command","input":{"command":"date"}}],"usage":{"input_tokens":12,"output_tokens":6}}`))
	}))
	defer srv.Close()

	cfg := aiConfig{
		BaseURL:          srv.URL,
		Model:            "claude-sonnet-4-5",
		Authorization:    "Bearer anth-token",
		AuthToken:        "anth-token",
		Provider:         providerAnthropic,
		UseNativeCaching: true,
	}
	resp, err := chat(context.Background(), cfg, []message{{Role: "user", Content: "hello"}}, nil)
	if err != nil {
		t.Fatalf("chat returned error: %v", err)
	}

	if gotPath != "/v1/messages" {
		t.Fatalf("unexpected path: got %q", gotPath)
	}
	if gotAPIKey != "anth-token" {
		t.Fatalf("unexpected x-api-key: got %q", gotAPIKey)
	}
	if gotVersion == "" {
		t.Fatalf("expected an anthropic-version header, got none")
	}
	if !strings.Contains(gotBody, `"cache_control":{"type":"ephemeral"}`) {
		t.Fatalf("expected cache_control in anthropic payload, got %s", gotBody)
	}
	if resp.Message.Content != "ready" {
		t.Fatalf("unexpected content: got %q", resp.Message.Content)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %d", len(resp.Message.ToolCalls))
	}
	if resp.Message.ToolCalls[0].ID != "toolu_1" {
		t.Fatalf("unexpected tool call id: got %q", resp.Message.ToolCalls[0].ID)
	}
	if !resp.Usage.Available || resp.Usage.InputTokens != 12 || resp.Usage.OutputTokens != 6 {
		t.Fatalf("unexpected usage: %+v", resp.Usage)
	}
}

func TestChatAnthropicAdapterMapsErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"type":"overloaded_error","message":"overloaded"}}`))
	}))
	defer srv.Close()

	cfg := aiConfig{
		BaseURL:       srv.URL,
		Model:         "claude-sonnet-4-5",
		Authorization: "Bearer anth-token",
		AuthToken:     "anth-token",
		Provider:      providerAnthropic,
	}
	t.Setenv("ASH_RETRY_MAX_ATTEMPTS", "1")
	_, err := chat(context.Background(), cfg, []message{{Role: "user", Content: "hi"}}, nil)
	if err == nil {
		t.Fatal("expected an error for a 503 response")
	}
	var statusErr chatStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected chatStatusError, got %T: %v", err, err)
	}
	if statusErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("StatusCode = %d, want 503", statusErr.StatusCode)
	}
}

func TestChatAnthropicAdapterCancelsPromptly(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	cfg := aiConfig{
		BaseURL:       srv.URL,
		Model:         "claude-sonnet-4-5",
		Authorization: "Bearer anth-token",
		AuthToken:     "anth-token",
		Provider:      providerAnthropic,
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := chat(ctx, cfg, []message{{Role: "user", Content: "hi"}}, nil)
		result <- err
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-result:
		if err == nil {
			t.Fatal("expected an error after context cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("chat did not return promptly after context cancellation")
	}
}

func TestChatCohereAdapter(t *testing.T) {
	var gotPath string
	var gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"1","finish_reason":"COMPLETE","message":{"role":"assistant","content":[{"type":"text","text":"ok"}],"tool_calls":[{"id":"call_1","type":"function","function":{"name":"run_unix_command","arguments":"{\"command\":\"pwd\"}"}}]},"usage":{"tokens":{"input_tokens":7,"output_tokens":4}}}`))
	}))
	defer srv.Close()

	cfg := aiConfig{
		BaseURL:   srv.URL,
		Model:     "command-r-plus",
		AuthToken: "cohere-token",
		Provider:  providerCohere,
	}
	resp, err := chat(context.Background(), cfg, []message{{Role: "user", Content: "where am i"}}, nil)
	if err != nil {
		t.Fatalf("chat returned error: %v", err)
	}

	if !strings.Contains(gotPath, "chat") {
		t.Fatalf("unexpected path: got %q", gotPath)
	}
	if gotAuth != "Bearer cohere-token" {
		t.Fatalf("unexpected auth header: got %q", gotAuth)
	}
	if resp.Message.Content != "ok" {
		t.Fatalf("unexpected content: got %q", resp.Message.Content)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].ID != "call_1" {
		t.Fatalf("unexpected tool calls: %+v", resp.Message.ToolCalls)
	}
	if !resp.Usage.Available || resp.Usage.InputTokens != 7 || resp.Usage.OutputTokens != 4 {
		t.Fatalf("unexpected usage: %+v", resp.Usage)
	}
}

func TestChatCohereAdapterMapsErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"invalid api token"}`))
	}))
	defer srv.Close()

	cfg := aiConfig{
		BaseURL:   srv.URL,
		Model:     "command-r-plus",
		AuthToken: "bad-token",
		Provider:  providerCohere,
	}
	t.Setenv("ASH_RETRY_MAX_ATTEMPTS", "1")
	_, err := chat(context.Background(), cfg, []message{{Role: "user", Content: "hi"}}, nil)
	if err == nil {
		t.Fatal("expected an error for a 401 response")
	}
	var statusErr chatStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected chatStatusError, got %T: %v", err, err)
	}
	if statusErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("StatusCode = %d, want 401", statusErr.StatusCode)
	}
}

func TestChatCohereAdapterCancelsPromptly(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	cfg := aiConfig{
		BaseURL:   srv.URL,
		Model:     "command-r-plus",
		AuthToken: "cohere-token",
		Provider:  providerCohere,
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := chat(ctx, cfg, []message{{Role: "user", Content: "hi"}}, nil)
		result <- err
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-result:
		if err == nil {
			t.Fatal("expected an error after context cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("chat did not return promptly after context cancellation")
	}
}

func TestChatBedrockAdapter(t *testing.T) {
	var gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":{"message":{"role":"assistant","content":[{"text":"ok"},{"toolUse":{"toolUseId":"call_1","name":"run_unix_command","input":{"command":"pwd"}}}]}},"stopReason":"tool_use","usage":{"inputTokens":8,"outputTokens":5,"totalTokens":13}}`))
	}))
	defer srv.Close()

	t.Setenv("AWS_ACCESS_KEY_ID", "test-access-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret-key")
	t.Setenv("AWS_REGION", "us-east-1")

	cfg := aiConfig{
		BaseURL:  srv.URL,
		Model:    "anthropic.claude-3-5-sonnet-20241022-v2:0",
		Provider: providerBedrock,
	}
	resp, err := chat(context.Background(), cfg, []message{{Role: "user", Content: "where am i"}}, nil)
	if err != nil {
		t.Fatalf("chat returned error: %v", err)
	}

	if !strings.Contains(gotPath, "converse") {
		t.Fatalf("unexpected path: got %q", gotPath)
	}
	if resp.Message.Content != "ok" {
		t.Fatalf("unexpected content: got %q", resp.Message.Content)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].ID != "call_1" {
		t.Fatalf("unexpected tool calls: %+v", resp.Message.ToolCalls)
	}
	if !resp.Usage.Available || resp.Usage.InputTokens != 8 || resp.Usage.OutputTokens != 5 {
		t.Fatalf("unexpected usage: %+v", resp.Usage)
	}
}

func TestChatBedrockAdapterMapsErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"access denied"}`))
	}))
	defer srv.Close()

	t.Setenv("AWS_ACCESS_KEY_ID", "test-access-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret-key")
	t.Setenv("AWS_REGION", "us-east-1")

	cfg := aiConfig{
		BaseURL:  srv.URL,
		Model:    "anthropic.claude-3-5-sonnet-20241022-v2:0",
		Provider: providerBedrock,
	}
	t.Setenv("ASH_RETRY_MAX_ATTEMPTS", "1")
	_, err := chat(context.Background(), cfg, []message{{Role: "user", Content: "hi"}}, nil)
	if err == nil {
		t.Fatal("expected an error for a 403 response")
	}
	var statusErr chatStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected chatStatusError, got %T: %v", err, err)
	}
	if statusErr.StatusCode != http.StatusForbidden {
		t.Fatalf("StatusCode = %d, want 403", statusErr.StatusCode)
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
	if !strings.Contains(logs, `"message":"AI request payload"`) {
		t.Fatalf("expected payload debug log, got %q", logs)
	}
	if !strings.Contains(logs, `"bytes":`) || !strings.Contains(logs, `"sha256":`) {
		t.Fatalf("expected redacted payload metadata in logs, got %q", logs)
	}
	if strings.Contains(logs, `run_unix_command`) || strings.Contains(logs, `run command`) {
		t.Fatalf("raw tool schema leaked into logs: %q", logs)
	}
	if !strings.Contains(logs, `"message":"AI response"`) {
		t.Fatalf("expected response debug log, got %q", logs)
	}
	if !strings.Contains(logs, `"status":200`) {
		t.Fatalf("expected response status in debug log, got %q", logs)
	}
}

func TestChatReusesHTTPClientAcrossRetries(t *testing.T) {
	t.Setenv(brokerSocketEnv, "")
	t.Setenv(brokerTokenEnv, "")

	origClient := newHTTPClient
	t.Cleanup(func() { newHTTPClient = origClient })
	t.Setenv("ASH_RETRY_MAX_ATTEMPTS", "3")
	t.Setenv("ASH_RETRY_BASE_DELAY", "0")
	t.Setenv("ASH_RETRY_MAX_DELAY", "0")

	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"temporary"}`))
			return
		}
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"ok"}}`))
	}))
	defer srv.Close()

	var clients atomic.Int32
	newHTTPClient = func(timeout time.Duration) *http.Client {
		clients.Add(1)
		return &http.Client{Timeout: timeout}
	}
	response, err := chat(context.Background(), testAIConfig(srv.URL, "model"), []message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("chat returned error: %v", err)
	}
	if response.Message.Content != "ok" || requests.Load() != 3 {
		t.Fatalf("unexpected retry result: %+v, requests=%d", response.Message, requests.Load())
	}
	if clients.Load() != 1 {
		t.Fatalf("HTTP client factory called %d times, want 1", clients.Load())
	}
}
