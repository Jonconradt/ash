package main

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestVerboseLoggingEnabledAcceptsTruthyValues(t *testing.T) {
	for _, value := range []string{"1", "Y", "y", "Yes", "TRUE", "on", "debug"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("ASH_VERBOSE", value)
			if !verboseLoggingEnabled() {
				t.Fatalf("expected ASH_VERBOSE=%q to enable verbose reporting", value)
			}
		})
	}

	for _, value := range []string{"", "0", "N", "no", "false", "off"} {
		t.Run("disabled_"+value, func(t *testing.T) {
			t.Setenv("ASH_VERBOSE", value)
			if verboseLoggingEnabled() {
				t.Fatalf("expected ASH_VERBOSE=%q to disable verbose reporting", value)
			}
		})
	}
}

func TestExecutionMetricsAggregateStagesToolsAndTokens(t *testing.T) {
	metrics := newExecutionMetrics(time.Now())
	metrics.addStageDuration(metricsStageDefaults, 200*time.Millisecond)
	metrics.addStageDuration(metricsStageDefaults, 300*time.Millisecond)
	metrics.addStageDuration(metricsStageAIProcessing, 2*time.Second)
	metrics.addToolCall("run_unix_command", 40*time.Millisecond)
	metrics.addToolCall("run_unix_command", 60*time.Millisecond)
	metrics.addTokenUsage(12, 8, true)
	metrics.addTokenUsage(30, 20, true)

	if got := metrics.stageDuration(metricsStageDefaults); got != 500*time.Millisecond {
		t.Fatalf("defaults duration = %v, want 500ms", got)
	}
	if metrics.toolCalls != 2 || metrics.toolDuration != 100*time.Millisecond {
		t.Fatalf("tool metrics = count %d duration %v, want 2 and 100ms", metrics.toolCalls, metrics.toolDuration)
	}
	if metrics.inputTokens != 42 || metrics.outputTokens != 28 {
		t.Fatalf("token totals = %d/%d, want 42/28", metrics.inputTokens, metrics.outputTokens)
	}
}

func TestRenderExecutionDashboard(t *testing.T) {
	originalVersion := ashVersion
	originalCommit := ashCommit
	originalDevelopmentBuild := ashDevelopmentBuild
	ashVersion = "0.17.1"
	ashCommit = "0123456789abcdef"
	ashDevelopmentBuild = "true"
	t.Cleanup(func() {
		ashVersion = originalVersion
		ashCommit = originalCommit
		ashDevelopmentBuild = originalDevelopmentBuild
	})

	metrics := newExecutionMetrics(time.Now())
	metrics.addStageDuration(metricsStageDefaults, 500*time.Millisecond)
	metrics.addStageDuration(metricsStageConnect, 2*time.Second)
	metrics.addStageDuration(metricsStageAIProcessing, 1250*time.Millisecond)
	metrics.addToolCall("run_unix_command", 100*time.Millisecond)
	metrics.addToolCall("run_unix_command", 100*time.Millisecond)
	metrics.finish(time.Now().Add(4 * time.Second))

	output := renderExecutionDashboard(metrics, false)
	for _, expected := range []string{
		"ASH v0.17.1 (dev:cdef) EXECUTION SUMMARY",
		"Loading defaults",
		"Connecting to AI server",
		"Connection reused    no",
		"AI processing",
		"Tool calls",
		"Total realtime",
		"2 tools",
		"Input tokens         N/A",
		"Output tokens        N/A",
		"500 ms",
		"2.00 s",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("dashboard missing %q:\n%s", expected, output)
		}
	}
}

