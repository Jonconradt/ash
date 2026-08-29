package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const managedVenvDirName = "venv"

// provisionPythonEnv is indirected so tests can skip real virtualenv creation.
// Provisioning is synchronous so bundled tools are usable as soon as installation returns.
var provisionPythonEnv = provisionManagedPythonEnv

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
		if managedVenvReady(venvPython) {
			return venvPython
		}
	}
	return "python3"
}

// managedVenvReady avoids selecting a partially-created virtualenv. Python can leave
// bin/python3 behind when venv creation fails before ensurepip installs pip.
func managedVenvReady(venvPython string) bool {
	if info, err := os.Stat(venvPython); err != nil || info.IsDir() {
		return false
	}
	pipPath := filepath.Join(filepath.Dir(venvPython), "pip")
	if runtime.GOOS == "windows" {
		pipPath += ".exe"
	}
	info, err := os.Stat(pipPath)
	return err == nil && !info.IsDir()
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

	if !managedVenvReady(venvPython) {
		if removeErr := os.RemoveAll(venvDir); removeErr != nil {
			_, _ = fmt.Fprintf(stdout, "skipping python environment setup: remove incomplete environment: %v\n", removeErr)
			return
		}
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
