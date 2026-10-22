// Copyright (c) 2026 Jonathan L. Conradt
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	t.Setenv("ASH_LOG_FILE", filepath.Join(home, ".local", "state", "ash", "logs", "scheduler.log"))
	t.Setenv("ASH_LOG_FORMAT", "json")
	t.Setenv("ASH_LOG_MAX_BYTES", "220")

	configureDebugLogging()
	if debugWriter == nil {
		t.Fatalf("expected debug writer to be configured")
	}

	slog.Debug("first ", "request_id", requestIDGenerator(), "value", strings.Repeat("a", 120), "EID", "IXk2kUYH")
	slog.Debug("second ", "request_id", requestIDGenerator(), "value", strings.Repeat("b", 120), "EID", "x1z9lqDJ")

	currentPath := filepath.Join(home, ".local", "state", "ash", "logs", "scheduler.log")
	rotatedPath := currentPath + ".1"

	currentData, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatalf("read current log file: %v", err)
	}
	if !strings.Contains(string(currentData), "second ") {
		t.Fatalf("expected current log file to contain second entry, got %q", string(currentData))
	}
	currentHeader, _, _ := strings.Cut(string(currentData), "\n")
	if !strings.Contains(currentHeader, `"message":"ash version: `) {
		t.Fatalf("expected current log file to start with version information, got %q", currentHeader)
	}

	rotatedData, err := os.ReadFile(rotatedPath)
	if err != nil {
		t.Fatalf("read rotated log file: %v", err)
	}
	if !strings.Contains(string(rotatedData), "first ") {
		t.Fatalf("expected rotated log file to contain first entry, got %q", string(rotatedData))
	}
	rotatedHeader, _, _ := strings.Cut(string(rotatedData), "\n")
	if !strings.Contains(rotatedHeader, `"message":"ash version: `) {
		t.Fatalf("expected rotated log file to start with version information, got %q", rotatedHeader)
	}
}

func TestRunPrintsVersionWithoutRuntimeConfiguration(t *testing.T) {
	origVersion := ashVersion
	origCommit := ashCommit
	origDevelopment := ashDevelopmentBuild
	t.Cleanup(func() {
		ashVersion = origVersion
		ashCommit = origCommit
		ashDevelopmentBuild = origDevelopment
	})
	ashVersion = "1.2.3"
	ashCommit = "abcdef1234567890"
	ashDevelopmentBuild = "true"

	for _, command := range []string{"version", "--version"} {
		t.Run(command, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run([]string{command}, &stdout, &stderr); code != 0 {
				t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
			}
			if got, want := strings.TrimSpace(stdout.String()), "v1.2.3 (dev:7890)"; got != want {
				t.Fatalf("version output = %q, want %q", got, want)
			}
			if stderr.Len() != 0 {
				t.Fatalf("expected no stderr output, got %q", stderr.String())
			}
		})
	}
}

func TestRunLogsSessionIDAndVersionAsFirstDebugEntry(t *testing.T) {
	origDebugWriter := debugWriter
	origVersion := ashVersion
	origCommit := ashCommit
	origDevelopment := ashDevelopmentBuild
	t.Cleanup(func() {
		debugWriter = origDebugWriter
		ashVersion = origVersion
		ashCommit = origCommit
		ashDevelopmentBuild = origDevelopment
	})

	t.Run("release build", func(t *testing.T) {
		ashVersion = "1.2.3"
		ashDevelopmentBuild = "false"
		gotVersion := runAndCaptureFirstDebugEntry(t)
		if gotVersion["version"] != "v1.2.3" {
			t.Fatalf("expected version v1.2.3, got %#v", gotVersion["version"])
		}
	})

	t.Run("developer build", func(t *testing.T) {
		ashVersion = "1.2.3"
		ashCommit = "abcdef1234567890"
		ashDevelopmentBuild = "true"
		gotVersion := runAndCaptureFirstDebugEntry(t)
		if gotVersion["version"] != "v1.2.3 (dev:7890)" {
			t.Fatalf("expected developer build version suffix, got %#v", gotVersion["version"])
		}
	})
}

