// Copyright (c) 2026 Jonathan L. Conradt
// SPDX-License-Identifier: MIT

package release

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func writeFixture(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func copyFixture(t *testing.T, root, dir, name string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(dir, name), string(data), 0o755)
}

func fixtureCommand(dir string, env []string, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(context.Background(), name, args...)
	cmd.Dir = dir
	cmd.Env = append([]string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + dir,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Release Test",
		"GIT_AUTHOR_EMAIL=release-test@example.invalid",
		"GIT_COMMITTER_NAME=Release Test",
		"GIT_COMMITTER_EMAIL=release-test@example.invalid",
	}, env...)
	return cmd
}

func runFixture(t *testing.T, dir string, env []string, name string, args ...string) string {
	t.Helper()
	output, err := fixtureCommand(dir, env, name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, output)
	}
	return string(output)
}

func releaseFixture(t *testing.T) (string, string) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, name := range []string{"Makefile", "scripts/release/next_version.sh", "scripts/release/push_with_retry.sh"} {
		copyFixture(t, root, dir, name)
	}
	writeFixture(t, filepath.Join(dir, ".gitignore"), "/dist/\n/.cache/\n", 0o644)
	runFixture(t, dir, nil, "git", "init", "-b", "main")
	runFixture(t, dir, nil, "git", "add", ".")
	runFixture(t, dir, nil, "git", "commit", "-m", "fixture")
	return root, dir
}

func TestBuildVersionUsesNearestReachableTag(t *testing.T) {
	_, dir := releaseFixture(t)
	runFixture(t, dir, nil, "git", "tag", "v0.28.0")
	runFixture(t, dir, nil, "git", "commit", "--allow-empty", "-m", "prepare beta")
	runFixture(t, dir, nil, "git", "tag", "v0.29.0-beta.1")
	runFixture(t, dir, nil, "git", "commit", "--allow-empty", "-m", "continue beta")

	writeFixture(t, filepath.Join(dir, "build-version.mk"), `include Makefile
.PHONY: print-build-version
print-build-version:
	@echo "$(BUILD_VERSION)"
`, 0o644)
	got := strings.TrimSpace(runFixture(t, dir, nil, "make", "--no-print-directory", "-f", "build-version.mk", "print-build-version"))
	if got != "v0.29.0-beta.1" {
		t.Fatalf("BUILD_VERSION = %q, want nearest prerelease tag v0.29.0-beta.1", got)
	}
}

func TestChannelVersions(t *testing.T) {
	_, dir := releaseFixture(t)
	for _, channel := range []string{"beta", "stable", "nightly"} {
		got := strings.TrimSpace(runFixture(t, dir, nil, "bash", "scripts/release/next_version.sh", channel))
		if !strings.HasPrefix(got, "v0.1.0") {
			t.Fatalf("baseline %s: %s", channel, got)
		}
	}
	for _, tag := range []string{"v1.1.7", "v1.2.0-beta.2", "v1.2.0-beta.10", "v9.0.0-nightly.20260101"} {
		runFixture(t, dir, nil, "git", "tag", tag)
	}
	expected := map[string]string{
		"beta":    "v1.2.0-beta.11",
		"stable":  "v1.2.0",
		"nightly": "v1.2.0-nightly." + time.Now().UTC().Format("20060102"),
	}
	for channel, want := range expected {
		got := strings.TrimSpace(runFixture(t, dir, nil, "bash", "scripts/release/next_version.sh", channel))
		if got != want {
			t.Fatalf("%s: got %s, want %s", channel, got, want)
		}
	}
	if err := fixtureCommand(dir, nil, "bash", "scripts/release/next_version.sh", "invalid").Run(); err == nil {
		t.Fatal("invalid channel accepted")
	}
}

func TestReleaseVersionRemainsFrozenAfterTagging(t *testing.T) {
	for _, channel := range []string{"beta", "stable", "nightly"} {
		t.Run(channel, func(t *testing.T) {
			_, dir := releaseFixture(t)
			makefile := filepath.Join(dir, "Makefile")
			data, err := os.ReadFile(makefile)
			if err != nil {
				t.Fatal(err)
			}
			writeFixture(t, makefile, strings.Replace(string(data),
				"release-check: $(RELEASE_VERIFY_TARGET)",
				"release-check:", 1), 0o644)
			runFixture(t, dir, nil, "git", "tag", "v1.1.7")
			want := strings.TrimSpace(runFixture(t, dir, nil, "bash", "scripts/release/next_version.sh", channel))
			writeFixture(t, filepath.Join(dir, "steps.mk"), `
release-check release-clean release-build release-translate release-pkg release-validate release-notes:
	@echo "STEP $(RELEASE_VERSION)"
release-publish:
	@git tag "$(RELEASE_VERSION)"
	@echo "STEP $(RELEASE_VERSION)"
release-watch:
	@echo "STEP $(RELEASE_VERSION)"
`, 0o644)
			writeFixture(t, filepath.Join(dir, "invoke.mk"), `
MAKE := $(MAKE) -f Makefile -f steps.mk
`, 0o644)
			output := runFixture(t, dir, nil, "make", "--no-print-directory", "-f", "Makefile", "-f", "steps.mk", "-f", "invoke.mk", "release", "CHANNEL="+channel)
			count := 0
			for line := range strings.SplitSeq(output, "\n") {
				if strings.HasPrefix(line, "STEP ") {
					count++
					if line != "STEP "+want {
						t.Fatalf("version changed after tagging: %s; want %s", line, want)
					}
				}
			}
			wantSteps := 9
			if channel == "nightly" {
				wantSteps--
			}
			if count != wantSteps {
				t.Fatalf("ran %d release steps, want %d\n%s", count, wantSteps, output)
			}
		})
	}
}

