package main

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestExecutionMetricsAddToolCallTracksPerNamedCount(t *testing.T) {
	metrics := newExecutionMetrics(time.Now())
	metrics.addToolCall("run_unix_command", time.Millisecond)
	metrics.addToolCall("run_unix_command", time.Millisecond)
	metrics.addToolCall("run_unix_command", time.Millisecond)
	metrics.addToolCall("ash_write_scratch_file", time.Millisecond)

	snap := metrics.snapshot()
	if got := snap.ToolCallCounts["run_unix_command"]; got != 3 {
		t.Fatalf("run_unix_command count = %d, want 3", got)
	}
	if got := snap.ToolCallCounts["ash_write_scratch_file"]; got != 1 {
		t.Fatalf("ash_write_scratch_file count = %d, want 1", got)
	}
	if len(snap.ToolCallCounts) != 2 {
		t.Fatalf("expected exactly 2 distinct tool names, got %d (%v)", len(snap.ToolCallCounts), snap.ToolCallCounts)
	}
}

// TestExecutionMetricsAddToolCallEmptyNameBucketedUnderUnknown documents and locks in the
// explicit decision to bucket blank tool names rather than silently discard them.
func TestExecutionMetricsAddToolCallEmptyNameBucketedUnderUnknown(t *testing.T) {
	metrics := newExecutionMetrics(time.Now())
	metrics.addToolCall("", time.Millisecond)

	snap := metrics.snapshot()
	if got := snap.ToolCallCounts[unknownToolName]; got != 1 {
		t.Fatalf("expected empty tool name bucketed under %q, got counts %v", unknownToolName, snap.ToolCallCounts)
	}
	if snap.ToolCalls != 1 {
		t.Fatalf("expected aggregate tool call count to still increment, got %d", snap.ToolCalls)
	}
}

func TestExecutionMetricsSnapshotOnNilMetricsReturnsZeroValue(t *testing.T) {
	var metrics *executionMetrics
	snap := metrics.snapshot()
	if snap.ToolCalls != 0 || snap.SubAgentCalls != 0 || len(snap.ToolCallCounts) != 0 {
		t.Fatalf("expected zero-value snapshot for nil metrics, got %+v", snap)
	}
	// addToolCall/addScratchWrite/addScratchExec on a nil receiver must not panic.
	metrics.addToolCall("run_unix_command", time.Millisecond)
	metrics.addScratchWrite("plan/notes.txt")
	metrics.addScratchExec("plan/notes.txt")
}

func TestExecutionMetricsAddScratchWriteTracksPerPathCount(t *testing.T) {
	metrics := newExecutionMetrics(time.Now())
	metrics.addScratchWrite("plan/notes.txt")
	metrics.addScratchWrite("plan/notes.txt")
	metrics.addScratchWrite("plan/other.txt")

	snap := metrics.snapshot()
	if got := snap.ScratchWrites["plan/notes.txt"]; got != 2 {
		t.Fatalf("plan/notes.txt write count = %d, want 2", got)
	}
	if got := snap.ScratchWrites["plan/other.txt"]; got != 1 {
		t.Fatalf("plan/other.txt write count = %d, want 1", got)
	}
}

func TestExecutionMetricsAddScratchWriteEmptyPathIgnored(t *testing.T) {
	metrics := newExecutionMetrics(time.Now())
	metrics.addScratchWrite("")

	snap := metrics.snapshot()
	if len(snap.ScratchWrites) != 0 {
		t.Fatalf("expected no scratch write entries for empty path, got %v", snap.ScratchWrites)
	}
}

func TestExecutionMetricsAddScratchExecTracksPerPathCount(t *testing.T) {
	metrics := newExecutionMetrics(time.Now())
	metrics.addScratchExec("plan/run.sh")
	metrics.addScratchExec("plan/run.sh")

	snap := metrics.snapshot()
	if got := snap.ScratchExecs["plan/run.sh"]; got != 2 {
		t.Fatalf("plan/run.sh exec count = %d, want 2", got)
	}
}

func TestExecutionMetricsAddScratchExecEmptyPathIgnored(t *testing.T) {
	metrics := newExecutionMetrics(time.Now())
	metrics.addScratchExec("")

	snap := metrics.snapshot()
	if len(snap.ScratchExecs) != 0 {
		t.Fatalf("expected no scratch exec entries for empty path, got %v", snap.ScratchExecs)
	}
}