func runAndCaptureFirstDebugEntry(t *testing.T) map[string]any {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ASH_SESSION_ID", "")
	t.Setenv("ASH_VERBOSE", "1")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"ok"}}`))
	}))
	defer srv.Close()
	t.Setenv("ASH_AI_ENDPOINT", srv.URL)
	t.Setenv("ASH_AI_MODEL", "llama3.1")

	var stdout bytes.Buffer
	var stderr syncBuffer
	if code := run([]string{"hello"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}

	lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
	if len(lines) == 0 {
		t.Fatalf("expected debug log output, got none")
	}

	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("expected first log line to be JSON, got %q: %v", lines[0], err)
	}
	if first["message"] != "ash session started" {
		t.Fatalf("expected first log entry to announce session start, got %#v", first)
	}

	sessionID, err := ensureSessionID()
	if err != nil {
		t.Fatalf("ensureSessionID error: %v", err)
	}
	if first["session_id"] != sessionID {
		t.Fatalf("expected session_id %q in first log entry, got %#v", sessionID, first["session_id"])
	}

	return first
}

func TestVerboseDebugLogsUseStructuredJSON(t *testing.T) {
	origWriter := debugWriter
	origJSON := debugJSONLogging
	t.Cleanup(func() {
		debugWriter = origWriter
		debugJSONLogging = origJSON
	})

	var buf bytes.Buffer
	debugWriter = &buf
	debugJSONLogging = true
	t.Setenv("ASH_VERBOSE", "1")

	configureDebugLogging()
	slog.Debug("structured debug output", "request_id", requestIDGenerator(), "EID", "LsSPp1Zz")

	output := strings.TrimSpace(buf.String())
	if output == "" {
		t.Fatal("expected debug output, got empty string")
	}

	var record map[string]any
	if err := json.Unmarshal([]byte(output), &record); err != nil {
		t.Fatalf("expected JSON debug payload, got %q: %v", output, err)
	}
	if record["level"] != "debug" {
		t.Fatalf("expected debug level in payload, got %#v", record["level"])
	}
	if record["message"] != "structured debug output" {
		t.Fatalf("expected structured message, got %#v", record["message"])
	}
	if _, ok := record["time"]; !ok {
		t.Fatalf("expected timestamp field in payload, got %#v", record)
	}
	if _, ok := record["request_id"]; !ok {
		t.Fatalf("expected request_id field in payload, got %#v", record)
	}
	if strings.Contains(output, "[EID=") {
		t.Fatalf("expected no EID markers in structured payload, got %q", output)
	}
}

func TestUserVisibleLogsUsePlainText(t *testing.T) {
	origWriter := debugWriter
	origJSON := debugJSONLogging
	t.Cleanup(func() {
		debugWriter = origWriter
		debugJSONLogging = origJSON
	})

	var buf bytes.Buffer
	t.Setenv("ASH_VERBOSE", "")
	configureDebugLogging(&buf)

	slog.Warn("AI took longer than 3m0s, so we should probably try again later", "EID", "80FzBwhZ")
	output := strings.TrimSpace(buf.String())
	if !strings.HasPrefix(output, "⚠️ warning: AI took longer than 3m0s") {
		t.Fatalf("expected plain-text warning with attention prefix, got %q", output)
	}
	if strings.Contains(output, `"time"`) || strings.Contains(output, `"EID"`) || strings.Contains(output, "80FzBwhZ") {
		t.Fatalf("expected user-visible message without structured log fields, got %q", output)
	}
}

func TestSchedulerLogFilePathRejectsInvalidSessionID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ASH_SESSION_ID", "abC-12_3.!@Z")

	if _, err := schedulerLogFilePath(true); err == nil {
		t.Fatal("expected invalid session ID to be rejected")
	}
}

func TestSchedulerLogFilePathRequiresSessionID(t *testing.T) {
	t.Setenv("ASH_SESSION_ID", "")
	if _, err := schedulerLogFilePath(true); err == nil {
		t.Fatalf("expected error when ASH_SESSION_ID is missing")
	} else if !strings.Contains(err.Error(), "ASH_SESSION_ID is required") {
		t.Fatalf("expected ASH_SESSION_ID requirement in error, got %q", err.Error())
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
