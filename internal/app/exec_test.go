package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestMaxAgents(t *testing.T) {
	t.Setenv(maxAgentsEnvName, "")
	if got := maxAgents(); got != defaultMaxAgents {
		t.Fatalf("maxAgents default = %d, want %d", got, defaultMaxAgents)
	}
	t.Setenv(maxAgentsEnvName, "3")
	if got := maxAgents(); got != 3 {
		t.Fatalf("maxAgents custom = %d, want 3", got)
	}
	for _, value := range []string{"0", "-1", "not-a-number"} {
		t.Setenv(maxAgentsEnvName, value)
		if got := maxAgents(); got != defaultMaxAgents {
			t.Fatalf("maxAgents(%q) = %d, want %d", value, got, defaultMaxAgents)
		}
	}
}

func TestGenerateChildSessionID(t *testing.T) {
	childID, err := generateChildSessionID("parent_01")
	if err != nil {
		t.Fatalf("generateChildSessionID error: %v", err)
	}
	parts := strings.Split(childID, ".")
	if len(parts) != 2 || parts[0] != "parent_01" || len(parts[1]) != 6 {
		t.Fatalf("unexpected child session ID: %q", childID)
	}
	for _, char := range parts[1] {
		if !strings.ContainsRune(childSessionAlphabet, char) {
			t.Fatalf("child suffix contains invalid character %q", char)
		}
	}
}

func TestAgentBudget(t *testing.T) {
	budget := newAgentBudget(2)
	if !budget.reserve() {
		t.Fatal("expected first reservation to succeed")
	}
	if !budget.reserve() {
		t.Fatal("expected first two reservations to succeed")
	}
	if budget.reserve() {
		t.Fatal("expected budget exhaustion")
	}
	budget.release()
	if !budget.reserve() {
		t.Fatal("expected released budget slot to be reusable")
	}
}

func TestAgentBudgetConcurrentReservations(t *testing.T) {
	budget := newAgentBudget(defaultMaxAgents)
	var group sync.WaitGroup
	var reserved atomic.Int32
	for i := 0; i < 32; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if budget.reserve() {
				reserved.Add(1)
			}
		}()
	}
	group.Wait()
	if reserved.Load() != int32(defaultMaxAgents) {
		t.Fatalf("reserved %d agents, want %d", reserved.Load(), defaultMaxAgents)
	}
}

func TestChildAgentRejectsAshLaunchPaths(t *testing.T) {
	t.Setenv(childAgentEnvName, childAgentEnvValue)
	shim := localToolShim{allowlist: map[string]struct{}{"ash": {}, "crontab": {}}}

	for _, test := range []struct {
		name string
		tool string
		args map[string]any
	}{
		{name: "direct ash", tool: "run_unix_command", args: map[string]any{"command": "ash", "args": []any{"help"}}},
		{name: "pipeline ash", tool: "run_unix_pipeline", args: map[string]any{"pipeline": "printf hi | ash"}},
		{name: "future schedule", tool: "schedule_future_prompt", args: map[string]any{"prompt": "x", "when": "in 1 minute"}},
		{name: "recurring schedule", tool: "schedule_recurring_prompt", args: map[string]any{"prompt": "x", "cron": "0 0 * * *"}},
		{name: "job management", tool: "manage_recurring_jobs", args: map[string]any{"action": "list"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := shim.CallTool(context.Background(), test.tool, test.args)
			if !strings.Contains(result, "child agents cannot") {
				t.Fatalf("expected child restriction in result, got %q", result)
			}
		})
	}
}

func TestRunSubAgentCommand(t *testing.T) {
	origExecutable := osExecutable
	osExecutable = func() (string, error) { return "/bin/echo", nil }
	t.Cleanup(func() { osExecutable = origExecutable })
	t.Setenv("SESSION_ID", "parent")
	t.Setenv(childAgentEnvName, "")

	result := runSubAgentCommand(context.Background(), "child result", "parent.abc123")
	if !result.OK || result.ExitCode != 0 || !strings.Contains(result.Stdout, "child result") {
		t.Fatalf("unexpected sub-agent result: %+v", result)
	}
}

func TestRunSubAgentCommandTimeoutKillsProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group test is Unix-specific")
	}
	scriptPath := filepath.Join(t.TempDir(), "fake-ash.sh")
	pidPath := filepath.Join(t.TempDir(), "child.pid")
	script := "#!/bin/sh\nsleep 30 &\nprintf '%s' \"$!\" > \"$PID_PATH\"\nwait\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake ash: %v", err)
	}
	origExecutable := osExecutable
	osExecutable = func() (string, error) { return scriptPath, nil }
	t.Cleanup(func() { osExecutable = origExecutable })
	t.Setenv("PID_PATH", pidPath)
	t.Setenv("AI_TIMEOUT", "1s")
	t.Setenv(childAgentEnvName, "")

	result := runSubAgentCommand(context.Background(), "ignored", "parent.abc123")
	if result.OK || !strings.Contains(result.Error, "timed out") {
		t.Fatalf("expected timeout result, got %+v", result)
	}
	pidBytes, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatalf("read child pid: %v", err)
	}
	pid, err := strconv.Atoi(string(pidBytes))
	if err != nil {
		t.Fatalf("parse child pid: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for processIsRunning(pid) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if processIsRunning(pid) {
		t.Fatalf("child process %d survived process-group termination", pid)
	}
}

func processIsRunning(pid int) bool {
	if runtime.GOOS == "linux" {
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			return false
		}
		fields := strings.Fields(string(data))
		return len(fields) > 2 && fields[2] != "Z"
	}
	return syscall.Kill(pid, 0) == nil
}

func toolNames(tools []toolDefinition) map[string]struct{} {
	names := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		names[tool.Function.Name] = struct{}{}
	}
	return names
}
