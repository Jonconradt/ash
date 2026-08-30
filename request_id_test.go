package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const zeroRequestID = "0000000000000000"

// syncBuffer wraps bytes.Buffer with a mutex so it can safely capture writes from concurrent
// goroutines in tests (e.g. the thinking-indicator spinner racing with debug log writes).
// Production uses os.Stderr, whose Write calls don't exhibit this issue.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestRequestIDGeneratorIsRandom(t *testing.T) {
	first := requestIDGenerator()
	second := requestIDGenerator()

	if first == second {
		t.Fatalf("expected two independent calls to produce different IDs, both were %q", first)
	}
	if first == zeroRequestID || second == zeroRequestID {
		t.Fatalf("expected generator to never return the all-zero ID, got %q and %q", first, second)
	}
}

func TestRequestIDGeneratorLength(t *testing.T) {
	for i := 0; i < 10; i++ {
		id := requestIDGenerator()
		if len(id) != 16 {
			t.Fatalf("expected 16-character hex-encoded request ID, got %q (len %d)", id, len(id))
		}
	}
}

// TestRequestIDGeneratorNeverAllZero guards against silently reintroducing the unfilled-byte-slice bug.
func TestRequestIDGeneratorNeverAllZero(t *testing.T) {
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		id := requestIDGenerator()
		if id == zeroRequestID {
			t.Fatalf("generator returned the all-zero ID on iteration %d", i)
		}
		seen[id] = struct{}{}
	}
	if len(seen) < 990 {
		t.Fatalf("expected near-unique IDs across 1000 calls, only saw %d distinct values", len(seen))
	}
}

func TestWithRequestIDAndFromContext(t *testing.T) {
	ctx := withRequestID(context.Background(), "abc123")
	if got := requestIDFromContext(ctx); got != "abc123" {
		t.Fatalf("requestIDFromContext = %q, want %q", got, "abc123")
	}
}

func TestRequestIDFromContextMissing(t *testing.T) {
	if got := requestIDFromContext(context.Background()); got != "" {
		t.Fatalf("expected empty request ID when none set, got %q", got)
	}
}

func TestRequestIDFromContextNilContext(t *testing.T) {
	//lint:ignore SA1012 intentionally exercising the nil-context guard in requestIDFromContext
	//nolint:staticcheck // intentionally exercising the nil-context guard in requestIDFromContext
	if got := requestIDFromContext(nil); got != "" {
		t.Fatalf("expected empty request ID for nil context, got %q", got)
	}
}

// TestRunToolLoopUsesConsistentRequestIDAcrossLogLines verifies that every debug log line
// emitted during a single tool-loop invocation shares the same request_id, matching
// production behavior where run() attaches one ID to ctx before calling runToolLoop.
func TestRunToolLoopUsesConsistentRequestIDAcrossLogLines(t *testing.T) {
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
	ctx := withRequestID(context.Background(), "fixed-test-request-id")
	_, _, err := runToolLoop(ctx, testAIConfig(srv.URL, "model"), "list files", []message{{Role: "user", Content: "list files"}}, shim)
	if err != nil {
		t.Fatalf("runToolLoop returned error: %v", err)
	}

	requestIDs := extractDebugLogFieldValues(t, logOutput.String(), "request_id")
	if len(requestIDs) == 0 {
		t.Fatalf("expected at least one debug log line, got none")
	}
	for _, id := range requestIDs {
		if id != "fixed-test-request-id" {
			t.Fatalf("expected all log lines to share request_id %q, found %q in logs %q", "fixed-test-request-id", id, logOutput.String())
		}
	}
}

// TestRunGeneratesDifferentRequestIDPerInvocation confirms two separate run() invocations
// get independent request IDs, and that each invocation's ID is internally consistent.
func TestRunGeneratesDifferentRequestIDPerInvocation(t *testing.T) {
	origDebugWriter := debugWriter
	t.Cleanup(func() { debugWriter = origDebugWriter })

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AI", "")
	t.Setenv("ASH_VERBOSE", "1")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"ok"}}`))
	}))
	defer srv.Close()
	t.Setenv("AI_ENDPOINT", srv.URL)
	t.Setenv("AI_MODEL", "llama3.1")

	runOnce := func() string {
		var stdout bytes.Buffer
		var stderr syncBuffer
		if code := run([]string{"hello"}, &stdout, &stderr); code != 0 {
			t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
		}
		ids := extractDebugLogFieldValues(t, stderr.String(), "request_id")
		if len(ids) == 0 {
			t.Fatalf("expected debug logs to include request_id, got none in %q", stderr.String())
		}
		first := ids[0]
		for _, id := range ids {
			if id != first {
				t.Fatalf("expected single invocation to share one request_id, saw %q and %q", first, id)
			}
		}
		return first
	}

	firstID := runOnce()
	secondID := runOnce()
	if firstID == secondID {
		t.Fatalf("expected different request IDs across separate invocations, both were %q", firstID)
	}
}

// extractDebugLogFieldValues parses newline-delimited JSON debug log output and returns
// the string value of the given field from every line that contains it.
func extractDebugLogFieldValues(t *testing.T, logs, field string) []string {
	t.Helper()
	var values []string
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}
		raw, ok := record[field]
		if !ok {
			continue
		}
		value, ok := raw.(string)
		if !ok {
			continue
		}
		values = append(values, value)
	}
	return values
}
