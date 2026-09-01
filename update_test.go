package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
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
	if err := extractUpgradeArchive(archive.Bytes(), root); err != nil {
		t.Fatalf("extractUpgradeArchive() error = %v", err)
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
	if err := extractUpgradeArchive(archive.Bytes(), t.TempDir()); err == nil {
		t.Fatal("extractUpgradeArchive() accepted traversal entry")
	}
}

func TestInstallUpgradeArchiveInstallsAshAndBroker(t *testing.T) {
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	ash := []byte("#!/bin/sh\nmkdir -p \"$2\"\nfor asset in .ash_env .ash_tools .ash_bashrc .ash_zshrc .ash_system; do\n  printf candidate > \"$2/$asset\"\ndone\n")
	broker := []byte("new ash-broker binary")
	for _, entry := range []struct {
		name string
		data []byte
	}{
		{name: "ash", data: ash},
		{name: "ash-broker", data: broker},
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
	originalProvisionPythonEnv := provisionPythonEnv
	provisionPythonEnv = func(io.Writer) {}
	t.Cleanup(func() { provisionPythonEnv = originalProvisionPythonEnv })

	if err := installUpgradeArchive(archive.Bytes(), "v1.2.3", upgradeOptions{replace: true}, &bytes.Buffer{}); err != nil {
		t.Fatalf("installUpgradeArchive() error = %v", err)
	}
	for path, want := range map[string][]byte{
		filepath.Join(home, ".local", "bin", "ash"):        ash,
		filepath.Join(home, ".local", "bin", "ash-broker"): broker,
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
	if err := os.WriteFile(filepath.Join(workspace, ".ash_env"), []byte("export AI_MODEL='keep-me'\n"), 0o600); err != nil {
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
	if !strings.Contains(string(content), "AI_MODEL='keep-me'") {
		t.Fatalf(".ash_env lost configured value: %q", content)
	}
}