func TestReleaseDashboardRunsOnlyOutsideCIByDefault(t *testing.T) {
	_, dir := releaseFixture(t)
	marker := filepath.Join(dir, "dashboard-invoked")
	writeFixture(t, filepath.Join(dir, "bin/gh"), `#!/bin/bash
printf '%s\n' 'fixture/repo'
`, 0o755)
	writeFixture(t, filepath.Join(dir, "scripts/release/release_dashboard.sh"), `#!/bin/bash
printf '%s\n' "$*" > "$DASHBOARD_MARKER"
`, 0o755)
	path := filepath.Join(dir, "bin") + ":" + os.Getenv("PATH")

	ciOutput := runFixture(t, dir, []string{
		"PATH=" + path,
		"CI=true",
		"DASHBOARD_MARKER=" + marker,
	}, "make", "release-watch", "RELEASE_VERSION=v1.2.0")
	if !strings.Contains(ciOutput, "release dashboard disabled") {
		t.Fatalf("CI release watch did not report that the dashboard is disabled: %s", ciOutput)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("dashboard ran in CI; marker stat error = %v", err)
	}

	manualOutput := runFixture(t, dir, []string{
		"PATH=" + path,
		"DASHBOARD_MARKER=" + marker,
	}, "make", "release-watch", "RELEASE_VERSION=v1.2.0")
	if !strings.Contains(manualOutput, "launching release dashboard") {
		t.Fatalf("manual release did not launch the dashboard: %s", manualOutput)
	}
	if data, err := os.ReadFile(marker); err != nil || !strings.Contains(string(data), "--tag v1.2.0 --repo fixture/repo") {
		t.Fatalf("manual release dashboard args = %q, %v", data, err)
	}
}

func TestReleaseVerificationDefaultsToFullSuite(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	makefile := string(data)
	if !strings.Contains(makefile, "RELEASE_VERIFY_TARGET ?= verify\n") ||
		!strings.Contains(makefile, "release-check: $(RELEASE_VERIFY_TARGET)") {
		t.Fatal("release target does not default to full verification")
	}
}

func TestVerifyCIUsesPrePushGate(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	_, section, found := strings.Cut(string(data), "verify-ci:\n")
	if !found {
		t.Fatal("missing verify-ci target")
	}
	section, _, _ = strings.Cut(section, "\nsync-route-words:")
	if !strings.Contains(section, "$(MAKE) --no-print-directory pre-push") {
		t.Fatal("verify-ci does not run the local pre-push gate")
	}
	if !strings.Contains(string(data), `"test-race"`) || !strings.Contains(string(data), `"benchmark"`) {
		t.Fatal("full verify target lost its extended checks")
	}
	_, gate, found := strings.Cut(string(data), "pre-push:\n")
	if !found {
		t.Fatal("missing pre-push target")
	}
	gate, _, _ = strings.Cut(gate, "\ncheck-security:")
	for _, check := range []string{"lint", "security", "test", "gitleaks"} {
		if !strings.Contains(gate, check) {
			t.Errorf("pre-push gate omits %s", check)
		}
	}
}

func TestPrePushValidatesExactCommitAndMergeBase(t *testing.T) {
	root, dir, base, pushed := prePushFixture(t)
	writeFixture(t, filepath.Join(dir, "README.md"), "dirty local worktree\n", 0o644)

	logPath := filepath.Join(t.TempDir(), "make.log")
	path := installFakeMake(t, logPath, 0)
	input := fmt.Sprintf("refs/heads/main %s refs/heads/main %s\n", pushed, base)
	output := runPrePush(t, root, dir, path, logPath, input)
	if !strings.Contains(output, "Validating pushed commit "+pushed) {
		t.Fatalf("helper did not identify pushed commit: %s", output)
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(log)), "\n")
	if len(lines) < 6 {
		t.Fatalf("fake make log is incomplete: %q", log)
	}
	if strings.Contains(lines[0], dir) {
		t.Fatalf("validation did not use a separate worktree: %q", lines[0])
	}
	if lines[1] != "pre-push" || lines[2] != "PUSH_BASE="+base || lines[3] != pushed {
		t.Fatalf("validation arguments and commit = %q, want pre-push, PUSH_BASE=%s, %s", lines, base, pushed)
	}
	if lines[4] != "committed revision" || lines[5] != "second commit" {
		t.Fatalf("validation read working-tree changes instead of pushed content: %q", lines)
	}
}

func TestPrePushFailureBlocksAndSkipsSecretScan(t *testing.T) {
	root, dir, base, pushed := prePushFixture(t)
	logPath := filepath.Join(t.TempDir(), "make.log")
	path := installFakeMake(t, logPath, 7)
	input := fmt.Sprintf("refs/heads/main %s refs/heads/main %s\n", pushed, base)
	cmd := exec.CommandContext(context.Background(), "bash", filepath.Join(root, "scripts/dev/pre-push.sh"))
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+path+":"+os.Getenv("PATH"), "PRE_PUSH_LOG="+logPath)
	cmd.Stdin = strings.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "pre-push validation failed") {
		t.Fatalf("failed validation did not block push: err=%v output=%s", err, output)
	}
	log, readErr := os.ReadFile(logPath)
	if readErr != nil || strings.Contains(string(log), "gitleaks") {
		t.Fatalf("secret scan should not run after failed checks: log=%q err=%v", log, readErr)
	}
}

func TestPrePushSkipsDeletedRefs(t *testing.T) {
	root, dir, _, _ := prePushFixture(t)
	logPath := filepath.Join(t.TempDir(), "make.log")
	path := installFakeMake(t, logPath, 0)
	zeroOID := strings.Repeat("0", 40)
	input := fmt.Sprintf("refs/heads/deleted %s refs/heads/deleted %s\n", zeroOID, zeroOID)
	output := runPrePush(t, root, dir, path, logPath, input)
	if strings.Contains(output, "Validating pushed commit") {
		t.Fatalf("deleted ref was validated: %s", output)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("deleted ref invoked make; stat error = %v", err)
	}
}

func TestHookInstallerPreservesCustomPath(t *testing.T) {
	root, dir := releaseFixture(t)
	copyFixture(t, root, dir, "scripts/dev/install_hooks.sh")
	writeFixture(t, filepath.Join(dir, "githooks/pre-commit"), "#!/bin/sh\nexit 0\n", 0o755)
	writeFixture(t, filepath.Join(dir, "githooks/pre-push"), "#!/bin/sh\nexit 0\n", 0o755)

	runFixture(t, dir, nil, "bash", "scripts/dev/install_hooks.sh")
	if got := strings.TrimSpace(runFixture(t, dir, nil, "git", "config", "--local", "--get", "core.hooksPath")); got != "githooks" {
		t.Fatalf("hooks path = %q, want githooks", got)
	}

	runFixture(t, dir, nil, "git", "config", "--local", "core.hooksPath", "custom-hooks")
	cmd := fixtureCommand(dir, nil, "bash", "scripts/dev/install_hooks.sh")
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "refusing to replace custom hooks") {
		t.Fatalf("custom hook path was not protected: err=%v output=%s", err, output)
	}
	if got := strings.TrimSpace(runFixture(t, dir, nil, "git", "config", "--local", "--get", "core.hooksPath")); got != "custom-hooks" {
		t.Fatalf("custom hooks path changed to %q", got)
	}
}

