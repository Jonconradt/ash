// Copyright (c) 2026 Jonathan L. Conradt
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// extractBashRouteGuards pulls the shipped prefilter out of the real bash asset so these
// tests exercise installed behavior instead of a fixture copy.
func extractBashRouteGuards(t *testing.T) string {
	t.Helper()
	content, err := readEmbeddedBootstrapAsset("ash_bootstrap/.ash_bashrc")
	if err != nil {
		t.Fatalf("read embedded bash asset: %v", err)
	}
	asset := string(content)
	start := strings.Index(asset, "# Question words that can open")
	if start < 0 {
		t.Fatal("bash asset is missing the routing guards")
	}
	marker := "\treturn 0\n}"
	end := strings.Index(asset[start:], marker)
	if end < 0 {
		t.Fatal("bash asset is missing the routing guard terminator")
	}
	return asset[start : start+end+len(marker)]
}

// runBashPrefilter reports whether the bash guards passed the input through to Go,
// and whether the ash subprocess was consulted at all.
func runBashPrefilter(t *testing.T, fields []string) (passed bool, consultedAsh bool) {
	t.Helper()
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("bash not available: %v", err)
	}

	shimDir := t.TempDir()
	marker := filepath.Join(shimDir, "consulted")
	shim := "#!/bin/sh\ntouch " + marker + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(shimDir, "ash"), []byte(shim), 0o700); err != nil {
		t.Fatalf("write ash shim: %v", err)
	}

	script := extractBashRouteGuards(t) + "\n_ash_should_route \"$@\"\n"
	args := append([]string{"-c", script, "bash"}, fields...)
	cmd := exec.CommandContext(context.Background(), bashPath, args...) // #nosec G204 -- fixed bash path with test-controlled args.
	cmd.Env = append(os.Environ(), "PATH="+shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	runErr := cmd.Run()

	_, statErr := os.Stat(marker)
	return runErr == nil, statErr == nil
}

// The bash guards are an optimization in front of shouldRoutePrompt. They may reject
// only inputs Go would also reject; rejecting a routable prompt would be a real bug.
func TestBashPrefilterNeverRejectsRoutablePrompts(t *testing.T) {
	corpus := []string{
		"What time is it?",
		"what time is it?",
		"write a poem using the following as inspiration love.txt",
		"What directory am I in and are there any executeable files",
		"what time is it and list all of the files in the ~/.ash/logs",
		"Time is it late?",
		"where should logs go",
		"who am I and list files in ~/.ash/logs",
		"In this repo what files changed",
		"For this error what should I do",
		"at remind me tomorrow",
		"which file is bigger?",
		"say something witty",
		"test is this thing on?",
		"type is this a question?",
	}

	for _, line := range corpus {
		fields := strings.Fields(line)
		if !shouldRoutePrompt(line) {
			t.Fatalf("corpus entry %q is not routable in Go; fix the test corpus", line)
		}
		passed, consulted := runBashPrefilter(t, fields)
		if !passed {
			t.Errorf("bash guards rejected %q, but Go would route it", line)
		}
		if consulted {
			t.Errorf("bash guards forked ash for %q", line)
		}
	}
}

// Ordinary command use must never pay for an ash subprocess.
func TestBashPrefilterSkipsSubprocessForRealCommands(t *testing.T) {
	cases := []string{
		"test -f /etc/hosts",
		"type ls",
		"which ls",
		"who",
		"test a = b",
		"type -a ls",
		"what /usr/bin/what",
		"at 5pm",
		"say -v Alex hello",
		"say --version",
	}

	for _, line := range cases {
		fields := strings.Fields(line)
		passed, consulted := runBashPrefilter(t, fields)
		if passed {
			t.Errorf("bash guards routed %q, expected passthrough", line)
		}
		if consulted {
			t.Errorf("bash guards forked ash for %q; this must stay fork-free", line)
		}
		if shouldRoutePrompt(line) {
			t.Errorf("Go would route %q, so the bash guard rejection is a behavior change", line)
		}
	}
}

// Guards against the bash asset regrowing its own copy of the heuristic.
func TestBashAssetDelegatesRoutingPolicyToGo(t *testing.T) {
	content, err := readEmbeddedBootstrapAsset("ash_bootstrap/.ash_bashrc")
	if err != nil {
		t.Fatalf("read embedded bash asset: %v", err)
	}
	asset := string(content)
	if strings.Contains(asset, "command ash route --check") {
		t.Error("bash asset must not launch a separate routing process")
	}
	if !strings.Contains(asset, "command ash --internal-route-prompt") {
		t.Error("bash asset must combine routing and prompt execution")
	}

	for _, duplicated := range []string{"natural_wrapper=1", "has_path_like", "teatime"} {
		if strings.Contains(asset, duplicated) {
			t.Errorf("bash asset still contains duplicated heuristic marker %q", duplicated)
		}
	}
}

