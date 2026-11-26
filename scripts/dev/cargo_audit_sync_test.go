package dev

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCargoAuditSyncFallsBackToGit(t *testing.T) {
	script, err := filepath.Abs("cargo-audit-sync.sh")
	if err != nil {
		t.Fatal(err)
	}

	tempDir := t.TempDir()
	dbDir := filepath.Join(tempDir, "advisory-db")
	cacheDir := filepath.Join(tempDir, "cache")
	binDir := filepath.Join(tempDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cargo := filepath.Join(binDir, "cargo")
	if err := os.WriteFile(cargo, []byte("#!/bin/sh\nif [ \"$1\" = \"--help\" ]; then echo 'Usage: cargo audit [OPTIONS]'; exit 0; fi\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	git := filepath.Join(binDir, "git")
	if err := os.WriteFile(git, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, script)
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"CARGO_ADVISORY_DB="+dbDir,
		"CARGO_ADVISORY_DB_URL=https://example.invalid/advisory-db.git",
		"XDG_CACHE_HOME="+cacheDir,
		"CARGO_AUDIT_FORCE_SYNC=1",
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cargo-audit-sync.sh failed: %v\n%s", err, output)
	}
}
