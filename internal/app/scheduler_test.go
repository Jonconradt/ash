package app

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

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
	t.Setenv("SESSION_ID", "session_ABC123")
	t.Setenv("HOME", "/Users/tester")
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("ASH_VERBOSE", "")

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
	if !strings.Contains(got, "ASH_LOG_FILE='") || !strings.Contains(got, "/.ash/logs/task_sessionABC123.log'") {
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

func TestBuildManagedAshEnvIncludesSessionIDLine(t *testing.T) {
	got := buildManagedAshEnv(map[string]string{
		"AI_ENDPOINT": "http://localhost:11434",
		"AI_MODEL":    "llama3.1",
	})

	wantSessionLine := "export SESSION_ID=`head -c 100 /dev/urandom | LC_ALL=C tr -dc 'a-zA-Z0-9' | fold -w 16 | head -n 1`\n"
	if !strings.Contains(got, wantSessionLine) {
		t.Fatalf("expected managed ash env to include SESSION_ID export line, got %q", got)
	}
	if !strings.Contains(got, "export AI_ENDPOINT='http://localhost:11434'\n") {
		t.Fatalf("expected managed ash env to include AI_ENDPOINT export, got %q", got)
	}
	if !strings.Contains(got, "export AI_MODEL='llama3.1'\n") {
		t.Fatalf("expected managed ash env to include AI_MODEL export, got %q", got)
	}
	if !strings.Contains(got, managedPathExportLine+"\n") {
		t.Fatalf("expected managed ash env to include tools PATH export, got %q", got)
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

func TestSchedulingHelpers(t *testing.T) {
	meta, line, err := buildRecurringJobLine("echo hi", "@daily", "/tmp", "test", "")
	if err != nil {
		t.Fatalf("buildRecurringJobLine error: %v", err)
	}
	if meta.ID == "" || !strings.Contains(line, jobMarkerPrefix) {
		t.Fatalf("unexpected recurring job line: %q", line)
	}

	payload, err := json.Marshal(recurringJobMetadata{ID: "job-1", Cron: "@daily", Prompt: "echo hi", Cwd: "/tmp", Env: map[string]string{"PATH": "/usr/bin"}, Purpose: "test", CreatedAt: time.Now().Format(time.RFC3339)})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	encoded := base64.RawStdEncoding.EncodeToString(payload)
	records, err := parseRecurringJobs("@daily /bin/sh # ash:job job-1 " + encoded)
	if err != nil {
		t.Fatalf("parseRecurringJobs error: %v", err)
	}
	if len(records) != 1 || records[0].Meta.ID != "job-1" {
		t.Fatalf("expected one recurring job record, got %#v", records)
	}
}
