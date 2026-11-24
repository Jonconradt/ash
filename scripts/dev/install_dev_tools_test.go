// Copyright (c) 2026 Jonathan L. Conradt
// SPDX-License-Identifier: MIT

package dev

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestConfigChecksNodeVersionBeforeInstalling(t *testing.T) {
	tests := []struct {
		version   string
		supported bool
	}{
		{"v18.19.1", false},
		{"v22.21.9", false},
		{"v22.22.0", true},
		{"v23.0.0", false},
		{"v24.7.0", false},
		{"v24.8.0", true},
		{"v25.0.0", true},
		{"invalid", false},
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "node"), []byte("#!/bin/sh\nprintf '%s\\n' '"+tt.version+"'\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(context.Background(), "bash", "-c", `source ./install_dev_tools.sh; node_supported`)
			cmd.Dir = "."
			cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			err := cmd.Run()
			if tt.supported && err != nil {
				t.Fatalf("Node %s should be supported: %v", tt.version, err)
			}
			if !tt.supported && err == nil {
				t.Fatalf("Node %s should be unsupported", tt.version)
			}
		})
	}
}
