// Copyright (c) 2026 Jonathan L. Conradt
// SPDX-License-Identifier: MIT

package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseUpgradeArgs(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		version        string
		replace        bool
		skipCustomized bool
		wantErr        bool
	}{
		{name: "defaults"},
		{name: "version", args: []string{"--version", "v1.2.3"}, version: "v1.2.3"},
		{name: "replace", args: []string{"--yes"}, replace: true},
		{name: "skip", args: []string{"--skip-customized"}, skipCustomized: true},
		{name: "missing version", args: []string{"--version"}, wantErr: true},
		{name: "invalid version", args: []string{"--version", "1.2.3"}, wantErr: true},
		{name: "conflicting modes", args: []string{"--yes", "--skip-customized"}, wantErr: true},
		{name: "unknown argument", args: []string{"--nope"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseUpgradeArgs(test.args)
			if (err != nil) != test.wantErr {
				t.Fatalf("parseUpgradeArgs() error = %v, wantErr %v", err, test.wantErr)
			}
			if err != nil {
				return
			}
			if got.version != test.version || got.replace != test.replace || got.skipCustomized != test.skipCustomized {
				t.Fatalf("parseUpgradeArgs() = %#v", got)
			}
		})
	}
}

func TestCompareUpgradeVersions(t *testing.T) {
	less := upgradeVersion{major: 1, minor: 9, patch: 9}
	greater := upgradeVersion{major: 2}
	if got := compareUpgradeVersions(less, greater); got >= 0 {
		t.Fatalf("compareUpgradeVersions() = %d, want negative", got)
	}
	if got := compareUpgradeVersions(greater, less); got <= 0 {
		t.Fatalf("compareUpgradeVersions() = %d, want positive", got)
	}
	if got := compareUpgradeVersions(less, less); got != 0 {
		t.Fatalf("compareUpgradeVersions() = %d, want zero", got)
	}
}

func TestParseUpgradeChecksums(t *testing.T) {
	content := []byte(strings.Repeat("a", sha256.Size*2) + "  ash-v1.2.3-darwin-arm64.tar.gz\n")
	checksums, err := parseUpgradeChecksums(content)
	if err != nil {
		t.Fatalf("parseUpgradeChecksums() error = %v", err)
	}
	if _, ok := checksums["ash-v1.2.3-darwin-arm64.tar.gz"]; !ok {
		t.Fatalf("checksum entry was not indexed by basename: %#v", checksums)
	}

	for _, invalid := range []string{
		"not-a-digest  ash.tar.gz\n",
		strings.Repeat("a", sha256.Size*2) + "  dist/ash.tar.gz\n",
		strings.Repeat("a", sha256.Size*2) + "  ash.tar.gz\n" + strings.Repeat("b", sha256.Size*2) + "  ash.tar.gz\n",
	} {
		if _, err := parseUpgradeChecksums([]byte(invalid)); err == nil {
			t.Fatalf("parseUpgradeChecksums(%q) succeeded, want error", invalid)
		}
	}
}

func TestVerifyUpgradeChecksum(t *testing.T) {
	content := []byte("release archive")
	digest := sha256.Sum256(content)
	expected := upgradeChecksum{Name: "ash.tar.gz", Digest: hex.EncodeToString(digest[:])}
	if err := verifyUpgradeChecksum(content, expected); err != nil {
		t.Fatalf("verifyUpgradeChecksum() error = %v", err)
	}
	if err := verifyUpgradeChecksum([]byte("tampered"), expected); err == nil {
		t.Fatal("verifyUpgradeChecksum() succeeded for tampered content")
	}
}

func TestDecodeUpgradeRelease(t *testing.T) {
	release, err := decodeUpgradeRelease([]byte(`{"tag_name":"v1.2.3","assets":[]}`))
	if err != nil {
		t.Fatalf("decodeUpgradeRelease() error = %v", err)
	}
	if release.TagName != "v1.2.3" {
		t.Fatalf("TagName = %q", release.TagName)
	}

	for _, body := range []string{
		`{"tag_name":"v1.2.3","prerelease":true}`,
		`{"tag_name":"v1.2.3","draft":true}`,
		`{"tag_name":"v1.2"}`,
	} {
		if _, err := decodeUpgradeRelease([]byte(body)); err == nil {
			t.Fatalf("decodeUpgradeRelease(%s) succeeded, want error", body)
		}
	}
}

