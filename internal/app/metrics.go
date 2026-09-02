package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"ash/internal/model"
)

type metricsStage string

const (
	metricsStageDefaults     metricsStage = "defaults"
	metricsStageConnect      metricsStage = "connect"
	metricsStageAIProcessing metricsStage = "ai_processing"
)

type chatUsage = model.ChatUsage

type executionMetrics struct {
	mu                    sync.RWMutex
	startedAt             time.Time
	finishedAt            time.Time
	stages                map[metricsStage]time.Duration
	toolCalls             int
	toolDuration          time.Duration
	toolCallCounts        map[string]int
	scratchWrites         map[string]int
	scratchExecs          map[string]int
	subAgentCalls         int
	subAgentDuration      time.Duration
	subAgentCanceled      int
	subAgentTimedOut      int
	subAgentFailed        int
	inputTokens           int
	outputTokens          int
	inputTokensAvailable  bool
	outputTokensAvailable bool
	connectionReused      bool
	connectionObserved    bool
}

// unknownToolName buckets tool-call counts when the invoked tool's name is blank.
const unknownToolName = "unknown"

// executionMetricsSnapshot is an immutable, race-free copy of executionMetrics for reporting.
type executionMetricsSnapshot struct {
	ToolCalls             int
	ToolDuration          time.Duration
	ToolCallCounts        map[string]int
	ScratchWrites         map[string]int
	ScratchExecs          map[string]int
	SubAgentCalls         int
	SubAgentDuration      time.Duration
	SubAgentCanceled      int
	SubAgentTimedOut      int
	SubAgentFailed        int
	InputTokens           int
	OutputTokens          int
	InputTokensAvailable  bool
	OutputTokensAvailable bool
}

type metricsContextKey struct{}

type requestIDContextKey struct{}

// withRequestID attaches a per-invocation request ID to ctx for downstream log correlation.
func withRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDContextKey{}, requestID)
}

// requestIDFromContext returns the request ID attached via withRequestID, or "" if none was set.
func requestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	requestID, _ := ctx.Value(requestIDContextKey{}).(string)
	return requestID
}

func newExecutionMetrics(startedAt time.Time) *executionMetrics {
	return &executionMetrics{
		startedAt:      startedAt,
		stages:         make(map[metricsStage]time.Duration),
		toolCallCounts: make(map[string]int),
		scratchWrites:  make(map[string]int),
		scratchExecs:   make(map[string]int),
	}
}

func (m *executionMetrics) addStageDuration(stage metricsStage, duration time.Duration) {
	if m == nil || duration < 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stages[stage] += duration
}