func TestExecutionDashboardVersion(t *testing.T) {
	originalVersion := ashVersion
	originalCommit := ashCommit
	originalDevelopmentBuild := ashDevelopmentBuild
	t.Cleanup(func() {
		ashVersion = originalVersion
		ashCommit = originalCommit
		ashDevelopmentBuild = originalDevelopmentBuild
	})

	tests := []struct {
		name        string
		version     string
		commit      string
		development string
		want        string
	}{
		{name: "release tag", version: "v0.17.1", commit: "0123456789abcdef", development: "false", want: "v0.17.1"},
		{name: "development build", version: "v0.17.1", commit: "0123456789abcdef", development: "true", want: "v0.17.1 (dev:cdef)"},
		{name: "development build with short commit", version: "0.17.1", commit: "abcd", development: "true", want: "v0.17.1 (dev:abcd)"},
		{name: "development build without commit", version: "dev", commit: "unknown", development: "true", want: "vdev (dev)"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ashVersion = test.version
			ashCommit = test.commit
			ashDevelopmentBuild = test.development
			if got := executionDashboardVersion(); got != test.want {
				t.Fatalf("executionDashboardVersion() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestExecutionMetricsMissingUsageRemainsUnavailable(t *testing.T) {
	metrics := newExecutionMetrics(time.Now())
	metrics.addTokenUsage(10, 20, false)

	if metrics.inputTokensAvailable || metrics.outputTokensAvailable {
		t.Fatalf("missing token usage should remain unavailable")
	}
	if strings.Contains(renderExecutionDashboard(metrics, false), "10") {
		t.Fatalf("dashboard should not report unavailable token values")
	}
}

func TestExecutionMetricsConcurrentUpdates(t *testing.T) {
	metrics := newExecutionMetrics(time.Now())
	var group sync.WaitGroup
	for i := 0; i < 32; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			metrics.addStageDuration(metricsStageAIProcessing, time.Millisecond)
			metrics.addToolCall("run_unix_command", time.Millisecond)
			metrics.addSubAgent(time.Millisecond, toolCommandResult{OK: true})
			metrics.addTokenUsage(1, 1, true)
		}()
	}
	group.Wait()
	snap := metrics.snapshot()
	if snap.ToolCalls != 32 || snap.SubAgentCalls != 32 || snap.InputTokens != 32 || snap.OutputTokens != 32 {
		t.Fatalf("unexpected concurrent metrics: tools=%d agents=%d input=%d output=%d", snap.ToolCalls, snap.SubAgentCalls, snap.InputTokens, snap.OutputTokens)
	}
	if snap.ToolCallCounts["run_unix_command"] != 32 {
		t.Fatalf("expected per-tool count 32 for run_unix_command, got %d", snap.ToolCallCounts["run_unix_command"])
	}
}

func TestProviderResponseUsageIsNormalized(t *testing.T) {
	tests := []struct {
		name    string
		adapter providerAdapter
		body    string
		input   int
		output  int
	}{
		{
			name:    "ollama",
			adapter: ollamaAdapter{},
			body:    `{"message":{"role":"assistant","content":"ok"},"prompt_eval_count":12,"eval_count":8}`,
			input:   12,
			output:  8,
		},
		{
			name:    "openai",
			adapter: openAIAdapter{},
			body:    `{"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":30,"output_tokens":20}}`,
			input:   30,
			output:  20,
		},
		{
			name:    "google",
			adapter: googleAdapter{},
			body:    `{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":40,"completion_tokens":10}}`,
			input:   40,
			output:  10,
		},
		{
			name:    "anthropic",
			adapter: anthropicAdapter{},
			body:    `{"content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":50,"output_tokens":15}}`,
			input:   50,
			output:  15,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, err := tt.adapter.ParseResponse([]byte(tt.body))
			if err != nil {
				t.Fatalf("ParseResponse returned error: %v", err)
			}
			if !response.Usage.Available || response.Usage.InputTokens != tt.input || response.Usage.OutputTokens != tt.output {
				t.Fatalf("usage = %#v, want available %d/%d", response.Usage, tt.input, tt.output)
			}
		})
	}
}

func TestRunPrintsDashboardOnEarlyExitWhenVerbose(t *testing.T) {
	originalInteractive := stdinIsInteractive
	originalVersion := ashVersion
	originalDevelopmentBuild := ashDevelopmentBuild
	t.Cleanup(func() {
		stdinIsInteractive = originalInteractive
		ashVersion = originalVersion
		ashDevelopmentBuild = originalDevelopmentBuild
	})
	stdinIsInteractive = func() bool { return true }
	ashVersion = "0.17.1"
	ashDevelopmentBuild = "false"
	t.Setenv("ASH_VERBOSE", "Yes")

	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 1 {
		t.Fatalf("run returned %d, want 1", code)
	}
	if !strings.Contains(stdout.String(), "ASH v0.17.1 EXECUTION SUMMARY") {
		t.Fatalf("dashboard was not written to stdout: %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "\033[") {
		t.Fatalf("buffered dashboard should not contain ANSI escapes: %q", stdout.String())
	}
}
