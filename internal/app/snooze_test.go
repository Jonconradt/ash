package app

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRunSnooze(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	originalTimeNow := timeNow
	t.Cleanup(func() { timeNow = originalTimeNow })
	fixedNow := time.Date(2026, time.August, 19, 12, 0, 0, 0, time.UTC)
	timeNow = func() time.Time { return fixedNow }

	t.Run("default duration", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		if code := runSnooze(nil, &stdout, &stderr); code != 0 {
			t.Fatalf("runSnooze returned %d, stderr=%q", code, stderr.String())
		}
		path, err := snoozeFilePath()
		if err != nil {
			t.Fatalf("snoozeFilePath returned error: %v", err)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read snooze state: %v", err)
		}
		want := strconv.FormatInt(fixedNow.Add(defaultSnoozeDuration).Unix(), 10)
		if strings.TrimSpace(string(content)) != want {
			t.Fatalf("snooze expiry = %q, want %q", strings.TrimSpace(string(content)), want)
		}
		if !snoozeActive() {
			t.Fatal("expected default snooze to be active")
		}
	})

	t.Run("custom duration replaces expiry", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		if code := runSnooze([]string{"30s"}, &stdout, &stderr); code != 0 {
			t.Fatalf("runSnooze returned %d, stderr=%q", code, stderr.String())
		}
		path, err := snoozeFilePath()
		if err != nil {
			t.Fatalf("snoozeFilePath returned error: %v", err)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read snooze state: %v", err)
		}
		want := strconv.FormatInt(fixedNow.Add(30*time.Second).Unix(), 10)
		if strings.TrimSpace(string(content)) != want {
			t.Fatalf("snooze expiry = %q, want %q", strings.TrimSpace(string(content)), want)
		}
	})

	t.Run("off clears state", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		if code := runSnooze([]string{"off"}, &stdout, &stderr); code != 0 {
			t.Fatalf("runSnooze returned %d, stderr=%q", code, stderr.String())
		}
		path, err := snoozeFilePath()
		if err != nil {
			t.Fatalf("snoozeFilePath returned error: %v", err)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("snooze state still exists or stat failed: %v", err)
		}
	})

	t.Run("invalid duration", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		if code := runSnooze([]string{"0s"}, &stdout, &stderr); code == 0 {
			t.Fatal("expected invalid duration to fail")
		}
	})
}

func TestSnoozeStateFailsOpen(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, ashWorkspaceDirName)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	path := filepath.Join(root, snoozeFileName)
	if err := os.WriteFile(path, []byte("not-a-timestamp"), 0o600); err != nil {
		t.Fatalf("write malformed state: %v", err)
	}
	if snoozeActive() {
		t.Fatal("malformed snooze state should fail open")
	}
	if err := os.WriteFile(path, []byte(strconv.FormatInt(time.Now().Add(-time.Minute).Unix(), 10)), 0o600); err != nil {
		t.Fatalf("write expired state: %v", err)
	}
	if snoozeActive() {
		t.Fatal("expired snooze state should be inactive")
	}
}

func TestSnoozeGuardsAreInstalled(t *testing.T) {
	for name, content := range map[string]string{
		"bash": bashInstallWrapperContent(),
		"fish": fishInstallWrapperContent(),
		"zsh":  zshInstallWrapperContent(),
	} {
		if !strings.Contains(content, ".ash_snooze_until") {
			t.Errorf("%s wrapper does not check snooze state", name)
		}
		if !strings.Contains(content, "_ash_prompt_processing_enabled") {
			t.Errorf("%s wrapper does not define snooze guard", name)
		}
	}
}