func prePushFixture(t *testing.T) (string, string, string, string) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	copyFixture(t, root, dir, "scripts/dev/pre-push.sh")
	writeFixture(t, filepath.Join(dir, "README.md"), "committed revision\n", 0o644)
	runFixture(t, dir, nil, "git", "init", "-b", "main")
	runFixture(t, dir, nil, "git", "add", ".")
	runFixture(t, dir, nil, "git", "commit", "-m", "base")
	base := strings.TrimSpace(runFixture(t, dir, nil, "git", "rev-parse", "HEAD"))
	writeFixture(t, filepath.Join(dir, "README.md"), "committed revision\nsecond commit\n", 0o644)
	runFixture(t, dir, nil, "git", "add", ".")
	runFixture(t, dir, nil, "git", "commit", "-m", "pushed")
	pushed := strings.TrimSpace(runFixture(t, dir, nil, "git", "rev-parse", "HEAD"))
	return root, dir, base, pushed
}

func installFakeMake(t *testing.T, logPath string, status int) string {
	t.Helper()
	dir := t.TempDir()
	makeScript := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$PWD" "$@" "$(git rev-parse HEAD)" "$(cat README.md)" > "$PRE_PUSH_LOG"
exit %d
`, status)
	writeFixture(t, filepath.Join(dir, "make"), makeScript, 0o755)
	if runtime.GOOS == "windows" {
		t.Skip("pre-push hook tests require a POSIX shell")
	}
	return dir
}

func runPrePush(t *testing.T, root, dir, path, logPath, input string) string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "bash", filepath.Join(root, "scripts/dev/pre-push.sh"))
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+path+":"+os.Getenv("PATH"), "PRE_PUSH_LOG="+logPath)
	cmd.Stdin = strings.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pre-push helper: %v\n%s", err, output)
	}
	return string(output)
}

func TestNightlyRetriesUseImmutableVersions(t *testing.T) {
	_, dir := releaseFixture(t)
	runFixture(t, dir, nil, "git", "tag", "v1.1.7")
	base := "v1.2.0-nightly." + time.Now().UTC().Format("20060102")
	runFixture(t, dir, nil, "git", "tag", base)
	for _, suffix := range []string{".1", ".2", ".10"} {
		got := strings.TrimSpace(runFixture(t, dir, nil, "bash", "scripts/release/next_version.sh", "nightly"))
		if got != base+suffix {
			t.Fatalf("got %s, want %s", got, base+suffix)
		}
		runFixture(t, dir, nil, "git", "tag", got)
		if suffix == ".2" {
			runFixture(t, dir, nil, "git", "tag", base+".9")
			runFixture(t, dir, nil, "git", "tag", base+".invalid")
			runFixture(t, dir, nil, "git", "tag", base+".01")
		}
	}
}

func workflowStep(t *testing.T, root, workflow, step string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".github/workflows", workflow))
	if err != nil {
		t.Fatal(err)
	}
	_, section, found := strings.Cut(string(data), "- name: "+step+"\n")
	if !found {
		t.Fatalf("missing workflow step %s", step)
	}
	_, script, found := strings.Cut(section, "run: |\n")
	if !found {
		t.Fatalf("missing script for %s", step)
	}
	script, _, _ = strings.Cut(script, "      - name:")
	lines := strings.Split(strings.TrimRight(script, "\n"), "\n")
	for i := range lines {
		lines[i] = strings.TrimPrefix(lines[i], "          ")
	}
	return strings.Join(lines, "\n")
}

func TestReleaseWorkflowDoesNotVerifyInstalls(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".github/workflows/release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"Verify external Go installs from the release tag",
		"go install \"github.com/Jonconradt/ash/",
		"Install and smoke test macOS package",
		"sudo installer -pkg",
		"Install and smoke test Linux packages",
		"sudo apt-get install -y \"./$deb\"",
		"sudo rpm -i \"$rpm\"",
		"\"$binary\" install --shell",
		"Install ash in a FreeBSD VM",
	} {
		if strings.Contains(string(data), forbidden) {
			t.Errorf("release workflow must not verify application installs: found %q", forbidden)
		}
	}
}

func TestScheduledNightlyUsesPublishedRelease(t *testing.T) {
	root, dir := releaseFixture(t)
	tag := "v1.2.0-nightly.20261011"
	runFixture(t, dir, nil, "git", "tag", tag)
	writeFixture(t, filepath.Join(dir, "bin/gh"), `#!/bin/bash
[[ "$*" == *"--paginate --slurp"* ]] || exit 1
if [[ "$TEST_API_FAIL" == yes ]]; then exit 1; fi
if [[ -n "$TEST_PUBLISHED_TAG" ]]; then
  printf '[[{"draft":false,"tag_name":"%s"}]]' "$TEST_PUBLISHED_TAG"
else
  printf '[[]]'
fi
`, 0o755)
	script := workflowStep(t, root, "nightly.yml", "Check for changes since the latest nightly")
	for _, published := range []string{"", tag} {
		output := filepath.Join(t.TempDir(), "output")
		env := []string{
			"PATH=" + filepath.Join(dir, "bin") + ":" + os.Getenv("PATH"),
			"GITHUB_REPOSITORY=fixture/repo", "GITHUB_OUTPUT=" + output,
			"TEST_PUBLISHED_TAG=" + published,
		}
		runFixture(t, dir, env, "bash", "-c", script)
		data, err := os.ReadFile(output)
		if err != nil {
			t.Fatal(err)
		}
		want := "latest_tag=\npublish=true\n"
		if published != "" {
			want = "latest_tag=" + published + "\npublish=false\n"
		}
		if string(data) != want {
			t.Fatalf("published=%q: got %q, want %q", published, data, want)
		}
	}
	env := []string{"PATH=" + filepath.Join(dir, "bin") + ":" + os.Getenv("PATH"), "GITHUB_REPOSITORY=fixture/repo", "TEST_API_FAIL=yes"}
	if err := fixtureCommand(dir, env, "bash", "-c", script).Run(); err == nil {
		t.Fatal("release API failure was ignored")
	}
}

func TestRepositoryNightlyRetryVersionMapping(t *testing.T) {
	root, dir := releaseFixture(t)
	script := workflowStep(t, root, "apt.yml", "Resolve release channel package versions")
	for _, tag := range []string{"v1.2.0-nightly.20261011", "v1.2.0-nightly.20261011.1"} {
		output := filepath.Join(t.TempDir(), "env")
		runFixture(t, dir, []string{"RELEASE_TAG=" + tag, "GITHUB_ENV=" + output}, "bash", "-c", script)
		data, err := os.ReadFile(output)
		if err != nil {
			t.Fatal(err)
		}
		version := strings.TrimPrefix(tag, "v")
		want := "PACKAGE_CHANNEL=nightly\nPACKAGE_VERSION=" + strings.Replace(version, "-nightly.", "~nightly.", 1) +
			"\nARCH_VERSION=" + strings.Replace(version, "-nightly.", ".nightly.", 1) + "\n"
		if string(data) != want {
			t.Fatalf("%s: got %q, want %q", tag, data, want)
		}
	}
}

func TestSiteDeploymentRunsAfterAnyChangedRepositoryUpdate(t *testing.T) {
	root, _ := releaseFixture(t)
	data, err := os.ReadFile(filepath.Join(root, ".github/workflows/site.yml"))
	if err != nil {
		t.Fatal(err)
	}
	siteWorkflow := string(data)
	for _, required := range []string{
		"repository_dispatch:",
		"types: [package-repository-updated]",
		"if: startsWith(github.event.client_payload.tag, 'v')",
	} {
		if !strings.Contains(siteWorkflow, required) {
			t.Errorf("site workflow missing package repository dispatch condition %q", required)
		}
	}
	for _, forbidden := range []string{"\n  push:", "\n  workflow_dispatch:"} {
		if strings.Contains(siteWorkflow, forbidden) {
			t.Errorf("site workflow still has non-release trigger %q", strings.TrimSpace(forbidden))
		}
	}

	repositoryStep := workflowStep(t, root, "apt.yml", "Commit and push package repositories")
	if !strings.Contains(repositoryStep, `echo "changed=false" >> "$GITHUB_OUTPUT"`) ||
		!strings.Contains(repositoryStep, `echo "changed=true" >> "$GITHUB_OUTPUT"`) {
		t.Fatal("package repository step does not report whether it updated files")
	}
	dispatchStep := workflowStep(t, root, "apt.yml", "Dispatch package repository site update")
	if !strings.Contains(dispatchStep, "event_type=package-repository-updated") ||
		!strings.Contains(dispatchStep, `client_payload[tag]=${RELEASE_TAG}`) {
		t.Fatal("package repository update does not dispatch the site workflow with the release tag")
	}
	aptData, err := os.ReadFile(filepath.Join(root, ".github/workflows/apt.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(aptData), "if: steps.repository_update.outputs.changed == 'true'") {
		t.Fatal("site deployment is not gated on a changed package repository")
	}
}

func TestReleaseWorkflowUploadsOnlyInstallablePackages(t *testing.T) {
	root, _ := releaseFixture(t)
	data, err := os.ReadFile(filepath.Join(root, ".github/workflows/release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	for _, upload := range []struct {
		name  string
		files []string
	}{
		{
			name: "macOS",
			files: []string{
				".tar.gz", ".tar.gz.sha256",
			},
		},
		{
			name: "Linux",
			files: []string{
				".deb", ".deb.sha256", ".rpm", ".rpm.sha256", ".tar.gz", ".tar.gz.sha256",
			},
		},
		{
			name:  "FreeBSD",
			files: []string{".tar.gz", ".tar.gz.sha256"},
		},
	} {
		step := "- name: Upload " + upload.name + " artifacts\n"
		_, section, found := strings.Cut(workflow, step)
		if !found {
			t.Fatalf("missing %s upload step", upload.name)
		}
		section, _, _ = strings.Cut(section, "\n      - name:")
		for _, suffix := range upload.files {
			osName := map[string]string{
				"macOS":   "darwin",
				"Linux":   "linux",
				"FreeBSD": "freebsd",
			}[upload.name]
			arches := []string{"${{ matrix.arch }}"}
			if upload.name == "macOS" {
				arches = []string{"amd64", "arm64"}
			}
			for _, arch := range arches {
				if !strings.Contains(section, "${{ github.ref_name }}-"+osName+"-"+arch+suffix) {
					t.Errorf("%s upload omits %s artifact suffix %s", upload.name, arch, suffix)
				}
			}
		}
		if strings.Contains(section, "dist/release/*") {
			t.Errorf("%s upload uses a broad glob that can include loose build outputs", upload.name)
		}
	}
}

func TestMacReleaseWorkflowCrossCompilesOnLinux(t *testing.T) {
	root, _ := releaseFixture(t)
	data, err := os.ReadFile(filepath.Join(root, ".github/workflows/release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	_, job, found := strings.Cut(workflow, "  package-macos:\n")
	if !found {
		t.Fatal("missing macOS packaging job")
	}
	job, _, _ = strings.Cut(job, "  package-linux:\n")
	for _, required := range []string{
		"runs-on: ubuntu-latest",
		`RELEASE_TARGET_ARCHES="amd64 arm64"`,
		"RELEASE_MACOS_PKG=0",
		"Validate cross-compiled macOS tarballs",
	} {
		if !strings.Contains(job, required) {
			t.Errorf("macOS packaging job is missing %q", required)
		}
	}
	for _, forbidden := range []string{"macos-14", "macos-15-intel", "rustup", "cargo", `"${binary}" --help`} {
		if strings.Contains(job, forbidden) {
			t.Errorf("macOS cross-compilation job contains %q", forbidden)
		}
	}
}

func TestMacTarballValidation(t *testing.T) {
	root, dir := releaseFixture(t)
	tag := "v1.2.0"
	for _, arch := range []string{"amd64", "arm64"} {
		payload := filepath.Join(dir, "payload", arch)
		for _, name := range []string{"ash-" + tag + "-darwin-" + arch, "ash-broker", "plugins/what_time_is_it", "plugins/calculator"} {
			writeFixture(t, filepath.Join(payload, name), "fixture", 0o755)
		}
		runFixture(t, dir, nil, "mkdir", "-p", "dist/release")
		runFixture(t, dir, nil, "tar", "-czf", "dist/release/ash-"+tag+"-darwin-"+arch+".tar.gz", "-C", payload, ".")
	}
	writeFixture(t, filepath.Join(dir, "bin/file"), `#!/bin/bash
case "$1" in
  *amd64*) cpu=x86_64 ;;
  *arm64*) cpu=arm64 ;;
  *) exit 1 ;;
