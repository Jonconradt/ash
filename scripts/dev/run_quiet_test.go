package dev

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRunQuietWithoutSetsid(t *testing.T) {
	script, err := filepath.Abs("run-quiet.sh")
	if err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	for _, name := range []string{"bash", "mktemp", "rm", "cat"} {
		source, err := exec.LookPath(name)
		if err != nil {
			t.Skipf("%s not found: %v", name, err)
		}
		if err := os.Symlink(source, filepath.Join(binDir, name)); err != nil {
			t.Fatal(err)
		}
	}

	command := "/bin/sh"
	if runtime.GOOS == "windows" {
		t.Skip("run-quiet.sh requires a POSIX shell")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, script, "portable", command, "-c", "printf 'quiet failure\\n'; exit 7")
	cmd.Env = []string{"PATH=" + binDir, "TMPDIR=" + t.TempDir()}
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("run-quiet.sh succeeded for failing command")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 7 {
		t.Fatalf("unexpected exit error: %v; output:\n%s", err, output)
	}
	if !strings.Contains(string(output), "quiet failure") {
		t.Fatalf("missing command output:\n%s", output)
	}
}
