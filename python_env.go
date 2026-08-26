package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const managedVenvDirName = "venv"

const provisionLogName = "python-env.log"

// provisionPythonEnv is indirected so tests can skip real virtualenv creation.
var provisionPythonEnv = startBackgroundPythonProvision

// startBackgroundPythonProvision detaches the virtualenv build so `ash install` returns
// immediately; pip pulling pandas/numpy would otherwise block the shell for minutes.
func startBackgroundPythonProvision(stdout io.Writer) {
	root, err := ashWorkspaceDir()
	if err != nil {
		provisionManagedPythonEnv(stdout)
		return
	}
	exe, err := osExecutable()
	if err != nil {
		provisionManagedPythonEnv(stdout)
		return
	}
	logPath := filepath.Join(root, provisionLogName)
	// #nosec G304 -- logPath is a fixed name inside the ash-owned workspace.
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		provisionManagedPythonEnv(stdout)
		return
	}
	defer func() { _ = logFile.Close() }()

	// context.Background is intentional: the child must outlive this process.
	cmd := exec.CommandContext(context.Background(), exe, "--internal-provision-python")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	detachProcess(cmd)
	if startErr := cmd.Start(); startErr != nil {
		provisionManagedPythonEnv(stdout)
		return
	}
	_ = cmd.Process.Release()
	_, _ = fmt.Fprintf(stdout, "preparing python environment for bundled tools in the background (log: %s)\n", logPath)
}

// managedVenvPython returns the interpreter path inside the ash-managed virtualenv.
func managedVenvPython() (string, error) {
	root, err := ashWorkspaceDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(root, managedVenvDirName, "Scripts", "python.exe"), nil
	}
	return filepath.Join(root, managedVenvDirName, "bin", "python3"), nil
}

// ashPythonInterpreter resolves the interpreter used for bundled Python tooling,
// preferring an explicit ASH_PYTHON override, then the managed venv, then system python3.
func ashPythonInterpreter() string {
	if override := strings.TrimSpace(os.Getenv("ASH_PYTHON")); override != "" {
		return override
	}
	venvPython, err := managedVenvPython()
	if err == nil {
		if info, statErr := os.Stat(venvPython); statErr == nil && !info.IsDir() {
			return venvPython
		}
	}
	return "python3"
}

// managedPythonScript resolves a bundled tool name to its installed script path.
func managedPythonScript(name string) (string, bool) {
	if !strings.HasSuffix(name, ".py") {
		return "", false
	}
	root, err := ashWorkspaceDir()
	if err != nil {
		return "", false
	}
	scriptPath := filepath.Join(root, "tools", name)
	info, err := os.Stat(scriptPath)
	if err != nil || info.IsDir() {
		return "", false
	}
	return scriptPath, true
}

// provisionManagedPythonEnv creates the managed virtualenv and installs bundled tool
// dependencies. Failures are reported but never abort the install: a user without
// python3, without the venv module, or without network access must still get a working shell.
func provisionManagedPythonEnv(stdout io.Writer) {
	requirements, err := readEmbeddedBootstrapAsset("ash_bootstrap/tools/requirements.txt")
	if err != nil || strings.TrimSpace(string(requirements)) == "" {
		return
	}

	root, err := ashWorkspaceDir()
	if err != nil {
		_, _ = fmt.Fprintf(stdout, "skipping python environment setup: %v\n", err)
		return
	}
	venvDir := filepath.Join(root, managedVenvDirName)
	venvPython, err := managedVenvPython()
	if err != nil {
		_, _ = fmt.Fprintf(stdout, "skipping python environment setup: %v\n", err)
		return
	}

	if _, statErr := os.Stat(venvPython); statErr != nil {
		if runErr := runProvisionCommand(stdout, "python3", "-m", "venv", venvDir); runErr != nil {
			_, _ = fmt.Fprintf(stdout, "skipping python environment setup: %v\n", runErr)
			_, _ = fmt.Fprintln(stdout, "install python3 and the venv module, then rerun 'ash install', to enable bundled python tools")
			return
		}
	}

	reqPath := filepath.Join(venvDir, "requirements.txt")
	if writeErr := os.WriteFile(reqPath, requirements, 0o600); writeErr != nil {
		_, _ = fmt.Fprintf(stdout, "skipping python dependency install: %v\n", writeErr)
		return
	}

	if runErr := runProvisionCommand(stdout, venvPython, "-m", "pip", "install", "--disable-pip-version-check", "--quiet", "-r", reqPath); runErr != nil {
		_, _ = fmt.Fprintf(stdout, "python dependency install failed: %v\n", runErr)
		_, _ = fmt.Fprintln(stdout, "bundled python tools that need third-party packages will report a missing-library error until this succeeds")
		return
	}

	_, _ = fmt.Fprintf(stdout, "python environment ready at %s\n", venvDir)
}

func runProvisionCommand(stdout io.Writer, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), provisionTimeout())
	defer cancel()
	cmd := execCommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(output))
		if trimmed != "" {
			return fmt.Errorf("%s: %w: %s", name, err, firstLines(trimmed, 3))
		}
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func provisionTimeout() time.Duration {
	return 10 * time.Minute
}

func firstLines(text string, limit int) string {
	lines := strings.Split(text, "\n")
	if len(lines) > limit {
		lines = lines[:limit]
	}
	return strings.Join(lines, "; ")
}