func TestSelectUpgradeAssets(t *testing.T) {
	release := upgradeRelease{
		TagName: "v1.2.3",
		Assets: []upgradeAsset{
			{Name: "ash-v1.2.3-darwin-arm64.tar.gz", DownloadURL: "https://example.test/archive"},
			{Name: "ash-v1.2.3-freebsd-amd64.tar.gz", DownloadURL: "https://example.test/freebsd"},
			{Name: upgradeManifest, DownloadURL: "https://example.test/manifest"},
			{Name: upgradeBundle, DownloadURL: "https://example.test/bundle"},
		},
	}
	archive, manifest, bundle, err := selectUpgradeAssets(release, "darwin", "arm64")
	if err != nil {
		t.Fatalf("selectUpgradeAssets() error = %v", err)
	}
	if archive.Name != "ash-v1.2.3-darwin-arm64.tar.gz" || manifest.Name != upgradeManifest || bundle.Name != upgradeBundle {
		t.Fatalf("selected assets = %#v, %#v, %#v", archive, manifest, bundle)
	}
	freebsdArchive, _, _, err := selectUpgradeAssets(release, "freebsd", "amd64")
	if err != nil {
		t.Fatalf("selectUpgradeAssets(freebsd) error = %v", err)
	}
	if freebsdArchive.Name != "ash-v1.2.3-freebsd-amd64.tar.gz" {
		t.Fatalf("selected FreeBSD archive = %#v", freebsdArchive)
	}
	if _, _, _, err := selectUpgradeAssets(release, "windows", "amd64"); err == nil {
		t.Fatal("selectUpgradeAssets() accepted unsupported OS")
	}
	duplicate := release
	duplicate.Assets = append(duplicate.Assets, duplicate.Assets[0])
	if _, _, _, err := selectUpgradeAssets(duplicate, "darwin", "arm64"); err == nil {
		t.Fatal("selectUpgradeAssets() accepted duplicate asset")
	}
}

func TestVerifyUpgradeManifestSignatureRejectsMalformedBundle(t *testing.T) {
	if err := verifyUpgradeManifestSignature([]byte("manifest"), []byte("not-json"), "v1.2.3"); err == nil {
		t.Fatal("verifyUpgradeManifestSignature() accepted malformed bundle")
	}
}

func TestIsUpgradeGitHubHost(t *testing.T) {
	for _, host := range []string{
		"api.github.com",
		"github.com",
		"objects.githubusercontent.com",
		"release-assets.githubusercontent.com",
	} {
		if !isUpgradeGitHubHost(host) {
			t.Errorf("isUpgradeGitHubHost(%q) = false, want true", host)
		}
	}
	for _, host := range []string{"example.com", "attacker.githubusercontent.com"} {
		if isUpgradeGitHubHost(host) {
			t.Errorf("isUpgradeGitHubHost(%q) = true, want false", host)
		}
	}
}

