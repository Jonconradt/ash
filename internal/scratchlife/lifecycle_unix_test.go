//go:build darwin || freebsd || linux

package scratchlife

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCleanupProtectsActiveAndRemovesUnlockedStaleSessions(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	lock, err := Acquire(root, "active-session")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	activeDir := filepath.Join(root, "active-session")
	if err := os.WriteFile(filepath.Join(activeDir, "note.txt"), []byte("active"), 0o600); err != nil {
		t.Fatal(err)
	}
	setOld(t, activeDir, now)

	staleDir := filepath.Join(root, "stale-session")
	if err := os.MkdirAll(staleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staleDir, "note.txt"), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	setOld(t, staleDir, now)

	deleted, err := Cleanup(root, CleanupOptions{MaxAge: 48 * time.Hour, IdleAge: 24 * time.Hour, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 1 || deleted[0] != staleDir {
		t.Fatalf("deleted = %v, want only %q", deleted, staleDir)
	}
	if _, err := os.Stat(filepath.Join(activeDir, "note.txt")); err != nil {
		t.Fatalf("active session was removed: %v", err)
	}
}

func TestLockFileRemovedWhenLastHolderExits(t *testing.T) {
	root := t.TempDir()
	first, err := Acquire(root, "shared-session")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Acquire(root, "shared-session")
	if err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(root, "shared-session", lockFileName)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("lock removed while another holder is active: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("last-holder lock file still exists: %v", err)
	}
}

func TestCleanupRecoversUnlockedStaleLockFile(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "crashed-session")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(sessionDir, lockFileName)
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	setOld(t, sessionDir, now)

	deleted, err := Cleanup(root, CleanupOptions{MaxAge: 48 * time.Hour, IdleAge: 24 * time.Hour, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 1 || deleted[0] != sessionDir {
		t.Fatalf("deleted = %v, want %q", deleted, sessionDir)
	}
}

func TestCleanupPreservesRecentUnlockedSession(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "recent-session")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	recent := now.Add(-2 * time.Hour)
	if err := os.Chtimes(sessionDir, recent, recent); err != nil {
		t.Fatal(err)
	}
	deleted, err := Cleanup(root, CleanupOptions{MaxAge: 48 * time.Hour, IdleAge: 24 * time.Hour, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 0 {
		t.Fatalf("recent session unexpectedly deleted: %v", deleted)
	}
}

func TestValidateSessionIDRejectsTraversal(t *testing.T) {
	for _, sessionID := range []string{"", ".", "..", "../escape", "a/b", strings.Repeat("a", maxSessionID+1)} {
		if err := ValidateSessionID(sessionID); err == nil {
			t.Errorf("ValidateSessionID(%q) succeeded", sessionID)
		}
	}
}

func TestCrossProcessHolderProtectsThenReleasesSession(t *testing.T) {
	root := t.TempDir()
	sessionID := "cross-process"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestScratchLockSubprocessHelper$")
	cmd.Env = append(os.Environ(), "ASH_SCRATCH_LOCK_HELPER=1", "ASH_SCRATCH_LOCK_ROOT="+root, "ASH_SCRATCH_LOCK_SESSION="+sessionID)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if stdin != nil {
			if err := stdin.Close(); err != nil {
				t.Errorf("close subprocess stdin: %v", err)
			}
		}
	}()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(stdout)
	line, err := reader.ReadString('\n')
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("child failed to acquire lock: %v", err)
	}
	if strings.TrimSpace(line) != "locked" {
		t.Fatalf("child status = %q", line)
	}
	sessionDir := filepath.Join(root, sessionID)
	setOld(t, sessionDir, time.Now())
	deleted, err := Cleanup(root, CleanupOptions{MaxAge: 48 * time.Hour, IdleAge: 24 * time.Hour, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 0 {
		t.Fatalf("cleanup removed a session locked in another process: %v", deleted)
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	stdin = nil
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, lockFileName)); !os.IsNotExist(err) {
		t.Fatalf("child's last-holder lock file remains after orderly exit: %v", err)
	}
}

func TestCleanupRecoversAfterHolderIsKilled(t *testing.T) {
	root := t.TempDir()
	sessionID := "killed-process"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestScratchLockSubprocessHelper$")
	cmd.Env = append(os.Environ(), "ASH_SCRATCH_LOCK_HELPER=1", "ASH_SCRATCH_LOCK_ROOT="+root, "ASH_SCRATCH_LOCK_SESSION="+sessionID)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if stdin != nil {
			if err := stdin.Close(); err != nil {
				t.Errorf("close subprocess stdin: %v", err)
			}
		}
	}()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "locked" {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("child failed to acquire lock: line=%q err=%v", line, err)
	}
	sessionDir := filepath.Join(root, sessionID)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	stdin = nil
	lockPath := filepath.Join(sessionDir, lockFileName)
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("abrupt process exit should leave a recoverable lock file: %v", err)
	}
	now := time.Now()
	setOld(t, sessionDir, now)
	deleted, err := Cleanup(root, CleanupOptions{MaxAge: 48 * time.Hour, IdleAge: 24 * time.Hour, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 1 || deleted[0] != sessionDir {
		t.Fatalf("cleanup failed to recover killed session lock: %v", deleted)
	}
}

func TestScratchLockSubprocessHelper(t *testing.T) {
	if os.Getenv("ASH_SCRATCH_LOCK_HELPER") != "1" {
		return
	}
	lock, err := Acquire(os.Getenv("ASH_SCRATCH_LOCK_ROOT"), os.Getenv("ASH_SCRATCH_LOCK_SESSION"))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = os.Stdout.WriteString("locked\n")
	_, _ = os.Stdin.Read(make([]byte, 1))
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
}

func setOld(t *testing.T, dir string, now time.Time) {
	t.Helper()
	old := now.Add(-72 * time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
}