func (m *executionMetrics) stageDuration(stage metricsStage) time.Duration {
	if m == nil {
		return 0
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stages[stage]
}

// addToolCall records one tool invocation's duration and increments its per-name count.
// A blank name is bucketed under unknownToolName rather than silently discarded.
func (m *executionMetrics) addToolCall(name string, duration time.Duration) {
	if m == nil {
		return
	}
	if name == "" {
		name = unknownToolName
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.toolCalls++
	if duration > 0 {
		m.toolDuration += duration
	}
	m.toolCallCounts[name]++
}

// addScratchWrite records one write/append/edit to the scratch file at relPath.
// An empty path is ignored to avoid a spurious map entry.
func (m *executionMetrics) addScratchWrite(relPath string) {
	if m == nil || relPath == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scratchWrites[relPath]++
}

// addScratchExec records one execution of the scratch file at relPath.
// An empty path is ignored to avoid a spurious map entry.
func (m *executionMetrics) addScratchExec(relPath string) {
	if m == nil || relPath == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scratchExecs[relPath]++
}

func (m *executionMetrics) addSubAgent(duration time.Duration, result toolCommandResult) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subAgentCalls++
	if duration > 0 {
		m.subAgentDuration += duration
	}
	switch {
	case strings.Contains(result.Error, "canceled"):
		m.subAgentCanceled++
	case strings.Contains(result.Error, "timed out"):
		m.subAgentTimedOut++
	case !result.OK:
		m.subAgentFailed++
	}
}

func (m *executionMetrics) addTokenUsage(inputTokens, outputTokens int, available bool) {
	if m == nil || !available {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inputTokens += inputTokens
	m.outputTokens += outputTokens
	m.inputTokensAvailable = true
	m.outputTokensAvailable = true
}

// recordAIResponseMetrics folds one completed AI call into the dashboard: its
// provider-reported token usage, plus its wall time minus whatever connect time
// the transport recorded during the same call (which is reported separately).
func recordAIResponseMetrics(ctx context.Context, usage chatUsage, startedAt time.Time, connectBefore time.Duration) {
	metrics := executionMetricsFromContext(ctx)
	if metrics == nil {
		return
	}
	metrics.addTokenUsage(usage.InputTokens, usage.OutputTokens, usage.Available)
	processing := time.Since(startedAt) - (metrics.stageDuration(metricsStageConnect) - connectBefore)
	if processing < 0 {
		processing = 0
	}
	metrics.addStageDuration(metricsStageAIProcessing, processing)
}

func (m *executionMetrics) setConnectionReused(reused bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connectionObserved = true
	m.connectionReused = m.connectionReused || reused
}

func (m *executionMetrics) finish(finishedAt time.Time) {
	if m != nil {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.finishedAt = finishedAt
	}
}

func withExecutionMetrics(ctx context.Context, metrics *executionMetrics) context.Context {
	return context.WithValue(ctx, metricsContextKey{}, metrics)
}

func executionMetricsFromContext(ctx context.Context) *executionMetrics {
	if ctx == nil {
		return nil
	}
	metrics, _ := ctx.Value(metricsContextKey{}).(*executionMetrics)
	return metrics
}

func formatMetricDuration(duration time.Duration) string {
	if duration < time.Second {
		return fmt.Sprintf("%d ms", duration.Milliseconds())
	}
	return fmt.Sprintf("%.2f s", duration.Seconds())
}

func (m *executionMetrics) totalDuration() time.Duration {
	if m == nil {
		return 0
	}
	m.mu.RLock()
	startedAt := m.startedAt
	finishedAt := m.finishedAt
	m.mu.RUnlock()
	if startedAt.IsZero() {
		return 0
	}
	if finishedAt.IsZero() {
		finishedAt = time.Now()
	}
	if finishedAt.Before(startedAt) {
		return 0
	}
	return finishedAt.Sub(startedAt)
}

// snapshot returns a race-free copy of the metrics for reporting. Calling snapshot on a
// nil receiver returns a zero-value snapshot rather than panicking.
func (m *executionMetrics) snapshot() executionMetricsSnapshot {
	if m == nil {
		return executionMetricsSnapshot{}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return executionMetricsSnapshot{
		ToolCalls:             m.toolCalls,
		ToolDuration:          m.toolDuration,
		ToolCallCounts:        copyStringIntMap(m.toolCallCounts),
		ScratchWrites:         copyStringIntMap(m.scratchWrites),
		ScratchExecs:          copyStringIntMap(m.scratchExecs),
		SubAgentCalls:         m.subAgentCalls,
		SubAgentDuration:      m.subAgentDuration,
		SubAgentCanceled:      m.subAgentCanceled,
		SubAgentTimedOut:      m.subAgentTimedOut,
		SubAgentFailed:        m.subAgentFailed,
		InputTokens:           m.inputTokens,
		OutputTokens:          m.outputTokens,
		InputTokensAvailable:  m.inputTokensAvailable,
		OutputTokensAvailable: m.outputTokensAvailable,
	}
}

// copyStringIntMap returns an independent copy of src so callers can't mutate metrics state.
func copyStringIntMap(src map[string]int) map[string]int {
	dst := make(map[string]int, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func renderExecutionDashboard(metrics *executionMetrics, ansi bool) string {
	if metrics == nil {
		return ""
	}
	const (
		reset = "\033[0m"
		cyan  = "\033[36m"
		bold  = "\033[1m"
	)
	style := func(value string) string {
		if !ansi {
			return value
		}
		return cyan + value + reset
	}
	header := fmt.Sprintf("ASH %s EXECUTION SUMMARY", executionDashboardVersion())
	if ansi {
		header = bold + header + reset
	}
	snap := metrics.snapshot()
	inputTokens := "N/A"
	if snap.InputTokensAvailable {
		inputTokens = strconv.Itoa(snap.InputTokens)
	}
	outputTokens := "N/A"
	if snap.OutputTokensAvailable {
		outputTokens = strconv.Itoa(snap.OutputTokens)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n%s\n", header)
	fmt.Fprintf(&b, "%-20s %s\n", style("Loading defaults"), formatMetricDuration(metrics.stageDuration(metricsStageDefaults)))
	fmt.Fprintf(&b, "%-20s %s\n", style("Connecting to AI server"), formatMetricDuration(metrics.stageDuration(metricsStageConnect)))
	connectionStatus := "no"
	metrics.mu.RLock()
	if metrics.connectionObserved && metrics.connectionReused {
		connectionStatus = "yes"
	}
	metrics.mu.RUnlock()
	fmt.Fprintf(&b, "%-20s %s\n", style("Connection reused"), connectionStatus)
	fmt.Fprintf(&b, "%-20s %s\n", style("AI processing"), formatMetricDuration(metrics.stageDuration(metricsStageAIProcessing)))
	fmt.Fprintf(&b, "%-20s %d tools (%s)\n", style("Tool calls"), snap.ToolCalls, formatMetricDuration(snap.ToolDuration))
	writeCountBreakdown(&b, style("  by tool"), snap.ToolCallCounts)
	fmt.Fprintf(&b, "%-20s %d (%s), canceled %d, timed out %d, failed %d\n", style("Sub-agents"), snap.SubAgentCalls, formatMetricDuration(snap.SubAgentDuration), snap.SubAgentCanceled, snap.SubAgentTimedOut, snap.SubAgentFailed)
	fmt.Fprintf(&b, "%-20s %s\n", style("Input tokens"), inputTokens)
	fmt.Fprintf(&b, "%-20s %s\n", style("Output tokens"), outputTokens)
	writeCountBreakdown(&b, style("Scratch files written"), snap.ScratchWrites)
	writeCountBreakdown(&b, style("Scratch files executed"), snap.ScratchExecs)
	fmt.Fprintf(&b, "%-20s %s\n", style("Total realtime"), formatMetricDuration(metrics.totalDuration()))
	return b.String()
}

// connectionWasReused reports whether an observed HTTP connection was reused.
func (m *executionMetrics) connectionWasReused() bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.connectionObserved && m.connectionReused
}

// logExecutionSummary emits the execution summary as a structured info-level record so the
// same values the dashboard prints are available as JSON for analysis.
func logExecutionSummary(requestID string, metrics *executionMetrics) {
	if metrics == nil {
		return
	}
	snap := metrics.snapshot()
	slog.Info("execution summary",
		"request_id", requestID,
		"version", executionDashboardVersion(),
		"defaults_ms", metrics.stageDuration(metricsStageDefaults).Milliseconds(),
		"connect_ms", metrics.stageDuration(metricsStageConnect).Milliseconds(),
		"connection_reused", metrics.connectionWasReused(),
		"ai_processing_ms", metrics.stageDuration(metricsStageAIProcessing).Milliseconds(),
		"tool_calls", snap.ToolCalls,
		"tool_duration_ms", snap.ToolDuration.Milliseconds(),
		"tool_call_counts", snap.ToolCallCounts,
		"sub_agent_calls", snap.SubAgentCalls,
		"sub_agent_duration_ms", snap.SubAgentDuration.Milliseconds(),
		"sub_agent_canceled", snap.SubAgentCanceled,
		"sub_agent_timed_out", snap.SubAgentTimedOut,
		"sub_agent_failed", snap.SubAgentFailed,
		"input_tokens", snap.InputTokens,
		"input_tokens_available", snap.InputTokensAvailable,
		"output_tokens", snap.OutputTokens,
		"output_tokens_available", snap.OutputTokensAvailable,
		"scratch_files_written", snap.ScratchWrites,
		"scratch_files_executed", snap.ScratchExecs,
		"total_realtime_ms", metrics.totalDuration().Milliseconds(),
		"EID", "Xr4mTq7A",
	)
}

func executionDashboardVersion() string {
	version := strings.TrimSpace(ashVersion)
	if version == "" {
		version = "dev"
	}
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	if ashDevelopmentBuild != "true" {
		return version
	}
	commit := strings.TrimSpace(ashCommit)
	if commit == "" || commit == "unknown" {
		return version + " (dev)"
	}
	if len(commit) > 4 {
		commit = commit[len(commit)-4:]
	}
	return fmt.Sprintf("%s (dev:%s)", version, commit)
}

// writeCountBreakdown writes one line per key in counts (sorted for deterministic output),
// preceded by a header line. It writes nothing when counts is empty.
// A count of 1 is omitted because it carries no information beyond the key being listed.
func writeCountBreakdown(b *strings.Builder, header string, counts map[string]int) {
	if len(counts) == 0 {
		return
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	fmt.Fprintf(b, "%s\n", header)
	for _, key := range keys {
		if counts[key] == 1 {
			fmt.Fprintf(b, "  %s\n", key)
			continue
		}
		fmt.Fprintf(b, "  %-30s %d\n", key, counts[key])
	}
}

func writerIsTerminal(writer io.Writer) bool {
	file, ok := writer.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