func TestExtractUpgradeArchiveAndReplace(t *testing.T) {
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	content := []byte("new ash binary")
	if err := tarWriter.WriteHeader(&tar.Header{Name: "ash-v1.2.3-darwin-arm64", Mode: 0o755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	unsupported, err := extractUpgradeArchive(archive.Bytes(), root)
	if err != nil {
		t.Fatalf("extractUpgradeArchive() error = %v", err)
	}
	if len(unsupported) != 0 {
		t.Fatalf("unsupported archive entries = %#v, want none", unsupported)
	}
	source := filepath.Join(root, "ash-v1.2.3-darwin-arm64")
	if err := os.Chmod(source, 0o700); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "installed-ash")
	if err := os.WriteFile(destination, []byte("old ash binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := replaceUpgradeFile(source, destination, 0o755); err != nil {
		t.Fatalf("replaceUpgradeFile() error = %v", err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("installed content = %q, want %q", got, content)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("installed mode = %o, want 755", info.Mode().Perm())
	}
}

func TestExtractUpgradeArchiveRejectsUnsafeEntries(t *testing.T) {
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "../ash", Mode: 0o755, Size: 0}); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := extractUpgradeArchive(archive.Bytes(), t.TempDir()); err == nil {
		t.Fatal("extractUpgradeArchive() accepted traversal entry")
	}
}

func TestExtractUpgradeArchiveReportsAndSkipsUnsupportedDirectoryPayload(t *testing.T) {
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	directoryPayload := []byte("directory metadata")
	if err := tarWriter.WriteHeader(&tar.Header{Name: "plugins/", Typeflag: 'D', Mode: 0o755, Size: int64(len(directoryPayload))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(directoryPayload); err != nil {
		t.Fatal(err)
	}
	plugin := []byte("plugin binary")
	if err := tarWriter.WriteHeader(&tar.Header{Name: "plugins/calculator", Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(plugin))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(plugin); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	unsupported, err := extractUpgradeArchive(archive.Bytes(), root)
	if err != nil {
		t.Fatalf("extractUpgradeArchive() error = %v", err)
	}
	if len(unsupported) != 1 || unsupported[0].name != "plugins/" || unsupported[0].size != int64(len(directoryPayload)) {
		t.Fatalf("unsupported archive entries = %#v", unsupported)
	}
	info, err := os.Stat(filepath.Join(root, "plugins"))
	if err != nil {
		t.Fatalf("stat extracted directory: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("extracted plugins entry is not a directory")
	}
	got, err := os.ReadFile(filepath.Join(root, "plugins", "calculator"))
	if err != nil {
		t.Fatalf("read extracted plugin: %v", err)
	}
	if !bytes.Equal(got, plugin) {
		t.Fatalf("extracted plugin = %q, want %q", got, plugin)
	}
}

func TestConfirmUpgradeExceptionsReportsSignatureAndArchiveState(t *testing.T) {
	entries := []unsupportedUpgradeArchiveEntry{{name: "plugins/", typeflag: 'D', size: 12}}
	tests := []struct {
		name          string
		signatureErr  error
		wantSignature string
		wantWarning   string
	}{
		{name: "verified", wantSignature: "Release signature verification: PASSED."},
		{
			name:          "failed",
			signatureErr:  errors.New("signature mismatch"),
			wantSignature: "Release signature verification: FAILED: signature mismatch",
			wantWarning:   "Do not install this release unless you independently trust its source.",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			confirmed, err := confirmUpgradeExceptions(strings.NewReader("yes\n"), &output, "v1.2.3", entries, test.signatureErr)
			if err != nil {
				t.Fatalf("confirmUpgradeExceptions() error = %v", err)
			}
			if !confirmed {
				t.Fatal("confirmUpgradeExceptions() did not accept yes")
			}
			for _, want := range []string{
				test.wantSignature,
				test.wantWarning,
				`"plugins/"`,
				"payloads will not be extracted",
			} {
				if want != "" && !strings.Contains(output.String(), want) {
					t.Errorf("confirmation output %q does not contain %q", output.String(), want)
				}
			}
		})
	}

	confirmed, err := confirmUpgradeExceptions(strings.NewReader("no\n"), io.Discard, "v1.2.3", entries, nil)
	if err != nil {
		t.Fatalf("confirmUpgradeExceptions() error = %v", err)
	}
	if confirmed {
		t.Fatal("confirmUpgradeExceptions() accepted no")
	}
}

func TestInstallUpgradeArchiveInstallsAshAndBroker(t *testing.T) {
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	ash := []byte("#!/bin/sh\nmkdir -p \"$2\"\nfor asset in .ash_env .ash_allow .ash_deny .ash_bashrc .ash_zshrc .ash_fish.fish .ash_fish_env.fish .ash_system; do\n  printf candidate > \"$2/$asset\"\ndone\n")
	broker := []byte("new ash-broker binary")
	plugin := []byte("#!/bin/sh\nprintf plugin\n")
	for _, entry := range []struct {
		name string
		data []byte
	}{
		{name: upgradeAssetName("v1.2.3", runtime.GOOS, runtime.GOARCH), data: ash},
		{name: "ash-broker", data: broker},
		{name: "plugins/calculator", data: plugin},
	} {
		if err := tarWriter.WriteHeader(&tar.Header{Name: entry.name, Mode: 0o755, Size: int64(len(entry.data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	originalHome := osUserHomeDir
	osUserHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { osUserHomeDir = originalHome })
	updateBinDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(updateBinDir, 0o700); err != nil {
		t.Fatalf("create user-local bin: %v", err)
	}
	updateExecutable := filepath.Join(updateBinDir, "ash")
	if err := os.WriteFile(updateExecutable, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatalf("create current ash executable: %v", err)
	}
	originalExecutable := osExecutable
	osExecutable = func() (string, error) { return updateExecutable, nil }
	t.Cleanup(func() { osExecutable = originalExecutable })
	if _, err := userLocalInstallDirectory(); err != nil {
		t.Fatalf("self-update ownership check failed: %v", err)
	}
	originalProvisionPythonEnv := provisionPythonEnv
	provisionPythonEnv = func(io.Writer) {}
	t.Cleanup(func() { provisionPythonEnv = originalProvisionPythonEnv })

	if err := installUpgradeArchive(archive.Bytes(), "v1.2.3", upgradeOptions{replace: true}, &bytes.Buffer{}); err != nil {
		t.Fatalf("installUpgradeArchive() error = %v", err)
	}
	for path, want := range map[string][]byte{
		filepath.Join(home, ".local", "bin", "ash"):                            ash,
		filepath.Join(home, ".local", "bin", "ash-broker"):                     broker,
		filepath.Join(home, ".local", "share", "ash", "plugins", "calculator"): plugin,
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s content = %q, want %q", path, got, want)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if info.Mode().Perm() != 0o755 {
			t.Errorf("%s mode = %o, want 755", path, info.Mode().Perm())
		}
	}
	pluginsInfo, err := os.Stat(filepath.Join(home, ".local", "share", "ash", "plugins"))
	if err != nil {
		t.Fatalf("expected plugins directory to be created: %v", err)
	}
	if !pluginsInfo.IsDir() {
		t.Fatal("expected plugins path to be a directory")
	}
}

func TestInstallUpgradeArchiveRollbackKeepsPreviousBinaryExecutable(t *testing.T) {
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	ash := []byte("#!/bin/sh\nmkdir -p \"$2\"\nprintf candidate > \"$2/.ash_env\"\n")
	if err := tarWriter.WriteHeader(&tar.Header{Name: "ash", Mode: 0o755, Size: int64(len(ash))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(ash); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	previous := []byte("previous ash binary")
	destination := filepath.Join(binDir, "ash")
	if err := os.WriteFile(destination, previous, 0o700); err != nil {
		t.Fatal(err)
	}
	originalHome := osUserHomeDir
	osUserHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { osUserHomeDir = originalHome })

	if err := installUpgradeArchive(archive.Bytes(), "v1.2.3", upgradeOptions{replace: true}, &bytes.Buffer{}); err == nil {
		t.Fatal("installUpgradeArchive() succeeded with incomplete candidate assets")
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, previous) {
		t.Fatalf("rollback content = %q, want %q", got, previous)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("rollback mode = %o, want 700", info.Mode().Perm())
	}
}

func TestExportUpgradeAssetsOmitsLegacyToolsAsset(t *testing.T) {
	candidate := t.TempDir()
	if err := exportUpgradeAssets("", candidate); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(candidate, legacyToolsFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy tools asset exists or could not be checked: %v", err)
	}
}

func TestFindUpgradeBinariesRequireCanonicalNames(t *testing.T) {
	root := t.TempDir()
	const expectedAsh = "ash-v1.0.0-linux-amd64"
	for _, name := range []string{expectedAsh, "ash-broker"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	ash, err := findUpgradeBinary(root, expectedAsh)
	if err != nil || filepath.Base(ash) != expectedAsh {
		t.Fatalf("findUpgradeBinary() = %q, %v", ash, err)
	}
	broker, err := findUpgradeBrokerBinary(root)
	if err != nil || filepath.Base(broker) != "ash-broker" {
		t.Fatalf("findUpgradeBrokerBinary() = %q, %v", broker, err)
	}

	if err := os.Rename(filepath.Join(root, "ash-broker"), filepath.Join(root, expectedAsh+"-broker")); err != nil {
		t.Fatal(err)
	}
	if _, err := findUpgradeBrokerBinary(root); err == nil {
		t.Fatal("findUpgradeBrokerBinary() accepted a non-canonical broker name")
	}
}

func TestRestoreUpgradeFilesRestoresFilesLinksAndCreatedDirectories(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "original")
	if err := os.WriteFile(original, []byte("before"), 0o640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink("original", link); err != nil {
		t.Fatal(err)
	}
	newTarget := filepath.Join(root, "new", "nested", "file")
	targets := []string{original, link, newTarget}
	directories, err := missingUpgradeDirectories(targets)
	if err != nil {
		t.Fatal(err)
	}
	snapshots, err := snapshotUpgradeFiles(append(directories, targets...))
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(original, []byte("candidate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(link, []byte("not a link"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(newTarget), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newTarget, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := restoreUpgradeFiles(snapshots); err != nil {
		t.Fatalf("restoreUpgradeFiles() error = %v", err)
	}
	got, err := os.ReadFile(original)
	if err != nil || string(got) != "before" {
		t.Fatalf("restored original = %q, %v", got, err)
	}
	info, err := os.Stat(original)
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("restored original mode = %v, %v", info, err)
	}
	linkTarget, err := os.Readlink(link)
	if err != nil || linkTarget != "original" {
		t.Fatalf("restored symlink = %q, %v", linkTarget, err)
	}
	if _, err := os.Stat(filepath.Dir(newTarget)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("created directory survived rollback: %v", err)
	}
}

func TestSyncUpgradeAssetsPreservesEnvironmentValues(t *testing.T) {
	home := t.TempDir()
	originalHome := osUserHomeDir
	osUserHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { osUserHomeDir = originalHome })

	workspace := filepath.Join(home, ashWorkspaceDirName)
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".ash_env"), []byte("export ASH_AI_MODEL='keep-me'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(home, "candidate-assets")
	if err := exportUpgradeAssets("", candidate); err != nil {
		t.Fatal(err)
	}
	if err := syncUpgradeAssets(candidate, upgradeOptions{replace: true}, &bytes.Buffer{}); err != nil {
		t.Fatalf("syncUpgradeAssets() error = %v", err)
	}
	content, err := os.ReadFile(filepath.Join(workspace, ".ash_env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "ASH_AI_MODEL='keep-me'") {
		t.Fatalf(".ash_env lost configured value: %q", content)
	}
}