// TestExecutionMetricsConcurrentToolAndScratchUpdatesNoRace exercises the mutex guarding the
// new maps; run with -race to catch data races.
func TestExecutionMetricsConcurrentToolAndScratchUpdatesNoRace(t *testing.T) {
	metrics := newExecutionMetrics(time.Now())
	var group sync.WaitGroup
	for i := 0; i < 32; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			metrics.addToolCall("run_unix_command", time.Millisecond)
			metrics.addScratchWrite("plan/notes.txt")
			metrics.addScratchExec("plan/run.sh")
		}()
	}
	group.Wait()

	snap := metrics.snapshot()
	if snap.ToolCallCounts["run_unix_command"] != 32 {
		t.Fatalf("expected 32 run_unix_command calls, got %d", snap.ToolCallCounts["run_unix_command"])
	}
	if snap.ScratchWrites["plan/notes.txt"] != 32 {
		t.Fatalf("expected 32 scratch writes, got %d", snap.ScratchWrites["plan/notes.txt"])
	}
	if snap.ScratchExecs["plan/run.sh"] != 32 {
		t.Fatalf("expected 32 scratch execs, got %d", snap.ScratchExecs["plan/run.sh"])
	}
}

func TestRenderExecutionDashboardOmitsEmptyToolBreakdownSection(t *testing.T) {
	metrics := newExecutionMetrics(time.Now())
	output := renderExecutionDashboard(metrics, false)
	if strings.Contains(output, "by tool") {
		t.Fatalf("expected no per-tool breakdown section when empty, got:\n%s", output)
	}
}

func TestRenderExecutionDashboardShowsToolBreakdownWhenPresent(t *testing.T) {
	metrics := newExecutionMetrics(time.Now())
	metrics.addToolCall("run_unix_command", time.Millisecond)
	metrics.addToolCall("run_unix_command", time.Millisecond)
	metrics.addToolCall("ash_write_scratch_file", time.Millisecond)

	output := renderExecutionDashboard(metrics, false)
	if !strings.Contains(output, "by tool") {
		t.Fatalf("expected per-tool breakdown section, got:\n%s", output)
	}
	byToolIdx := strings.Index(output, "by tool")
	nameA := strings.Index(output, "ash_write_scratch_file")
	nameB := strings.Index(output, "run_unix_command")
	if nameA < byToolIdx || nameB < byToolIdx {
		t.Fatalf("expected both tool names after the breakdown header, got:\n%s", output)
	}
	if nameA > nameB {
		t.Fatalf("expected alphabetical order (ash_write_scratch_file before run_unix_command), got:\n%s", output)
	}
	if !strings.Contains(output, "ash_write_scratch_file") || !strings.Contains(output, "1") {
		t.Fatalf("expected ash_write_scratch_file count of 1, got:\n%s", output)
	}
}

func TestRenderExecutionDashboardOmitsEmptyScratchSections(t *testing.T) {
	metrics := newExecutionMetrics(time.Now())
	output := renderExecutionDashboard(metrics, false)
	if strings.Contains(output, "Scratch files written") {
		t.Fatalf("expected no scratch-written section when empty, got:\n%s", output)
	}
	if strings.Contains(output, "Scratch files executed") {
		t.Fatalf("expected no scratch-executed section when empty, got:\n%s", output)
	}
}

func TestRenderExecutionDashboardShowsScratchSectionsWhenPresent(t *testing.T) {
	metrics := newExecutionMetrics(time.Now())
	metrics.addScratchWrite("plan/notes.txt")
	metrics.addScratchExec("plan/run.sh")

	output := renderExecutionDashboard(metrics, false)
	if !strings.Contains(output, "Scratch files written") || !strings.Contains(output, "plan/notes.txt") {
		t.Fatalf("expected scratch-written section with path, got:\n%s", output)
	}
	if !strings.Contains(output, "Scratch files executed") || !strings.Contains(output, "plan/run.sh") {
		t.Fatalf("expected scratch-executed section with path, got:\n%s", output)
	}
}

func TestRenderExecutionDashboardNilMetricsReturnsEmptyString(t *testing.T) {
	var metrics *executionMetrics
	if got := renderExecutionDashboard(metrics, false); got != "" {
		t.Fatalf("expected empty string for nil metrics, got %q", got)
	}
}
