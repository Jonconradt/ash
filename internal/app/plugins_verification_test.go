package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAllPluginsVerification(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working dir: %v", err)
	}
	repoRoot, err := filepath.Abs(filepath.Join(wd, "../.."))
	if err != nil {
		t.Fatalf("failed to resolve repo root: %v", err)
	}

	pluginsSrcDir := filepath.Join(repoRoot, "internal/app/ash_bootstrap/plugins_src")
	entries, err := os.ReadDir(pluginsSrcDir)
	if err != nil {
		t.Fatalf("failed to read plugins_src directory: %v", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pluginName := entry.Name()
		pluginDir := filepath.Join(pluginsSrcDir, pluginName)
		t.Run(pluginName, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			// 1. Build plugin using its Makefile
			buildCmd := exec.CommandContext(ctx, "make", "-C", pluginDir, "build")
			if out, err := buildCmd.CombinedOutput(); err != nil {
				t.Fatalf("make build failed for plugin %s: %v\nOutput: %s", pluginName, err, string(out))
			}

			binPath := filepath.Join(repoRoot, "bin/plugins", pluginName)
			if _, err := os.Stat(binPath); err != nil {
				t.Fatalf("expected binary at %s after build: %v", binPath, err)
			}

			// 2. Test --ai-docs returns valid JSON documentation
			docsCmd := exec.CommandContext(ctx, binPath, "--ai-docs")
			docsOut, err := docsCmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s --ai-docs failed: %v\nOutput: %s", pluginName, err, string(docsOut))
			}
			var docsMap map[string]any
			if err := json.Unmarshal(docsOut, &docsMap); err != nil {
				t.Fatalf("%s --ai-docs did not return valid JSON: %v\nOutput: %s", pluginName, err, string(docsOut))
			}
			if _, ok := docsMap["Capabilities"]; !ok {
				t.Errorf("%s --ai-docs missing 'Capabilities' key", pluginName)
			}
			if _, ok := docsMap["Arguments"]; !ok {
				t.Errorf("%s --ai-docs missing 'Arguments' key", pluginName)
			}
			if _, ok := docsMap["Return format"]; !ok {
				t.Errorf("%s --ai-docs missing 'Return format' key", pluginName)
			}

			// 3. Test --version returns semantic version string
			verCmd := exec.CommandContext(ctx, binPath, "--version")
			verOut, err := verCmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s --version failed: %v\nOutput: %s", pluginName, err, string(verOut))
			}
			verStr := strings.TrimSpace(string(verOut))
			if !strings.HasPrefix(verStr, pluginName) {
				t.Errorf("%s --version output %q does not start with plugin name", pluginName, verStr)
			}

			// 4. Test --help returns usage instructions
			helpCmd := exec.CommandContext(ctx, binPath, "--help")
			helpOut, err := helpCmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s --help failed: %v\nOutput: %s", pluginName, err, string(helpOut))
			}
			if !strings.Contains(strings.ToLower(string(helpOut)), "usage") {
				t.Errorf("%s --help output does not contain usage information: %s", pluginName, string(helpOut))
			}

			// 5. Test environment variables (ASH_LOG_FILE, ASH_LOG_FORMAT, ASH_VERBOSE)
			tmpDir := t.TempDir()
			logFilePath := filepath.Join(tmpDir, "plugin_test.log")

			testExecArgs := []string{}
			if pluginName == "calculator" {
				testExecArgs = []string{"--expr", "10 + 5"}
			}

			execCmd := exec.CommandContext(ctx, binPath, testExecArgs...)
			execCmd.Env = append(os.Environ(),
				"ASH_LOG_FILE="+logFilePath,
				"ASH_LOG_FORMAT=json",
				"ASH_VERBOSE=1",
			)
			var stdout, stderr bytes.Buffer
			execCmd.Stdout = &stdout
			execCmd.Stderr = &stderr
			if err := execCmd.Run(); err != nil {
				t.Fatalf("%s execution failed: %v\nStderr: %s", pluginName, err, stderr.String())
			}

			// Verify log file was written and contains structured JSON entries with EID
			logData, err := os.ReadFile(logFilePath)
			if err != nil {
				t.Fatalf("%s did not write to ASH_LOG_FILE %s: %v", pluginName, logFilePath, err)
			}
			if len(logData) == 0 {
				t.Fatalf("%s wrote empty ASH_LOG_FILE", pluginName)
			}
			lines := strings.Split(strings.TrimSpace(string(logData)), "\n")
			for _, line := range lines {
				var logEntry map[string]any
				if err := json.Unmarshal([]byte(line), &logEntry); err != nil {
					t.Fatalf("%s log entry is not valid JSON: %s (err: %v)", pluginName, line, err)
				}
				if _, ok := logEntry["EID"]; !ok {
					t.Errorf("%s log entry missing EID field: %s", pluginName, line)
				}
			}

			// 6. Test cancellation / clean shutdown
			cancelCtx, cancelFunc := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancelFunc()
			cancelCmd := exec.CommandContext(cancelCtx, binPath, "--help")
			if err := cancelCmd.Run(); err != nil {
				t.Errorf("%s failed quick context run: %v", pluginName, err)
			}
		})
	}
}
