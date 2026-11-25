// Copyright (c) 2026 Jonathan L. Conradt
// SPDX-License-Identifier: MIT

package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManagedNodeExecutablePrefersOverride(t *testing.T) {
	t.Setenv("ASH_NODE", "/opt/node/bin/node")
	originalLookPath := execLookPath
	t.Cleanup(func() { execLookPath = originalLookPath })
	execLookPath = func(name string) (string, error) {
		if name == "/opt/node/bin/node" {
			return name, nil
		}
		return "", os.ErrNotExist
	}

	got, err := managedNodeExecutable()
	if err != nil || got != "/opt/node/bin/node" {
		t.Fatalf("managedNodeExecutable() = %q, %v", got, err)
	}
}

func TestManagedNodeExecutableFindsSystemNode(t *testing.T) {
	t.Setenv("ASH_NODE", "")
	originalLookPath := execLookPath
	t.Cleanup(func() { execLookPath = originalLookPath })
	execLookPath = func(name string) (string, error) {
		if name == "node" {
			return "/usr/bin/node", nil
		}
		return "", os.ErrNotExist
	}

	got, err := managedNodeExecutable()
	if err != nil || got != "/usr/bin/node" {
		t.Fatalf("managedNodeExecutable() = %q, %v", got, err)
	}
}

func TestManagedNodeExecutableFindsNvmInstallation(t *testing.T) {
	t.Setenv("ASH_NODE", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NVM_DIR", filepath.Join(home, ".nvm"))
	originalHome := osUserHomeDir
	osUserHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { osUserHomeDir = originalHome })
	originalLookPath := execLookPath
	t.Cleanup(func() { execLookPath = originalLookPath })
	execLookPath = func(string) (string, error) { return "", os.ErrNotExist }

	nodePath := filepath.Join(home, ".nvm", "versions", "node", "v24.0.0", "bin", "node")
	if err := os.MkdirAll(filepath.Dir(nodePath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(nodePath, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatalf("write node: %v", err)
	}

	got, err := managedNodeExecutable()
	if err != nil || got != nodePath {
		t.Fatalf("managedNodeExecutable() = %q, %v", got, err)
	}
}
