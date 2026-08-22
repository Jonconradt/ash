package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

type metricsStage string

const (
	metricsStageDefaults     metricsStage = "defaults"
	metricsStageConnect      metricsStage = "connect"
	metricsStageAIProcessing metricsStage = "ai_processing"
)

type chatUsage struct {
	InputTokens  int
	OutputTokens int
	Available    bool
}

type executionMetrics struct {
	mu                    sync.RWMutex
	startedAt             time.Time
	finishedAt            time.Time
	stages                map[metricsStage]time.Duration
	toolCalls             int
	toolDuration          time.Duration
	subAgentCalls         int
	subAgentDuration      time.Duration
	subAgentCanceled      int
	subAgentTimedOut      int
	subAgentFailed        int
	inputTokens           int
	outputTokens          int
	inputTokensAvailable  bool
	outputTokensAvailable bool
}

type metricsContextKey struct{}

func newExecutionMetrics(startedAt time.Time) *executionMetrics {
	return &executionMetrics{
		startedAt: startedAt,
		stages:    make(map[metricsStage]time.Duration),
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

func (m *executionMetrics) addToolCall(duration time.Duration) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.toolCalls++
	if duration > 0 {
		m.toolDuration += duration
	}
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

func (m *executionMetrics) snapshot() (toolCalls int, toolDuration time.Duration, subAgentCalls int, subAgentDuration time.Duration, subAgentCanceled int, subAgentTimedOut int, subAgentFailed int, inputTokens int, outputTokens int, inputAvailable bool, outputAvailable bool) {
	if m == nil {
		return 0, 0, 0, 0, 0, 0, 0, 0, 0, false, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.toolCalls, m.toolDuration, m.subAgentCalls, m.subAgentDuration, m.subAgentCanceled, m.subAgentTimedOut, m.subAgentFailed, m.inputTokens, m.outputTokens, m.inputTokensAvailable, m.outputTokensAvailable
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
	header := "ASH EXECUTION SUMMARY"
	if ansi {
		header = bold + header + reset
	}
	toolCalls, toolDuration, subAgentCalls, subAgentDuration, subAgentCanceled, subAgentTimedOut, subAgentFailed, inputTokenCount, outputTokenCount, inputAvailable, outputAvailable := metrics.snapshot()
	inputTokens := "N/A"
	if inputAvailable {
		inputTokens = fmt.Sprintf("%d", inputTokenCount)
	}
	outputTokens := "N/A"
	if outputAvailable {
		outputTokens = fmt.Sprintf("%d", outputTokenCount)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n%s\n", header)
	fmt.Fprintf(&b, "%-20s %s\n", style("Loading defaults"), formatMetricDuration(metrics.stageDuration(metricsStageDefaults)))
	fmt.Fprintf(&b, "%-20s %s\n", style("Connecting to AI server"), formatMetricDuration(metrics.stageDuration(metricsStageConnect)))
	fmt.Fprintf(&b, "%-20s %s\n", style("AI processing"), formatMetricDuration(metrics.stageDuration(metricsStageAIProcessing)))
	fmt.Fprintf(&b, "%-20s %d tools (%s)\n", style("Tool calls"), toolCalls, formatMetricDuration(toolDuration))
	fmt.Fprintf(&b, "%-20s %d (%s), canceled %d, timed out %d, failed %d\n", style("Sub-agents"), subAgentCalls, formatMetricDuration(subAgentDuration), subAgentCanceled, subAgentTimedOut, subAgentFailed)
	fmt.Fprintf(&b, "%-20s %s\n", style("Input tokens"), inputTokens)
	fmt.Fprintf(&b, "%-20s %s\n", style("Output tokens"), outputTokens)
	fmt.Fprintf(&b, "%-20s %s\n", style("Total realtime"), formatMetricDuration(metrics.totalDuration()))
	return b.String()
}

func writerIsTerminal(writer io.Writer) bool {
	file, ok := writer.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