func TestBashRoutingUsesOneAshProcess(t *testing.T) {
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("bash not available: %v", err)
	}
	content, err := readEmbeddedBootstrapAsset("ash_bootstrap/.ash_bashrc")
	if err != nil {
		t.Fatal(err)
	}
	asset := string(content)
	start := strings.Index(asset, "# Question words that can open")
	if start < 0 {
		t.Fatal("missing routing functions")
	}
	for _, tc := range []struct {
		name      string
		command   string
		ashExit   string
		wantExit  int
		wantCalls string
	}{
		{"prompt", "What is the time?", "0", 0, "ash --internal-route-prompt What is the time?\n"},
		{"failed prompt", "What is the time?", "1", 1, "ash --internal-route-prompt What is the time?\n"},
		{"native fallback", "What is the time?", "125", 0, "ash --internal-route-prompt What is the time?\nWhat is the time?\n"},
		{"ordinary command", "which ls", "0", 0, "which ls\n"},
		{"builtin prompt", "test is this a question?", "0", 0, "ash --internal-route-prompt test is this a question?\n"},
		{"builtin fallback", "test is this a question?", "125", 2, "ash --internal-route-prompt test is this a question?\n"},
		{"speech prompt", "say something witty", "0", 0, "ash --internal-route-prompt --say say something witty\n"},
		{"speech fallback", "say something witty", "125", 0, "ash --internal-route-prompt --say say something witty\nsay something witty\n"},
		{"unknown command", "Summarize my bio", "0", 0, "ash -- Summarize my bio\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, name := range []string{"ash", "What", "which", "say"} {
				script := "#!/bin/sh\nprintf '%s %s\\n' " + name + " \"$*\" >> \"$ASH_TEST_CALLS\"\n"
				if name == "ash" {
					script += "exit \"$ASH_TEST_EXIT\"\n"
				}
				if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			hookStart := strings.Index(asset, "command_not_found_handle() {")
			hookEnd := strings.Index(asset[hookStart:], "\nash() {")
			script := asset[hookStart:hookStart+hookEnd] + "\n" + asset[start:] +
				"\n_ash_prompt_processing_enabled() { return 0; }\n_ash_ensure_broker() { return 0; }\n" + tc.command + "\n"
			callsPath := filepath.Join(dir, "calls")
			cmd := exec.CommandContext(context.Background(), bashPath, "--noprofile", "--norc", "-ec", script)
			cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "ASH_TEST_CALLS="+callsPath, "ASH_TEST_EXIT="+tc.ashExit)
			output, runErr := cmd.CombinedOutput()
			exitCode := 0
			if runErr != nil {
				var exitErr *exec.ExitError
				if !errors.As(runErr, &exitErr) {
					t.Fatal(runErr)
				}
				exitCode = exitErr.ExitCode()
			}
			if exitCode != tc.wantExit {
				t.Fatalf("exit = %d, want %d; output=%q", exitCode, tc.wantExit, output)
			}
			calls, err := os.ReadFile(callsPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(calls) != tc.wantCalls {
				t.Fatalf("calls = %q, want %q", calls, tc.wantCalls)
			}
		})
	}
}
func TestBashCommandNotFoundDoesNotReenterWhenAshIsMissing(t *testing.T) {
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("bash not available: %v", err)
	}
	content, err := readEmbeddedBootstrapAsset("ash_bootstrap/.ash_bashrc")
	if err != nil {
		t.Fatalf("read embedded bash asset: %v", err)
	}

	home := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bashPath, "--noprofile", "--norc", "-ic", string(content)+"\nSummarize my bio\n")
	cmd.Env = []string{"HOME=" + home, "PATH=", "ASH_SESSION_ID=test"}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("bash command-not-found hook did not terminate: %v", ctx.Err())
	}
	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) || exitErr.ExitCode() != 127 {
		t.Fatalf("bash command exit = %v, want status 127; stderr=%q", runErr, stderr.String())
	}
	if strings.Contains(stderr.String(), "maximum function nesting level") {
		t.Fatalf("bash command-not-found hook recursed: %q", stderr.String())
	}
}
