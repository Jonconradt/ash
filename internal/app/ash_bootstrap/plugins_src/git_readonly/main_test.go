package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testPlugin() (*gitPlugin, *bytes.Buffer) {
	return &gitPlugin{}, &bytes.Buffer{}
}

func TestAIDocs(t *testing.T) {
	p := &gitPlugin{}
	var docs map[string]any
	if err := json.Unmarshal([]byte(p.AIDocs()), &docs); err != nil {
		t.Fatalf("AIDocs is invalid JSON: %v", err)
	}
	for _, key := range []string{"schema_version", "name", "version", "input_schema", "output_schema", "Capabilities", "Arguments", "Return format"} {
		if _, ok := docs[key]; !ok {
			t.Errorf("missing AI docs key %q", key)
		}
	}
}

func TestListAndQuery(t *testing.T) {
	p, _ := testPlugin()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	var stdout bytes.Buffer
	if code := p.Run(context.Background(), []string{"--list"}, &stdout, &bytes.Buffer{}, logger); code != 0 {
		t.Fatalf("list code = %d", code)
	}
	var got output
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("list output is invalid JSON: %v", err)
	}
	if got.Status != "success" || len(got.Commands) == 0 {
		t.Fatalf("unexpected list result: %#v", got)
	}

	stdout.Reset()
	if code := p.Run(context.Background(), []string{"--query", "status"}, &stdout, &bytes.Buffer{}, logger); code != 0 {
		t.Fatalf("query code = %d", code)
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("query output is invalid JSON: %v", err)
	}
	if got.Status != "success" || !got.Available || got.Command != "status" {
		t.Fatalf("unexpected query result: %#v", got)
	}

	stdout.Reset()
	if code := p.Run(context.Background(), []string{"--query", "checkout"}, &stdout, &bytes.Buffer{}, logger); code != 0 {
		t.Fatalf("missing query code = %d", code)
	}
	got = output{}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("missing query output is invalid JSON: %v", err)
	}
	if got.Available {
		t.Fatal("checkout must not be available")
	}
}

func TestRejectsMutatingAndEscapeArguments(t *testing.T) {
	p, _ := testPlugin()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	tests := [][]string{
		{"--run", "checkout"},
		{"--run", "status", "--arg", "-C"},
		{"--run", "status", "--arg", "--git-dir=/tmp/repo"},
		{"--run", "status", "--arg", "../outside"},
		{"--run", "diff", "--arg", "--output=/tmp/out"},
		{"--run", "status", "--arg", "--ignored", "extra"},
	}
	for _, args := range tests {
		var stdout, stderr bytes.Buffer
		code := p.Run(context.Background(), args, &stdout, &stderr, logger)
		if code == 0 {
			t.Errorf("expected rejection for %v, stdout=%s", args, stdout.String())
		}
	}
}

func TestRunStatusInRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "tracked.txt")
	runGit(t, dir, "commit", "-qm", "initial")

	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	p := &gitPlugin{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var stdout, stderr bytes.Buffer
	code := p.Run(context.Background(), []string{"--run", "status", "--arg", "--short"}, &stdout, &stderr, logger)
	if code != 0 {
		t.Fatalf("run status failed: %d, stdout=%s, stderr=%s", code, stdout.String(), stderr.String())
	}
	var got output
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("invalid status JSON: %v", err)
	}
	if got.Status != "success" || got.Command != "status" {
		t.Fatalf("unexpected status result: %#v", got)
	}
}

func TestRefusesRepositorySymlinkAndParentDiscovery(t *testing.T) {
	dir := t.TempDir()
	realRepo := filepath.Join(t.TempDir(), "repo")
	if err := os.Mkdir(realRepo, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realRepo, filepath.Join(dir, ".git")); err != nil {
		t.Skip("symlinks unavailable")
	}

	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	p := &gitPlugin{}
	var stdout bytes.Buffer
	code := p.Run(context.Background(), []string{"--run", "status"}, &stdout, &bytes.Buffer{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if code == 0 {
		t.Fatalf("symlinked .git must be rejected: %s", stdout.String())
	}
	var got output
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("symlink error output is invalid JSON: %v", err)
	}
	if got.Status != "error" || got.ErrorReason != "unsafe_git_metadata" {
		t.Fatalf("unexpected symlink error: %#v", got)
	}
	if !strings.Contains(got.ErrorMessage, "Unsafe Git repository metadata") ||
		!strings.Contains(got.ErrorMessage, "will not follow") {
		t.Fatalf("symlink error is not informative: %q", got.ErrorMessage)
	}
}

func TestReportsNotInGitRepository(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	p := &gitPlugin{}
	var stdout bytes.Buffer
	code := p.Run(context.Background(), []string{"--run", "status"}, &stdout, &bytes.Buffer{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if code == 0 {
		t.Fatalf("directory outside a repository must be rejected: %s", stdout.String())
	}
	var got output
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("missing repository error is invalid JSON: %v", err)
	}
	if got.Status != "error" || got.ErrorReason != "not_git_repository" {
		t.Fatalf("unexpected missing repository error: %#v", got)
	}
	if !strings.Contains(got.ErrorMessage, "Not in a Git repository") ||
		!strings.Contains(got.ErrorMessage, "repository root") {
		t.Fatalf("missing repository error is not informative: %q", got.ErrorMessage)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %v: %s", strings.Join(args, " "), err, out)
	}
}
