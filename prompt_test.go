package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadSystemPrompt(t *testing.T) {
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalCwd)
	})

	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}

	homePrompt := "home prompt"
	if err := os.WriteFile(filepath.Join(home, systemFileName), []byte(homePrompt), 0o600); err != nil {
		t.Fatalf("write home prompt: %v", err)
	}

	prompt, err := readSystemPrompt()
	if err != nil {
		t.Fatalf("readSystemPrompt error: %v", err)
	}
	if prompt != homePrompt {
		t.Fatalf("expected home prompt, got %q", prompt)
	}

	cwdPrompt := "cwd prompt"
	if err := os.WriteFile(filepath.Join(cwd, systemFileName), []byte(cwdPrompt), 0o600); err != nil {
		t.Fatalf("write cwd prompt: %v", err)
	}

	prompt, err = readSystemPrompt()
	if err != nil {
		t.Fatalf("readSystemPrompt error: %v", err)
	}
	if prompt != cwdPrompt {
		t.Fatalf("expected cwd prompt, got %q", prompt)
	}

	canonicalPrompt := "canonical prompt"
	if err := os.MkdirAll(filepath.Join(home, ashWorkspaceDirName), 0o700); err != nil {
		t.Fatalf("mkdir canonical workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ashWorkspaceDirName, systemFileName), []byte(canonicalPrompt), 0o600); err != nil {
		t.Fatalf("write canonical prompt: %v", err)
	}

	prompt, err = readSystemPrompt()
	if err != nil {
		t.Fatalf("readSystemPrompt error: %v", err)
	}
	if prompt != canonicalPrompt {
		t.Fatalf("expected canonical prompt, got %q", prompt)
	}
}

func TestReadSystemPromptExpandsEnvironmentVariables(t *testing.T) {
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalCwd)
	})

	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ASH_TEST_ONE", "first")
	t.Setenv("ASH_TEST_TWO", "second")

	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}

	content := "one=$ASH_TEST_ONE two=${ASH_TEST_TWO} missing=$ASH_TEST_MISSING"
	if err := os.WriteFile(filepath.Join(cwd, systemFileName), []byte(content), 0o600); err != nil {
		t.Fatalf("write cwd prompt: %v", err)
	}

	prompt, err := readSystemPrompt()
	if err != nil {
		t.Fatalf("readSystemPrompt error: %v", err)
	}

	want := "one=first two=second missing="
	if prompt != want {
		t.Fatalf("expanded prompt mismatch: got %q want %q", prompt, want)
	}
}

func TestReadSystemPromptExpandsUname(t *testing.T) {
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalCwd)
	})

	origLookPath := execLookPath
	origCommandOutput := execCommandOutput
	t.Cleanup(func() {
		execLookPath = origLookPath
		execCommandOutput = origCommandOutput
	})

	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("UNAME", "env-uname")

	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}

	if err := os.WriteFile(filepath.Join(cwd, systemFileName), []byte("host=$UNAME"), 0o600); err != nil {
		t.Fatalf("write cwd prompt: %v", err)
	}

	execLookPath = func(file string) (string, error) {
		if file != "uname" {
			t.Fatalf("unexpected lookpath query: %q", file)
		}
		return "/usr/bin/uname", nil
	}
	execCommandOutput = func(name string, args ...string) ([]byte, error) {
		if name != "uname" {
			t.Fatalf("unexpected command name: %q", name)
		}
		if len(args) != 1 || args[0] != "-a" {
			t.Fatalf("unexpected command args: %#v", args)
		}
		return []byte("Test Kernel 1.0\n"), nil
	}

	prompt, err := readSystemPrompt()
	if err != nil {
		t.Fatalf("readSystemPrompt error: %v", err)
	}

	want := "host=Test Kernel 1.0"
	if prompt != want {
		t.Fatalf("expanded prompt mismatch: got %q want %q", prompt, want)
	}
}

func TestReadSystemPromptErrors(t *testing.T) {
	origGetwd := osGetwd
	origHome := osUserHomeDir
	origReadFile := osReadFile
	t.Cleanup(func() {
		osGetwd = origGetwd
		osUserHomeDir = origHome
		osReadFile = origReadFile
	})

	t.Run("getwd error", func(t *testing.T) {
		osUserHomeDir = func() (string, error) { return "/tmp/home", nil }
		osReadFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
		osGetwd = func() (string, error) { return "", errors.New("cwd fail") }
		_, err := readSystemPrompt()
		if err == nil || !strings.Contains(err.Error(), "cwd fail") {
			t.Fatalf("expected cwd fail error, got %v", err)
		}
		osGetwd = origGetwd
		osReadFile = origReadFile
		osUserHomeDir = origHome
	})

	t.Run("cwd read unexpected error", func(t *testing.T) {
		osGetwd = func() (string, error) { return "/tmp", nil }
		osReadFile = func(string) ([]byte, error) { return nil, errors.New("read fail") }
		_, err := readSystemPrompt()
		if err == nil || !strings.Contains(err.Error(), "read fail") {
			t.Fatalf("expected read fail error, got %v", err)
		}
		osGetwd = origGetwd
		osReadFile = origReadFile
	})

	t.Run("home dir error", func(t *testing.T) {
		osGetwd = func() (string, error) { return "/tmp", nil }
		osReadFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
		osUserHomeDir = func() (string, error) { return "", errors.New("home fail") }
		_, err := readSystemPrompt()
		if err == nil || !strings.Contains(err.Error(), "home fail") {
			t.Fatalf("expected home fail error, got %v", err)
		}
		osGetwd = origGetwd
		osReadFile = origReadFile
		osUserHomeDir = origHome
	})

	t.Run("home read unexpected error", func(t *testing.T) {
		calls := 0
		osGetwd = func() (string, error) { return "/tmp", nil }
		osUserHomeDir = func() (string, error) { return "/home/test", nil }
		osReadFile = func(path string) ([]byte, error) {
			calls++
			if calls == 1 {
				return nil, os.ErrNotExist
			}
			return nil, errors.New("home read fail")
		}
		_, err := readSystemPrompt()
		if err == nil || !strings.Contains(err.Error(), "home read fail") {
			t.Fatalf("expected home read fail error, got %v", err)
		}
		osGetwd = origGetwd
		osReadFile = origReadFile
		osUserHomeDir = origHome
	})
}

func TestBuildSystemPrompt(t *testing.T) {
	now := time.Date(2026, time.July, 24, 9, 15, 30, 0, time.FixedZone("PDT", -7*3600))

	t.Run("header only when empty prompt", func(t *testing.T) {
		got := buildSystemPrompt("", now)
		if !strings.HasPrefix(got, "Current local datetime: 2026-07-24T09:15:30-07:00\n\n") {
			t.Fatalf("unexpected prompt header: got %q", got)
		}
		if !strings.Contains(got, "run_sub_agent") || !strings.Contains(got, "untrusted evidence") {
			t.Fatalf("expected delegation guidance, got %q", got)
		}
	})

	t.Run("header plus prompt body", func(t *testing.T) {
		got := buildSystemPrompt("sys-msg", now)
		if !strings.HasPrefix(got, "Current local datetime: 2026-07-24T09:15:30-07:00\n\n") {
			t.Fatalf("expected datetime prefix, got %q", got)
		}
		if !strings.HasSuffix(got, "sys-msg") {
			t.Fatalf("expected user prompt suffix, got %q", got)
		}
		if !strings.Contains(got, "only for an independent, well-scoped task") {
			t.Fatalf("expected delegation guidance, got %q", got)
		}
	})
}
