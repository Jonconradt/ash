package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	upgradeRepository = "Jonconradt/ash"
	upgradeManifest   = "SHA256SUMS"
	upgradeBundle     = "SHA256SUMS.sigstore.json"
	upgradeAPIBase    = "https://api.github.com/repos/" + upgradeRepository
)

var stableVersionPattern = regexp.MustCompile(`^v([0-9]+)\.([0-9]+)\.([0-9]+)$`)

type upgradeOptions struct {
	version        string
	replace        bool
	skipCustomized bool
}

type upgradeVersion struct {
	major int
	minor int
	patch int
}

type upgradeChecksum struct {
	Digest string
	Name   string
}

type upgradeRelease struct {
	TagName    string         `json:"tag_name"`
	Prerelease bool           `json:"prerelease"`
	Draft      bool           `json:"draft"`
	Assets     []upgradeAsset `json:"assets"`
}

type upgradeAsset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"browser_download_url"`
}

func upgradeAssetName(version, goos, goarch string) string {
	return fmt.Sprintf("ash-%s-%s-%s.tar.gz", version, goos, goarch)
}

func selectUpgradeAssets(release upgradeRelease, goos, goarch string) (upgradeAsset, upgradeAsset, upgradeAsset, error) {
	if goos != "darwin" && goos != "linux" {
		return upgradeAsset{}, upgradeAsset{}, upgradeAsset{}, fmt.Errorf("ash update is unsupported on %s", goos)
	}
	wanted := upgradeAssetName(release.TagName, goos, goarch)
	assets := make(map[string]upgradeAsset, len(release.Assets))
	for _, asset := range release.Assets {
		if asset.Name == "" || asset.DownloadURL == "" {
			continue
		}
		assets[asset.Name] = asset
	}
	archive, ok := assets[wanted]
	if !ok {
		return upgradeAsset{}, upgradeAsset{}, upgradeAsset{}, fmt.Errorf("release %s has no asset %s", release.TagName, wanted)
	}
	manifest, ok := assets[upgradeManifest]
	if !ok {
		return upgradeAsset{}, upgradeAsset{}, upgradeAsset{}, fmt.Errorf("release %s has no %s asset", release.TagName, upgradeManifest)
	}
	bundle, ok := assets[upgradeBundle]
	if !ok {
		return upgradeAsset{}, upgradeAsset{}, upgradeAsset{}, fmt.Errorf("release %s has no %s asset", release.TagName, upgradeBundle)
	}
	return archive, manifest, bundle, nil
}

func upgradeHTTPClient() *http.Client {
	return newHTTPClient(30 * time.Second)
}

func decodeUpgradeRelease(body []byte) (upgradeRelease, error) {
	var release upgradeRelease
	if err := json.Unmarshal(body, &release); err != nil {
		return upgradeRelease{}, fmt.Errorf("decode GitHub release: %w", err)
	}
	if release.TagName == "" || release.Draft || release.Prerelease {
		return upgradeRelease{}, errors.New("release is not a stable published release")
	}
	if _, err := parseUpgradeVersion(release.TagName); err != nil {
		return upgradeRelease{}, err
	}
	return release, nil
}

func runUpgrade(args []string, stdout, stderr io.Writer) int {
	options, err := parseUpgradeArgs(args)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "ash update: %v\n", err)
		printUsage(stderr)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "ash update is not yet available for %s\n", options.version)
	return 1
}

func parseUpgradeArgs(args []string) (upgradeOptions, error) {
	options := upgradeOptions{}
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--version":
			if index+1 >= len(args) || strings.TrimSpace(args[index+1]) == "" {
				return upgradeOptions{}, errors.New("--version requires a value")
			}
			options.version = strings.TrimSpace(args[index+1])
			index++
		case "--yes":
			options.replace = true
		case "--skip-customized":
			options.skipCustomized = true
		default:
			return upgradeOptions{}, fmt.Errorf("unknown argument %q", args[index])
		}
	}
	if options.replace && options.skipCustomized {
		return upgradeOptions{}, errors.New("--yes and --skip-customized cannot be used together")
	}
	if options.version != "" {
		if _, err := parseUpgradeVersion(options.version); err != nil {
			return upgradeOptions{}, err
		}
	}
	return options, nil
}

func parseUpgradeVersion(value string) (upgradeVersion, error) {
	match := stableVersionPattern.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return upgradeVersion{}, fmt.Errorf("version %q must use stable vX.Y.Z format", value)
	}
	major, err := strconv.Atoi(match[1])
	if err != nil {
		return upgradeVersion{}, fmt.Errorf("invalid major version %q: %w", value, err)
	}
	minor, err := strconv.Atoi(match[2])
	if err != nil {
		return upgradeVersion{}, fmt.Errorf("invalid minor version %q: %w", value, err)
	}
	patch, err := strconv.Atoi(match[3])
	if err != nil {
		return upgradeVersion{}, fmt.Errorf("invalid patch version %q: %w", value, err)
	}
	return upgradeVersion{major: major, minor: minor, patch: patch}, nil
}

func compareUpgradeVersions(left, right upgradeVersion) int {
	if left.major != right.major {
		return compareUpgradeIntegers(left.major, right.major)
	}
	if left.minor != right.minor {
		return compareUpgradeIntegers(left.minor, right.minor)
	}
	return compareUpgradeIntegers(left.patch, right.patch)
}

func compareUpgradeIntegers(left, right int) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func parseUpgradeChecksums(content []byte) (map[string]upgradeChecksum, error) {
	checksums := make(map[string]upgradeChecksum)
	for lineNumber, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || len(fields[0]) != sha256.Size*2 {
			return nil, fmt.Errorf("invalid checksum manifest line %d", lineNumber+1)
		}
		digest := strings.ToLower(fields[0])
		if _, err := hex.DecodeString(digest); err != nil {
			return nil, fmt.Errorf("invalid checksum manifest line %d: %w", lineNumber+1, err)
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name != strings.TrimSpace(name) || name == "" || strings.Contains(name, "/") || strings.Contains(name, `\`) {
			return nil, fmt.Errorf("invalid checksum filename on line %d", lineNumber+1)
		}
		if _, exists := checksums[name]; exists {
			return nil, fmt.Errorf("duplicate checksum entry for %q", name)
		}
		checksums[name] = upgradeChecksum{Digest: digest, Name: name}
	}
	if len(checksums) == 0 {
		return nil, errors.New("checksum manifest is empty")
	}
	return checksums, nil
}

func verifyUpgradeChecksum(content []byte, expected upgradeChecksum) error {
	digest := sha256.Sum256(content)
	actual := hex.EncodeToString(digest[:])
	if actual != expected.Digest {
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", expected.Name, expected.Digest, actual)
	}
	return nil
}
