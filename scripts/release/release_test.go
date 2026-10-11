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
				"release-check: $(if $(filter stable,$(CHANNEL)),verify,lint test gosec govulncheck)",
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
			if count != 9 {
				t.Fatalf("ran %d release steps, want 9\n%s", count, output)
			}
		})
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
	for _, name := range []string{"ash", "ash-broker", "plugins/fixture", "LICENSE", "ash.1"} {
		writeFixture(t, filepath.Join(dir, "payload", name), "fixture", 0o755)
	}
	writeFixture(t, filepath.Join(dir, "bin/fpm"), "#!/bin/bash\nprintf '%s\\n' \"$@\"\n", 0o755)
	versions := map[string]string{
		"v1.2.0":                  "1.2.0",
		"v1.2.0-beta.1":           "1.2.0~beta.1",
		"v1.2.0-nightly.20261011": "1.2.0~nightly.20261011",
	}
	for version, want := range versions {
		for _, format := range []string{"deb", "rpm"} {
			env := []string{"PATH=" + filepath.Join(dir, "bin") + ":" + os.Getenv("PATH")}
			output := runFixture(t, dir, env, "bash", "scripts/package/linux/build_"+format+".sh",
				"--app-name", "ash", "--version", version, "--arch", "arm64",
				"--binary", "payload/ash", "--broker", "payload/ash-broker",
				"--plugins-dir", "payload/plugins", "--license-file", "payload/LICENSE",
				"--install-path", "/usr/bin", "--plugin-install-path", "/usr/lib/ash/plugins",
				"--license-install-path", "/usr/share/licenses/ash/LICENSE",
				"--man-page", "payload/ash.1", "--man-install-path", "/usr/share/man/man1",
				"--output", "dist/package."+format)
			if !strings.Contains(output, "-v\n"+want+"\n") {
				t.Fatalf("%s %s has incorrect package version: %s", format, version, output)
			}
		}
	}
}

func TestArchPrereleaseUsesOriginalReleaseBinaryName(t *testing.T) {
	root, dir := releaseFixture(t)
	copyFixture(t, root, dir, "scripts/package/arch/PKGBUILD")
	for _, channel := range []string{"beta.1", "nightly.20261011"} {
		t.Run(channel, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "src")
			pkg := filepath.Join(t.TempDir(), "pkg")
			tag := "v1.2.0-" + channel
			for _, file := range []string{"ash-" + tag + "-linux-amd64", "ash-broker", "plugins/fixture", "LICENSE", "usr/share/man/man1/ash.1"} {
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
		})
	}
}

func TestDashboardRequiresSuccessfulWorkflow(t *testing.T) {
	root, dir := releaseFixture(t)
	copyFixture(t, root, dir, "scripts/release/release_dashboard.sh")
	tag := "v1.2.0-beta.1"
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
	for _, name := range []string{"SHA256SUMS", "SHA256SUMS.sigstore.json", "ash-site-" + tag + ".tar.gz", "sbom.spdx.json", "provenance.sigstore.json"} {
		assets = append(assets, map[string]string{"name": name})
	}
	data, err := json.Marshal(map[string]any{"assets": assets})
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(dir, "assets.json"), string(data), 0o644)
	writeFixture(t, filepath.Join(dir, "bin/gh"), `#!/bin/bash
case "$1 $2" in
  "run list") echo 123 ;;
  "run view") printf '{"status":"completed","conclusion":"%s","jobs":[]}' "$TEST_CONCLUSION" ;;
  "release view") cat assets.json ;;
  *) exit 1 ;;
esac
`, 0o755)
	for _, conclusion := range []string{"success", "failure"} {
		env := []string{"PATH=" + filepath.Join(dir, "bin") + ":" + os.Getenv("PATH"), "TEST_CONCLUSION=" + conclusion}
		output, err := fixtureCommand(dir, env, "/bin/bash", "scripts/release/release_dashboard.sh", "--tag", tag, "--repo", "fixture/repo", "--once").CombinedOutput()
		if (err == nil) != (conclusion == "success") {
			t.Fatalf("dashboard conclusion=%s: %v\n%s", conclusion, err, output)
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
		"v1.2.0":                  "ash",
		"v1.2.0-beta.1":           "ash-beta",
		"v1.2.0-nightly.20261011": "ash-nightly",
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
	}
}
