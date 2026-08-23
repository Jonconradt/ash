package main

import (
	"crypto/sha256"
	"encoding/hex"
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
	if _, _, _, err := selectUpgradeAssets(release, "windows", "amd64"); err == nil {
		t.Fatal("selectUpgradeAssets() accepted unsupported OS")
	}
}
