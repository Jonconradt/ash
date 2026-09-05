package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type dummyPlugin struct {
	runFunc func(ctx context.Context, args []string, stdout, stderr io.Writer, logger *slog.Logger) int
}

func (d *dummyPlugin) Name() string    { return "dummy" }
func (d *dummyPlugin) Version() string { return "0.1.0" }
func (d *dummyPlugin) AIDocs() string  { return `{"Capabilities": ["test"]}` }
func (d *dummyPlugin) Run(ctx context.Context, args []string, stdout, stderr io.Writer, logger *slog.Logger) int {
	if d.runFunc != nil {
		return d.runFunc(ctx, args, stdout, stderr, logger)
	}
	return 0
}

func TestPluginRunStandardFlags(t *testing.T) {
	p := &dummyPlugin{}

	t.Run("flag --ai-docs", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := Run(p, []string{"--ai-docs"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected code 0, got %d", code)
		}
		if !strings.Contains(stdout.String(), "Capabilities") {
			t.Errorf("expected docs in stdout, got %q", stdout.String())
		}
	})

	t.Run("flag --version", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := Run(p, []string{"--version"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected code 0, got %d", code)
		}
		if strings.TrimSpace(stdout.String()) != "dummy 0.1.0" {
			t.Errorf("unexpected version output %q", stdout.String())
		}
	})

	t.Run("flag --help", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := Run(p, []string{"--help"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected code 0, got %d", code)
		}
		if !strings.Contains(strings.ToLower(stdout.String()), "usage") {
			t.Errorf("expected usage in help output, got %q", stdout.String())
		}
	})
}

func TestPluginAdversarialAndMalformedInputs(t *testing.T) {
	p := &dummyPlugin{
		runFunc: func(ctx context.Context, args []string, stdout, stderr io.Writer, logger *slog.Logger) int {
			for _, arg := range args {
				// Assert plugin receives raw arguments safely without crash
				logger.Info("received arg", "len", len(arg), "EID", "tPlg01a")
			}
			return 0
		},
	}

	malformedInputs := [][]string{
		{"\x00\x00\x00", "null_bytes"},
		{"../../../../etc/passwd", "../../../.ssh/id_rsa"},
		{strings.Repeat("A", 100000)}, // 100KB large input string
		{"$(rm -rf /)", "; ls -la;", "| cat", "&& echo 1", "`whoami`"},
		{"\n\r\t", "   ", ""},
		{"{\"malformed\": json", "<xml><unclosed>", "%s%s%s%s%n"},
		{"\uFFFD\u0000\uFFFF"}, // Edge case unicode
	}

	for i, args := range malformedInputs {
		t.Run(fmt.Sprintf("malformed_case_%d", i), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run(p, args, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("expected graceful 0 exit code on malformed args, got %d", code)
			}
		})
	}
}

func TestSetupLoggerConfigurations(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("invalid or unwritable path gracefully falls back", func(t *testing.T) {
		t.Setenv("ASH_LOG_FILE", "/dev/null/impossible_dir/test.log")
		logger, cleanup := SetupLogger("test_fallback")
		defer cleanup()
		if logger == nil {
			t.Fatal("logger should not be nil")
		}
		logger.Info("test log message", "EID", "tPlg02b")
	})

	t.Run("valid log file and format json", func(t *testing.T) {
		logPath := filepath.Join(tmpDir, "valid_test.log")
		t.Setenv("ASH_LOG_FILE", logPath)
		t.Setenv("ASH_LOG_FORMAT", "json")
		t.Setenv("ASH_VERBOSE", "debug")

		logger, cleanup := SetupLogger("test_json")
		logger.Debug("debug message", "key", "value", "EID", "tPlg03c")
		cleanup()

		data, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatalf("failed to read log file: %v", err)
		}
		var entry map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(data), &entry); err != nil {
			t.Fatalf("log entry is not valid JSON: %s (err: %v)", string(data), err)
		}
		if entry["plugin"] != "test_json" {
			t.Errorf("expected plugin field 'test_json', got %v", entry["plugin"])
		}
		if entry["key"] != "value" {
			t.Errorf("expected key='value', got %v", entry["key"])
		}
	})

	t.Run("text format logging", func(t *testing.T) {
		logPath := filepath.Join(tmpDir, "text_test.log")
		t.Setenv("ASH_LOG_FILE", logPath)
		t.Setenv("ASH_LOG_FORMAT", "text")
		t.Setenv("ASH_VERBOSE", "0")

		logger, cleanup := SetupLogger("test_text")
		logger.Info("info message in text format", "EID", "tPlg04d")
		cleanup()

		data, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatalf("failed to read text log file: %v", err)
		}
		if !strings.Contains(string(data), "info message in text format") {
			t.Errorf("expected log message in text output, got %s", string(data))
		}
	})
}

func TestPluginContextCancellation(t *testing.T) {
	p := &dummyPlugin{
		runFunc: func(ctx context.Context, args []string, stdout, stderr io.Writer, logger *slog.Logger) int {
			select {
			case <-ctx.Done():
				return 130
			case <-time.After(100 * time.Millisecond):
				return 0
			}
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Pre-cancel

	var stdout, stderr bytes.Buffer
	code := p.Run(ctx, []string{}, &stdout, &stderr, slog.Default())
	if code != 130 {
		t.Fatalf("expected code 130 on cancelled context, got %d", code)
	}
}

func TestPluginRunErrorPropagation(t *testing.T) {
	p := &dummyPlugin{
		runFunc: func(ctx context.Context, args []string, stdout, stderr io.Writer, logger *slog.Logger) int {
			_, _ = io.WriteString(stderr, "simulated fatal error")
			return 2
		},
	}

	var stdout, stderr bytes.Buffer
	code := Run(p, []string{"--execute-failure"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected return code 2, got %d", code)
	}
	if !strings.Contains(stderr.String(), "simulated fatal error") {
		t.Errorf("expected stderr message, got %q", stderr.String())
	}
}
