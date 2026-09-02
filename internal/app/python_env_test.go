package app

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAshPythonInterpreterPrefersOverrideThenVenv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ASH_PYTHON", "")

	if got := ashPythonInterpreter(); got != "python3" {
		t.Fatalf("expected system python3 with no venv, got %q", got)
	}

	venvPython, err := managedVenvPython()
	if err != nil {
		t.Fatalf("managedVenvPython: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(venvPython), 0o700); err != nil {
		t.Fatalf("mkdir venv bin: %v", err)
	}
	if err := os.WriteFile(venvPython, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatalf("write venv python: %v", err)
	}
	pipPath := filepath.Join(filepath.Dir(venvPython), "pip")
	if err := os.WriteFile(pipPath, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatalf("write venv pip: %v", err)
	}
	if got := ashPythonInterpreter(); got != venvPython {
		t.Fatalf("expected managed venv interpreter %q, got %q", venvPython, got)
	}

	t.Setenv("ASH_PYTHON", "/custom/python3")
	if got := ashPythonInterpreter(); got != "/custom/python3" {
		t.Fatalf("expected ASH_PYTHON override, got %q", got)
	}
}

func TestAshPythonInterpreterSkipsIncompleteVenv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ASH_PYTHON", "")

	venvPython, err := managedVenvPython()
	if err != nil {
		t.Fatalf("managedVenvPython: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(venvPython), 0o700); err != nil {
		t.Fatalf("mkdir venv bin: %v", err)
	}
	if err := os.WriteFile(venvPython, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatalf("write incomplete venv python: %v", err)
	}

	if got := ashPythonInterpreter(); got != "python3" {
		t.Fatalf("expected system python3 for incomplete venv, got %q", got)
	}
}

func TestPythonExecutionAvailable(t *testing.T) {
	originalLookPath := execLookPath
	t.Cleanup(func() { execLookPath = originalLookPath })

	tests := []struct {
		name      string
		strict    string
		available bool
		want      bool
	}{
		{name: "available", available: true, want: true},
		{name: "interpreter unavailable", want: false},
		{name: "strict mode", strict: "true", available: true, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("ASH_STRICT", test.strict)
			t.Setenv("ASH_PYTHON", "python3")
			execLookPath = func(file string) (string, error) {
				if file != "python3" {
					t.Fatalf("unexpected interpreter lookup: %q", file)
				}
				if test.available {
					return "/usr/bin/python3", nil
				}
				return "", os.ErrNotExist
			}
			if got := pythonExecutionAvailable(); got != test.want {
				t.Fatalf("pythonExecutionAvailable() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestManagedPythonScriptResolvesOnlyInstalledPyTools(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	toolsDir := filepath.Join(home, ".ash", "tools")
	if err := os.MkdirAll(toolsDir, 0o700); err != nil {
		t.Fatalf("mkdir tools: %v", err)
	}
	scriptPath := filepath.Join(toolsDir, "yfinance.py")
	if err := os.WriteFile(scriptPath, []byte("#!/usr/bin/env python3\n"), 0o700); err != nil {
		t.Fatalf("write tool: %v", err)
	}

	got, ok := managedPythonScript("yfinance.py")
	if !ok || got != scriptPath {
		t.Fatalf("expected %q, got %q ok=%v", scriptPath, got, ok)
	}
	if _, ok := managedPythonScript("curl"); ok {
		t.Fatal("non-python tool must not resolve to a managed script")
	}
	if _, ok := managedPythonScript("missing.py"); ok {
		t.Fatal("uninstalled python tool must not resolve")
	}
}

// hardenAshWorkspacePermissions previously chmod'd every regular file to 0600,
// which silently made the bundled tool scripts non-executable.
func TestHardenAshWorkspacePermissionsPreservesExecutableBit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	toolsDir := filepath.Join(home, ".ash", "tools")
	if err := os.MkdirAll(toolsDir, 0o700); err != nil {
		t.Fatalf("mkdir tools: %v", err)
	}
	script := filepath.Join(toolsDir, "wikipedia.py")
	if err := os.WriteFile(script, []byte("#!/usr/bin/env python3\n"), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	plain := filepath.Join(home, ".ash", ".ash_system")
	if err := os.WriteFile(plain, []byte("system prompt\n"), 0o600); err != nil {
		t.Fatalf("write plain file: %v", err)
	}

	if err := hardenAshWorkspacePermissions(); err != nil {
		t.Fatalf("hardenAshWorkspacePermissions: %v", err)
	}

	scriptInfo, err := os.Stat(script)
	if err != nil {
		t.Fatalf("stat script: %v", err)
	}
	if scriptInfo.Mode().Perm() != fs.FileMode(0o700) {
		t.Fatalf("expected tool script to stay 0700, got %o", scriptInfo.Mode().Perm())
	}

	plainInfo, err := os.Stat(plain)
	if err != nil {
		t.Fatalf("stat plain file: %v", err)
	}
	if plainInfo.Mode().Perm() != fs.FileMode(0o600) {
		t.Fatalf("expected non-script to be 0600, got %o", plainInfo.Mode().Perm())
	}
}

func TestRequirementsTxtIsNotInstalledAsTool(t *testing.T) {
	if _, err := readEmbeddedBootstrapAsset("ash_bootstrap/tools/requirements.txt"); err != nil {
		t.Fatalf("requirements.txt must be embedded for venv provisioning: %v", err)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	var out strings.Builder
	if err := installEmbeddedBootstrapAssets(true, "", &out); err != nil {
		t.Fatalf("installEmbeddedBootstrapAssets: %v", err)
	}

	if _, err := os.Stat(filepath.Join(home, ".ash", "tools", "requirements.txt")); !os.IsNotExist(err) {
		t.Fatal("requirements.txt must not be installed into ~/.ash/tools")
	}
	for _, want := range []string{"yfinance.py", "wikipedia.py", "headlines.py"} {
		info, err := os.Stat(filepath.Join(home, ".ash", "tools", want))
		if err != nil {
			t.Fatalf("expected %s to be installed: %v", want, err)
		}
		if info.Mode().Perm()&0o100 == 0 {
			t.Errorf("expected %s to be executable, got %o", want, info.Mode().Perm())
		}
	}
}
