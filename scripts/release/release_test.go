// Copyright (c) 2026 Jonathan L. Conradt
// SPDX-License-Identifier: MIT

package release

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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
			for _, line := range strings.Split(output, "\n") {
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

func TestVerifyCIOmitsSlowChecks(t *testing.T) {
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
	for _, skipped := range []string{"test-race", "test-cover", "vet", "staticcheck", "secrets-history", "secrets-tree", "test-fuzz", "benchmark"} {
		if strings.Contains(section, `"`+skipped+`"`) {
			t.Errorf("verify-ci unexpectedly runs %s", skipped)
		}
	}
	for _, retained := range []string{`"lint"`, `"test"`, `"security"`} {
		if !strings.Contains(section, retained) {
			t.Errorf("verify-ci omits expected check %s", retained)
		}
	}
	if !strings.Contains(string(data), `"test-race"`) || !strings.Contains(string(data), `"benchmark"`) {
		t.Fatal("full verify target lost checks when adding verify-ci")
	}
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

func TestExternalGoInstallsDoNotDependOnTagIndexing(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".github/workflows/release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	_, step, found := strings.Cut(string(data), "- name: Verify external Go installs from the release tag\n")
	if !found {
		t.Fatal("missing external Go install check")
	}
	step, _, _ = strings.Cut(step, "      - name:")
	for _, required := range []string{
		"          GONOPROXY: github.com/Jonconradt/ash\n",
		"          GONOSUMDB: github.com/Jonconradt/ash\n",
		`export GOBIN="$install_dir/bin"`,
		`export GOMODCACHE="$GOPATH/pkg/mod"`,
		`go install "github.com/Jonconradt/ash/cmd/ash@${GITHUB_REF_NAME}"`,
		`go install "github.com/Jonconradt/ash/cmd/ash-broker@${GITHUB_REF_NAME}"`,
		`"$GOBIN/ash" --help`,
		`"$GOBIN/ash-broker" --version`,
	} {
		if !strings.Contains(step, required) {
			t.Errorf("external Go install check missing %q", required)
		}
	}
	for _, forbidden := range []string{"GOSUMDB:", "GOPRIVATE:", "GOPROXY:"} {
		if strings.Contains(step, forbidden) {
			t.Errorf("external Go install check overrides dependency verification with %s", forbidden)
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

func TestSiteDeploymentOnlyRunsAfterStableRepositoryUpdate(t *testing.T) {
	root, _ := releaseFixture(t)
	data, err := os.ReadFile(filepath.Join(root, ".github/workflows/site.yml"))
	if err != nil {
		t.Fatal(err)
	}
	siteWorkflow := string(data)
	for _, required := range []string{
		"repository_dispatch:",
		"types: [stable-release-updated]",
		"if: startsWith(github.event.client_payload.tag, 'v') && !contains(github.event.client_payload.tag, '-')",
	} {
		if !strings.Contains(siteWorkflow, required) {
			t.Errorf("site workflow missing stable-release condition %q", required)
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
	dispatchStep := workflowStep(t, root, "apt.yml", "Dispatch stable site update")
	if !strings.Contains(dispatchStep, "event_type=stable-release-updated") ||
		!strings.Contains(dispatchStep, `client_payload[tag]=${RELEASE_TAG}`) {
		t.Fatal("stable repository update does not dispatch the site workflow with the release tag")
	}
	aptData, err := os.ReadFile(filepath.Join(root, ".github/workflows/apt.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(aptData), "if: startsWith(env.RELEASE_TAG, 'v') && !contains(env.RELEASE_TAG, '-') && steps.repository_update.outputs.changed == 'true'") {
		t.Fatal("site deployment is not gated on a changed stable repository")
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
				".pkg", ".pkg.sha256", ".tar.gz", ".tar.gz.sha256",
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
			if !strings.Contains(section, "${{ github.ref_name }}-"+map[string]string{
				"macOS":   "darwin",
				"Linux":   "linux",
				"FreeBSD": "freebsd",
			}[upload.name]+"-${{ matrix.arch }}"+suffix) {
				t.Errorf("%s upload omits expected artifact suffix %s", upload.name, suffix)
			}
		}
		if strings.Contains(section, "dist/release/*") {
			t.Errorf("%s upload uses a broad glob that can include loose build outputs", upload.name)
		}
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
		switch {
		case strings.HasPrefix(target, "darwin"):
			formats = append(formats, "pkg")
		case strings.HasPrefix(target, "linux"):
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
