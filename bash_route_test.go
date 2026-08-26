package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	marker := "\tcommand ash route --check -- \"$cmd\" \"$@\"\n}"
	end := strings.Index(asset[start:], marker)
	if end < 0 {
		t.Fatal("bash asset no longer delegates to ash route --check")
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
		"test is this thing on?",
		"type is this a question?",
	}

	for _, line := range corpus {
		fields := strings.Fields(line)
		if !shouldRoutePrompt(line) {
			t.Fatalf("corpus entry %q is not routable in Go; fix the test corpus", line)
		}
		passed, _ := runBashPrefilter(t, fields)
		if !passed {
			t.Errorf("bash guards rejected %q, but Go would route it", line)
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
	if !strings.Contains(asset, "command ash route --check -- \"$cmd\" \"$@\"") {
		t.Error("bash asset must delegate routing to ash route --check")
	}
	for _, duplicated := range []string{"natural_wrapper=1", "has_path_like", "teatime"} {
		if strings.Contains(asset, duplicated) {
			t.Errorf("bash asset still contains duplicated heuristic marker %q", duplicated)
		}
	}
}
