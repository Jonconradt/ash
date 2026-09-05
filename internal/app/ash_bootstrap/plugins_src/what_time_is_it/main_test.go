package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ash/internal/plugin"
)

func TestAIDocsSchema(t *testing.T) {
	p := &timePlugin{}
	docsStr := p.AIDocs()
	var docs map[string]any
	if err := json.Unmarshal([]byte(docsStr), &docs); err != nil {
		t.Fatalf("AIDocs() is not valid JSON: %v", err)
	}

	for _, key := range []string{"Capabilities", "Arguments", "Return format", "Usage guidance for the AI"} {
		if _, ok := docs[key]; !ok {
			t.Errorf("missing expected key %q in AIDocs", key)
		}
	}
}

func TestPluginMetadata(t *testing.T) {
	p := &timePlugin{}
	if p.Name() != "what_time_is_it" {
		t.Errorf("expected name what_time_is_it, got %q", p.Name())
	}
	if p.Version() != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %q", p.Version())
	}
}

func TestPluginRunFormats(t *testing.T) {
	p := &timePlugin{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	tests := []struct {
		format string
		verify func(t *testing.T, out string)
	}{
		{
			format: "json",
			verify: func(t *testing.T, out string) {
				var res map[string]any
				if err := json.Unmarshal([]byte(out), &res); err != nil {
					t.Fatalf("json format output is not valid JSON: %v\nOutput: %s", err, out)
				}
				if res["status"] != "success" {
					t.Errorf("expected status success, got %v", res["status"])
				}
				if _, ok := res["local_time"]; !ok {
					t.Errorf("missing local_time in JSON output")
				}
				if _, ok := res["local_datetime"]; !ok {
					t.Errorf("missing local_datetime in JSON output")
				}
				if _, ok := res["iso8601"]; !ok {
					t.Errorf("missing iso8601 in JSON output")
				}
				if _, ok := res["unix_timestamp"]; !ok {
					t.Errorf("missing unix_timestamp in JSON output")
				}
			},
		},
		{
			format: "rfc3339",
			verify: func(t *testing.T, out string) {
				trimmed := strings.TrimSpace(out)
				if _, err := time.Parse(time.RFC3339, trimmed); err != nil {
					t.Errorf("rfc3339 format %q is not valid RFC3339: %v", trimmed, err)
				}
			},
		},
		{
			format: "unix",
			verify: func(t *testing.T, out string) {
				trimmed := strings.TrimSpace(out)
				if len(trimmed) < 10 {
					t.Errorf("unix timestamp %q is too short", trimmed)
				}
			},
		},
		{
			format: "utc",
			verify: func(t *testing.T, out string) {
				trimmed := strings.TrimSpace(out)
				if !strings.HasSuffix(trimmed, "Z") {
					t.Errorf("utc format %q does not end with Z", trimmed)
				}
			},
		},
		{
			format: "human",
			verify: func(t *testing.T, out string) {
				trimmed := strings.TrimSpace(out)
				if len(trimmed) < 15 {
					t.Errorf("human format %q is too short", trimmed)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run("format_"+tc.format, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := p.Run(context.Background(), []string{"--format", tc.format}, &stdout, &stderr, logger)
			if code != 0 {
				t.Fatalf("Run failed with code %d, stderr: %s", code, stderr.String())
			}
			tc.verify(t, stdout.String())
		})
	}
}

func TestPluginTimezoneHandling(t *testing.T) {
	p := &timePlugin{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("valid timezone UTC", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := p.Run(context.Background(), []string{"--timezone", "UTC", "--format", "json"}, &stdout, &stderr, logger)
		if code != 0 {
			t.Fatalf("expected code 0, got %d", code)
		}
		var res map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if res["timezone"] != "UTC" {
			t.Errorf("expected timezone UTC, got %v", res["timezone"])
		}
	})

	t.Run("valid timezone America/New_York", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := p.Run(context.Background(), []string{"--timezone", "America/New_York", "--format", "json"}, &stdout, &stderr, logger)
		if code != 0 {
			t.Fatalf("expected code 0, got %d", code)
		}
		var res map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		tz := res["timezone"].(string)
		if tz != "EST" && tz != "EDT" {
			t.Errorf("expected EST or EDT timezone, got %v", tz)
		}
	})

	t.Run("invalid timezone", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := p.Run(context.Background(), []string{"--timezone", "Invalid/Nonexistent_Timezone"}, &stdout, &stderr, logger)
		if code != 1 {
			t.Fatalf("expected error code 1 for invalid timezone, got %d", code)
		}
		var res map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
			t.Fatalf("expected JSON error response, got %q: %v", stdout.String(), err)
		}
		if res["status"] != "error" {
			t.Errorf("expected status=error, got %v", res["status"])
		}
	})
}

func TestPluginCancellation(t *testing.T) {
	p := &timePlugin{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	var stdout, stderr bytes.Buffer
	code := p.Run(ctx, []string{}, &stdout, &stderr, logger)
	if code != 130 {
		t.Fatalf("expected exit code 130 on cancellation, got %d", code)
	}
}

func TestPluginLoggingIntegration(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "what_time_is_it_test.log")

	t.Setenv("ASH_LOG_FILE", logPath)
	t.Setenv("ASH_LOG_FORMAT", "json")
	t.Setenv("ASH_VERBOSE", "1")

	p := &timePlugin{}
	var stdout, stderr bytes.Buffer

	code := plugin.Run(p, []string{"--format", "json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("plugin.Run failed with code %d, stderr: %s", code, stderr.String())
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file error: %v", err)
	}
	if len(data) == 0 {
		t.Fatalf("log file is empty")
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for _, line := range lines {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log entry is not JSON: %s (err: %v)", line, err)
		}
		if _, ok := entry["EID"]; !ok {
			t.Errorf("missing EID in log entry: %s", line)
		}
		if entry["plugin"] != "what_time_is_it" {
			t.Errorf("expected plugin field 'what_time_is_it', got %v", entry["plugin"])
		}
	}
}