esac
if [[ "$TEST_WRONG_ARCH" == yes ]]; then cpu=unknown; fi
printf 'Mach-O 64-bit %s executable\n' "$cpu"
`, 0o755)
	env := []string{"PATH=" + filepath.Join(dir, "bin") + ":" + os.Getenv("PATH"), "GITHUB_REF_NAME=" + tag}
	script := workflowStep(t, root, "release.yml", "Validate cross-compiled macOS tarballs")
	runFixture(t, dir, env, "bash", "-c", script)
	if output, err := fixtureCommand(dir, append(env, "TEST_WRONG_ARCH=yes"), "bash", "-c", script).CombinedOutput(); err == nil {
		t.Fatalf("validation accepted the wrong Mach-O architecture:\n%s", output)
	}
	writeFixture(t, filepath.Join(dir, "smoke/arm64/plugins/flip_a_coin"), "stale", 0o755)
	if output, err := fixtureCommand(dir, env, "bash", "-c", script).CombinedOutput(); err == nil {
		t.Fatalf("validation accepted a Rust plugin in the macOS payload:\n%s", output)
	}
}

func TestDebianRepositoryUsesArchitectureFilenameSuffix(t *testing.T) {
	root, dir := releaseFixture(t)
	script := workflowStep(t, root, "apt.yml", "Import signing key and update repositories")
	start := `for package in "$RUNNER_TEMP"/debs/*.deb; do`
	_, staging, found := strings.Cut(script, start)
	if !found {
		t.Fatal("missing Debian staging loop")
	}
	staging, _, found = strings.Cut(staging, "\ninstall -m 0644")
	if !found {
		t.Fatal("missing end of Debian staging loop")
	}
	staging = start + staging
	writeFixture(t, filepath.Join(dir, "bin/dpkg-deb"), `#!/bin/bash
case "$2" in
  *-amd64.deb) echo amd64 ;;
  *-arm64.deb) echo arm64 ;;
  *) exit 1 ;;
esac
`, 0o755)
	for _, arch := range []string{"amd64", "arm64"} {
		writeFixture(t, filepath.Join(dir, "runner/debs/ash-v1.2.0-nightly.20261011.1-linux-"+arch+".deb"), arch, 0o644)
	}
	if err := os.MkdirAll(filepath.Join(dir, "site/apt/pool/nightly/main"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"PATH=" + filepath.Join(dir, "bin") + ":" + os.Getenv("PATH"),
		"RUNNER_TEMP=" + filepath.Join(dir, "runner"), "PACKAGE_CHANNEL=nightly", "PACKAGE_VERSION=1.2.0~nightly.20261011.1",
	}
	runFixture(t, dir, env, "bash", "-euo", "pipefail", "-c", staging)
	for _, arch := range []string{"amd64", "arm64"} {
		path := filepath.Join(dir, "site/apt/pool/nightly/main/ash_1.2.0~nightly.20261011.1_"+arch+".deb")
		data, err := os.ReadFile(path)
		if err != nil || string(data) != arch {
			t.Fatalf("invalid Debian repository file %s: %q, %v", path, data, err)
		}
	}
}

func TestArchRepositorySignsMainPackage(t *testing.T) {
	root, dir := releaseFixture(t)
	script := workflowStep(t, root, "apt.yml", "Import signing key and update repositories")
	_, signing, found := strings.Cut(script, "pacman_package=")
	if !found {
		t.Fatal("missing Arch package signing")
	}
	signing, _, found = strings.Cut(signing, "\ndocker run")
	if !found {
		t.Fatal("missing end of Arch package signing")
	}
	signing = "pacman_package=" + signing
	writeFixture(t, filepath.Join(dir, "bin/gpg"), `#!/bin/bash
while [[ $# -gt 0 ]]; do
  if [[ "$1" == --output ]]; then output="$2"; shift; fi
  input="$1"
  shift
done
cp "$input" "$output"
`, 0o755)
	archive := "ash-1.2.0.nightly.20261011.1-1-x86_64.pkg.tar.zst"
	writeFixture(t, filepath.Join(dir, "repo/ash-debug-1.2.0.nightly.20261011.1-1-x86_64.pkg.tar.zst"), "debug", 0o644)
	writeFixture(t, filepath.Join(dir, "repo", archive), "main", 0o644)
	env := []string{
		"PATH=" + filepath.Join(dir, "bin") + ":" + os.Getenv("PATH"),
		"pacman_repo=repo", "ARCH_VERSION=1.2.0.nightly.20261011.1", "key_id=fixture", "GPG_PASSPHRASE=fixture",
	}
	runFixture(t, dir, env, "bash", "-euo", "pipefail", "-c", signing)
	data, err := os.ReadFile(filepath.Join(dir, "repo", archive+".sig"))
	if err != nil || string(data) != "main" {
		t.Fatalf("main Arch package was not signed: %q, %v", data, err)
	}
}

func TestReleasePublishingToIsolatedRemote(t *testing.T) {
	for _, channel := range []string{"beta", "stable", "nightly"} {
		t.Run(channel, func(t *testing.T) {
			_, dir := releaseFixture(t)
			remote := filepath.Join(t.TempDir(), "origin.git")
			runFixture(t, dir, nil, "git", "init", "--bare", remote)
			runFixture(t, dir, nil, "git", "remote", "add", "origin", remote)
			version := strings.TrimSpace(runFixture(t, dir, nil, "bash", "scripts/release/next_version.sh", channel))
			writeFixture(t, filepath.Join(dir, "dist/release/release-notes.md"), "## Features\n- Fixture\n\n## Fixes & Improvements\n- Fixture\n", 0o644)
			runFixture(t, dir, nil, "make", "release-publish", "CHANNEL="+channel, "RELEASE_VERSION="+version)
			branch := "main"
			if channel == "stable" {
				branch = "release/v0.1"
			}
			head := strings.TrimSpace(runFixture(t, dir, nil, "git", "rev-parse", "HEAD"))
			for _, ref := range []string{"refs/heads/" + branch, "refs/tags/" + version + "^{}"} {
				got := strings.TrimSpace(runFixture(t, dir, nil, "git", "--git-dir="+remote, "rev-parse", ref))
				if got != head {
					t.Fatalf("%s points to %s, want %s", ref, got, head)
				}
			}
			if got := strings.TrimSpace(runFixture(t, dir, nil, "git", "cat-file", "-t", "refs/tags/"+version)); got != "tag" {
				t.Fatalf("release tag is not annotated: %s", got)
			}
		})
	}
}

func TestReleaseTranslationDoesNotModifyTrackedCache(t *testing.T) {
	_, dir := releaseFixture(t)
	cache := `{"version":1,"translations":{}}`
	writeFixture(t, filepath.Join(dir, ".site-translation-cache.json"), cache, 0o644)
	writeFixture(t, filepath.Join(dir, "site/index.html"), "fixture", 0o644)
	writeFixture(t, filepath.Join(dir, "internal/localize/languages/en_US.json"), "{}", 0o644)
	writeFixture(t, filepath.Join(dir, "site/generate_locales.py"), `
from pathlib import Path
Path(".site-translation-cache.json").write_text("updated")
Path("site/fr").mkdir()
Path("site/fr/index.html").write_text("translated")
`, 0o644)
	runFixture(t, dir, nil, "git", "add", ".")
	runFixture(t, dir, nil, "git", "commit", "-m", "translation fixture")
	runFixture(t, dir, nil, "make", "release-translate", "RELEASE_VERSION=v1.2.0-beta.1")
	data, err := os.ReadFile(filepath.Join(dir, ".site-translation-cache.json"))
	if err != nil || string(data) != cache {
		t.Fatalf("tracked cache changed: %q, %v", data, err)
	}
	data, err = os.ReadFile(filepath.Join(dir, ".cache/ash/site-translation-cache.json"))
	if err != nil || string(data) != "updated" {
		t.Fatalf("runtime cache not saved: %q, %v", data, err)
	}
	if output := runFixture(t, dir, nil, "git", "status", "--porcelain"); output != "" {
		t.Fatalf("translation dirtied release checkout: %s", output)
	}
}

func TestFailedBranchPushStopsBeforeTagging(t *testing.T) {
	_, dir := releaseFixture(t)
	writeFixture(t, filepath.Join(dir, "scripts/release/push_with_retry.sh"), "#!/bin/bash\nexit 42\n", 0o755)
	runFixture(t, dir, nil, "git", "add", ".")
	runFixture(t, dir, nil, "git", "commit", "-m", "failing push fixture")
	writeFixture(t, filepath.Join(dir, "dist/release/release-notes.md"), "fixture notes", 0o644)
	if output, err := fixtureCommand(dir, nil, "make", "release-publish", "RELEASE_VERSION=v1.2.0-beta.1").CombinedOutput(); err == nil {
		t.Fatalf("failed branch push returned success: %s", output)
	}
	if err := fixtureCommand(dir, nil, "git", "show-ref", "--verify", "--quiet", "refs/tags/v1.2.0-beta.1").Run(); err == nil {
		t.Fatal("tag created after branch push failure")
	}
}

func TestArtifactBuildStopsOnFailure(t *testing.T) {
	_, dir := releaseFixture(t)
	writeFixture(t, filepath.Join(dir, "steps.mk"), `
release-build-one:
	@exit 42
release-pkg-one:
	@touch unexpected-package
release-validate-one:
	@touch unexpected-validation
`, 0o644)
	writeFixture(t, filepath.Join(dir, "invoke.mk"), `
MAKE := $(MAKE) -f Makefile -f steps.mk
`, 0o644)
	cmd := fixtureCommand(dir, nil, "make", "-f", "Makefile", "-f", "steps.mk", "-f", "invoke.mk", "release-artifacts", "RELEASE_TARGET_OSES=darwin", "RELEASE_TARGET_ARCHES=arm64")
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("failed build returned success: %s", output)
	}
	for _, name := range []string{"unexpected-package", "unexpected-validation"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s ran after build failure: %v", name, err)
		}
	}
}

func TestMacReleaseCanSkipPkg(t *testing.T) {
	_, dir := releaseFixture(t)
	writeFixture(t, filepath.Join(dir, "steps.mk"), `
release-build-one:
	@echo "build $(RELEASE_GOOS) $(RELEASE_ARCH)"
release-pkg-one:
	@echo "package $(RELEASE_FORMAT)"
release-validate-one:
	@echo "validate $(RELEASE_FORMAT)"
`, 0o644)
	writeFixture(t, filepath.Join(dir, "invoke.mk"), `
MAKE := $(MAKE) -f Makefile -f steps.mk
`, 0o644)
	for _, skip := range []bool{false, true} {
		args := []string{"-f", "Makefile", "-f", "steps.mk", "-f", "invoke.mk",
			"release-artifacts", "RELEASE_TARGET_OSES=darwin", "RELEASE_TARGET_ARCHES=arm64",
			"RELEASE_VERSION=v1.2.0"}
		if skip {
			args = append(args, "RELEASE_MACOS_PKG=0")
		}
		output := runFixture(t, dir, nil, "make", args...)
		for _, action := range []string{"package", "validate"} {
			if strings.Contains(output, action+" pkg") == skip {
				t.Fatalf("unexpected macOS pkg %s with skip=%t:\n%s", action, skip, output)
			}
			if !strings.Contains(output, action+" tar.gz") {
				t.Fatalf("macOS release tarball missing %s:\n%s", action, output)
			}
		}
	}
}

func TestPluginBuildExcludesRustOnlyForMacOS(t *testing.T) {
	root, dir := releaseFixture(t)
	copyFixture(t, root, dir, "scripts/dev/run-quiet.sh")
	for _, name := range []string{"rust_fixture", "go_fixture"} {
		writeFixture(t, filepath.Join(dir, "plugins", name, "Makefile"), `
build:
	@mkdir -p "$(BUILD_DIR)"
	@touch "$(BUILD_DIR)/$(notdir $(CURDIR))"
`, 0o644)
	}
	writeFixture(t, filepath.Join(dir, "plugins/rust_fixture/Cargo.toml"), "[package]\n", 0o644)
	writeFixture(t, filepath.Join(dir, "bin/uname"), "#!/bin/sh\nprintf 'Darwin\\n'\n", 0o755)
	env := []string{"PATH=" + filepath.Join(dir, "bin") + ":" + os.Getenv("PATH")}
	for _, target := range []string{"darwin", "linux", "freebsd", ""} {
		t.Run("target="+target, func(t *testing.T) {
			outputDir := t.TempDir()
			// A reused output directory must not retain an old Rust executable.
			writeFixture(t, filepath.Join(outputDir, "rust_fixture"), "stale", 0o755)
			runFixture(t, dir, env, "make", "plugins-build", "PLUGINS_SRC_DIR=plugins",
				"PLUGINS_BIN_DIR="+outputDir, "PLUGIN_TARGET_GOOS="+target)
			if _, err := os.Stat(filepath.Join(outputDir, "go_fixture")); err != nil {
				t.Fatal(err)
			}
			_, err := os.Stat(filepath.Join(outputDir, "rust_fixture"))
			if target == "darwin" || target == "" {
				if !os.IsNotExist(err) {
					t.Fatalf("macOS build retained Rust plugin: %v", err)
				}
			} else if err != nil {
				t.Fatalf("%s build omitted Rust plugin: %v", target, err)
			}
		})
	}
}

func TestLinuxPackageVersionMapping(t *testing.T) {
	root, dir := releaseFixture(t)
	for _, name := range []string{"scripts/package/linux/build_deb.sh", "scripts/package/linux/build_rpm.sh"} {
		copyFixture(t, root, dir, name)
	}
	for _, name := range []string{"ash", "ash-broker", "plugins/fixture", "LICENSE", "THIRD_PARTY_NOTICES.md", "ash.1"} {
		writeFixture(t, filepath.Join(dir, "payload", name), "fixture", 0o755)
	}
	writeFixture(t, filepath.Join(dir, "bin/fpm"), `#!/bin/bash
args=("$@")
while [[ $# -gt 0 ]]; do
  if [[ "$1" == "-C" ]]; then
    payload="$2"
    shift 2
  else
    shift
  fi
done
test -f "$payload/usr/share/licenses/ash/THIRD_PARTY_NOTICES.md"
printf 'THIRD_PARTY_NOTICES_INCLUDED\n'
printf '%s\n' "${args[@]}"
`, 0o755)
	versions := map[string]string{
		"v1.2.0":                    "1.2.0",
		"v1.2.0-beta.1":             "1.2.0~beta.1",
		"v1.2.0-nightly.20261011":   "1.2.0~nightly.20261011",
		"v1.2.0-nightly.20261011.1": "1.2.0~nightly.20261011.1",
	}
	for version, want := range versions {
		for _, format := range []string{"deb", "rpm"} {
			env := []string{"PATH=" + filepath.Join(dir, "bin") + ":" + os.Getenv("PATH")}
			output := runFixture(t, dir, env, "bash", "scripts/package/linux/build_"+format+".sh",
				"--app-name", "ash", "--version", version, "--arch", "arm64",
				"--binary", "payload/ash", "--broker", "payload/ash-broker",
				"--plugins-dir", "payload/plugins", "--license-file", "payload/LICENSE",
				"--third-party-notices-file", "payload/THIRD_PARTY_NOTICES.md",
				"--install-path", "/usr/bin", "--plugin-install-path", "/usr/lib/ash/plugins",
				"--license-install-path", "/usr/share/licenses/ash/LICENSE",
				"--man-page", "payload/ash.1", "--man-install-path", "/usr/share/man/man1",
				"--output", "dist/package."+format)
			if !strings.Contains(output, "-v\n"+want+"\n") {
				t.Fatalf("%s %s has incorrect package version: %s", format, version, output)
			}
			if !strings.Contains(output, "THIRD_PARTY_NOTICES_INCLUDED") {
				t.Fatalf("%s %s omitted third-party notices", format, version)
			}
		}
	}
}

func TestMacPackageIncludesThirdPartyNotices(t *testing.T) {
	root, dir := releaseFixture(t)
	copyFixture(t, root, dir, "scripts/package/macos/build_pkg.sh")
	for _, name := range []string{"ash", "ash-broker", "plugins/fixture", "LICENSE", "THIRD_PARTY_NOTICES.md", "ash.1"} {
		writeFixture(t, filepath.Join(dir, "payload", name), "fixture", 0o755)
	}
	writeFixture(t, filepath.Join(dir, "bin/pkgbuild"), `#!/bin/bash
set -e
while [[ $# -gt 0 ]]; do
  case "$1" in
    --root) payload="$2"; shift 2 ;;
    *) output="$1"; shift ;;
  esac
done
test -f "$payload/usr/local/share/licenses/ash/THIRD_PARTY_NOTICES.md"
touch "$output"
`, 0o755)
	env := []string{"PATH=" + filepath.Join(dir, "bin") + ":" + os.Getenv("PATH")}
	runFixture(t, dir, env, "bash", "scripts/package/macos/build_pkg.sh",
		"--app-name", "ash", "--version", "v1.2.0", "--binary", "payload/ash",
		"--broker", "payload/ash-broker", "--plugins-dir", "payload/plugins",
		"--license-file", "payload/LICENSE", "--third-party-notices-file", "payload/THIRD_PARTY_NOTICES.md",
		"--install-path", "/usr/local/bin", "--plugin-install-path", "/usr/local/lib/ash/plugins",
		"--license-install-path", "/usr/local/share/licenses/ash/LICENSE",
		"--man-page", "payload/ash.1", "--man-install-path", "/usr/local/share/man/man1",
		"--output", "dist/ash.pkg")
	if _, err := os.Stat(filepath.Join(dir, "dist/ash.pkg")); err != nil {
		t.Fatal(err)
	}
}

func TestTarballIncludesThirdPartyNotices(t *testing.T) {
	_, dir := releaseFixture(t)
	writeFixture(t, filepath.Join(dir, "LICENSE"), "license", 0o644)
	writeFixture(t, filepath.Join(dir, "THIRD_PARTY_NOTICES.md"), "notices", 0o644)
	for _, name := range []string{"ash", "ash-broker", "ash-v1.2.0-linux-amd64-broker", "ash.1", "LICENSE", "THIRD_PARTY_NOTICES.md", "plugins/fixture"} {
		writeFixture(t, filepath.Join(dir, "payload", name), "fixture", 0o755)
	}
	runFixture(t, dir, nil, "make", "release-pkg-one",
		"RELEASE_FORMAT=tar.gz", "RELEASE_VERSION=v1.2.0",
		"RELEASE_OUTPUT_DIR=payload", "RELEASE_PACKAGE_DIR=dist",
		"RELEASE_BINARY_PATH=payload/ash", "RELEASE_ARTIFACT_BASE=ash-v1.2.0-linux-amd64",
		"RELEASE_PLUGINS_BIN_DIR=payload/plugins", "MAN_PAGE_PATH=payload/ash.1")
	entries := runFixture(t, dir, nil, "tar", "-tzf", "dist/ash-v1.2.0-linux-amd64.tar.gz")
	if !strings.Contains(entries, "THIRD_PARTY_NOTICES.md") {
		t.Fatalf("tarball omitted third-party notices: %s", entries)
	}
}

func TestArchPrereleaseUsesOriginalReleaseBinaryName(t *testing.T) {
	root, dir := releaseFixture(t)
	copyFixture(t, root, dir, "scripts/package/arch/PKGBUILD")
	for _, channel := range []string{"beta.1", "nightly.20261011", "nightly.20261011.1"} {
		t.Run(channel, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "src")
			pkg := filepath.Join(t.TempDir(), "pkg")
			tag := "v1.2.0-" + channel
			for _, file := range []string{"ash-" + tag + "-linux-amd64", "ash-broker", "plugins/fixture", "LICENSE", "THIRD_PARTY_NOTICES.md", "usr/share/man/man1/ash.1"} {
				writeFixture(t, filepath.Join(src, file), "fixture", 0o755)
			}
			env := []string{"ASH_VERSION=1.2.0." + channel, "ASH_RELEASE_TAG=" + tag, "ASH_ARCHIVE_SHA256=fixture", "srcdir=" + src, "pkgdir=" + pkg}
			runFixture(t, dir, env, "bash", "-eu", "-c", `
install() {
  if [[ "$1" == -d ]]; then mkdir -p "$2"; return; fi
  shift
  if [[ "$*" == *" -t "* ]]; then
    while [[ "$1" != -t ]]; do test -f "$1"; shift; done
    mkdir -p "$2"
  else
    test -f "$1"
    mkdir -p "$(dirname "$2")"
    cp "$1" "$2"
  fi
}
source scripts/package/arch/PKGBUILD
package
`)
			if _, err := os.Stat(filepath.Join(pkg, "usr/bin/ash")); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(pkg, "usr/share/licenses/ash/THIRD_PARTY_NOTICES.md")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDashboardRequiresSuccessfulWorkflow(t *testing.T) {
	root, dir := releaseFixture(t)
	copyFixture(t, root, dir, "scripts/release/release_dashboard.sh")
	tag := "v1.2.0-beta.1"
	runFixture(t, dir, nil, "git", "tag", tag)
	commit := strings.TrimSpace(runFixture(t, dir, nil, "git", "rev-parse", tag))
	var assets []map[string]string
	for _, target := range []string{"darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64", "freebsd-amd64"} {
		formats := []string{"tar.gz"}
		if strings.HasPrefix(target, "linux") {
			formats = append(formats, "deb", "rpm")
		}

		for _, format := range formats {
			name := "ash-" + tag + "-" + target + "." + format
			assets = append(assets, map[string]string{"name": name}, map[string]string{"name": name + ".sha256"})
		}
	}
	for _, name := range []string{"SHA256SUMS", "SHA256SUMS.sigstore.json", "ash-site-" + tag + ".tar.gz", "sbom.spdx.json", "THIRD_PARTY_NOTICES.md", "provenance.sigstore.json"} {
		assets = append(assets, map[string]string{"name": name})
	}
	data, err := json.Marshal(map[string]any{"assets": assets})
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(dir, "assets.json"), string(data), 0o644)
	writeFixture(t, filepath.Join(dir, "bin/gh"), `#!/bin/bash
case "$1 $2" in
  "run list") [[ "$*" == *"--commit $TEST_EXPECTED_COMMIT"* ]] || exit 1; echo 123 ;;
  "run view") printf '{"databaseId":123,"status":"completed","conclusion":"%s","jobs":[]}' "$TEST_CONCLUSION" ;;
  "release view") cat assets.json ;;
  *) exit 1 ;;
esac
`, 0o755)
	for _, conclusion := range []string{"success", "failure"} {
		env := []string{"PATH=" + filepath.Join(dir, "bin") + ":" + os.Getenv("PATH"), "TEST_CONCLUSION=" + conclusion, "TEST_EXPECTED_COMMIT=" + commit}
		output, err := fixtureCommand(dir, env, "/bin/bash", "scripts/release/release_dashboard.sh", "--tag", tag, "--repo", "fixture/repo", "--once").CombinedOutput()
		if (err == nil) != (conclusion == "success") {
			t.Fatalf("dashboard conclusion=%s: %v\n%s", conclusion, err, output)
		}
		if !strings.Contains(string(output), "Run: 123") {
			t.Fatalf("dashboard did not match the tag commit: %s", output)
		}
	}
}

func TestHomebrewPublishingInitializesEmptyTap(t *testing.T) {
	root, dir := releaseFixture(t)
	copyFixture(t, root, dir, "scripts/release/publish_homebrew_tap.sh")
	remote := filepath.Join(t.TempDir(), "tap.git")
	runFixture(t, dir, nil, "git", "init", "--bare", "--initial-branch=main", remote)
	writeFixture(t, filepath.Join(dir, ".gitconfig"),
		"[url \"file://"+remote+"\"]\n\tinsteadOf = https://github.com/Jonconradt/homebrew-ash.git\n", 0o644)
	writeFixture(t, filepath.Join(dir, "bin/curl"), `#!/bin/bash
while [[ $# -gt 0 ]]; do
  if [[ "$1" == --output ]]; then printf '{}' > "$2"; shift; fi
  shift
done
printf '200'
`, 0o755)
	env := []string{"PATH=" + filepath.Join(dir, "bin") + ":" + os.Getenv("PATH"), "HOMEBREW_TAP_GITHUB_TOKEN=fixture"}
	versions := map[string]string{
		"v1.2.0":                    "ash",
		"v1.2.0-beta.1":             "ash-beta",
		"v1.2.0-nightly.20261011":   "ash-nightly",
		"v1.2.0-nightly.20261011.1": "ash-nightly",
	}
	for tag, formula := range versions {
		var sums strings.Builder
		for _, target := range []string{"darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64"} {
			sums.WriteString(strings.Repeat("a", 64) + "  ash-" + tag + "-" + target + ".tar.gz\n")
		}
		writeFixture(t, filepath.Join(dir, "SHA256SUMS"), sums.String(), 0o644)
		runFixture(t, dir, env, "bash", "scripts/release/publish_homebrew_tap.sh", tag, "SHA256SUMS")
		content := runFixture(t, dir, nil, "git", "--git-dir="+remote, "show", "main:Formula/"+formula+".rb")
		if !strings.Contains(content, `version "`+strings.TrimPrefix(tag, "v")+`"`) {
			t.Fatalf("unexpected %s formula:\n%s", formula, content)
		}
		if !strings.Contains(content, `install "THIRD_PARTY_NOTICES.md"`) {
			t.Fatalf("%s formula does not install third-party notices:\n%s", formula, content)
		}
	}
}
