package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	protocommon "github.com/sigstore/protobuf-specs/gen/pb-go/common/v1"
	rekorV1 "github.com/sigstore/protobuf-specs/gen/pb-go/rekor/v1"
	sigstoreBundle "github.com/sigstore/sigstore-go/pkg/bundle"
	sigstoreRoot "github.com/sigstore/sigstore-go/pkg/root"
	sigstoreTlog "github.com/sigstore/sigstore-go/pkg/tlog"
	sigstoreTUF "github.com/sigstore/sigstore-go/pkg/tuf"
	sigstoreVerify "github.com/sigstore/sigstore-go/pkg/verify"
	sigstoreSignature "github.com/sigstore/sigstore/pkg/signature"
)

const (
	upgradeRepository  = "Jonconradt/ash"
	upgradeManifest    = "SHA256SUMS"
	upgradeBundle      = "SHA256SUMS.sigstore.json"
	upgradeAPIBase     = "https://api.github.com/repos/" + upgradeRepository
	upgradeMaxDownload = 128 << 20
	upgradeMaxEntry    = 64 << 20
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

type upgradeAssetChange struct {
	target       string
	content      []byte
	previous     []byte
	hadPrevious  bool
	backup       []byte
	hadBackup    bool
	backupChoice bool
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

type legacyUpgradeBundle struct {
	Base64Signature string            `json:"base64Signature"`
	Cert            string            `json:"cert"`
	RekorBundle     legacyRekorBundle `json:"rekorBundle"`
}

type legacyRekorBundle struct {
	Payload              json.RawMessage `json:"Payload"`
	SignedEntryTimestamp string          `json:"SignedEntryTimestamp"`
}

type legacyRekorPayload struct {
	Body           string `json:"body"`
	IntegratedTime int64  `json:"integratedTime"`
	LogID          string `json:"logID"`
	LogIndex       int64  `json:"logIndex"`
}

func upgradeAssetName(version, goos, goarch string) string {
	return fmt.Sprintf("ash-%s-%s-%s.tar.gz", version, goos, goarch)
}

func selectUpgradeAssets(release upgradeRelease, goos, goarch string) (upgradeAsset, upgradeAsset, upgradeAsset, error) {
	if goos != "darwin" && goos != "freebsd" && goos != "linux" {
		return upgradeAsset{}, upgradeAsset{}, upgradeAsset{}, fmt.Errorf("ash update is unsupported on %s", goos)
	}
	wanted := upgradeAssetName(release.TagName, goos, goarch)
	assets := make(map[string]upgradeAsset, len(release.Assets))
	for _, asset := range release.Assets {
		if asset.Name == "" || asset.DownloadURL == "" {
			continue
		}
		if _, exists := assets[asset.Name]; exists {
			return upgradeAsset{}, upgradeAsset{}, upgradeAsset{}, fmt.Errorf("release %s has duplicate asset %s", release.TagName, asset.Name)
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
	client := newHTTPClient(30 * time.Second)
	client.CheckRedirect = func(request *http.Request, _ []*http.Request) error {
		if !isUpgradeGitHubHost(request.URL.Hostname()) {
			return fmt.Errorf("refusing redirect to untrusted host %q", request.URL.Hostname())
		}
		return nil
	}
	return client
}

func fetchUpgradeRelease(version string) (upgradeRelease, error) {
	url := upgradeAPIBase + "/releases/latest"
	if version != "" {
		url = upgradeAPIBase + "/releases/tags/" + version
	}
	body, err := fetchUpgradeURL(url, "application/vnd.github+json")
	if err != nil {
		return upgradeRelease{}, err
	}
	return decodeUpgradeRelease(body)
}

func fetchUpgradeURL(url, accept string) ([]byte, error) {
	parsed, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if parsed.URL.Scheme != "https" || parsed.URL.Host != "api.github.com" {
		return nil, fmt.Errorf("refusing non-GitHub HTTPS URL %q", url)
	}
	parsed.Header.Set("Accept", accept)
	parsed.Header.Set("User-Agent", "ash-updater/1")
	response, err := upgradeHTTPClient().Do(parsed)
	if err != nil {
		return nil, errors.New("GitHub release request failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub release request returned HTTP %s", response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, upgradeMaxDownload+1))
	if err != nil {
		return nil, errors.New("GitHub release response could not be read")
	}
	if len(body) > upgradeMaxDownload {
		return nil, fmt.Errorf("download %s exceeds size limit", url)
	}
	return body, nil
}

func downloadUpgradeAsset(asset upgradeAsset) ([]byte, error) {
	parsed, err := http.NewRequestWithContext(context.Background(), http.MethodGet, asset.DownloadURL, nil)
	if err != nil {
		return nil, err
	}
	if parsed.URL.Scheme != "https" || !isUpgradeGitHubHost(parsed.URL.Hostname()) || parsed.URL.Hostname() == "api.github.com" {
		return nil, fmt.Errorf("refusing non-GitHub HTTPS asset host %q", parsed.URL.Hostname())
	}
	parsed.Header.Set("Accept", "application/octet-stream")
	parsed.Header.Set("User-Agent", "ash-updater/1")
	response, err := upgradeHTTPClient().Do(parsed)
	if err != nil {
		return nil, fmt.Errorf("download asset %s failed", asset.Name)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download asset %s returned HTTP %s", asset.Name, response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, upgradeMaxDownload+1))
	if err != nil {
		return nil, fmt.Errorf("read asset %s failed", asset.Name)
	}
	if len(body) > upgradeMaxDownload {
		return nil, fmt.Errorf("asset %s exceeds size limit", asset.Name)
	}
	return body, nil
}

func isUpgradeGitHubHost(host string) bool {
	switch strings.ToLower(strings.TrimSuffix(host, ".")) {
	case "api.github.com", "github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com":
		return true
	default:
		return false
	}
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
	if runtime.GOOS != "darwin" && runtime.GOOS != "freebsd" && runtime.GOOS != "linux" {
		_, _ = fmt.Fprintf(stderr, "ash update: unsupported platform %s\n", runtime.GOOS)
		return 1
	}
	release, err := fetchUpgradeRelease(options.version)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "ash update: %v\n", err)
		return 1
	}
	if ashVersion != "dev" {
		current, parseErr := parseUpgradeVersion(ashVersion)
		if parseErr == nil {
			candidate, candidateErr := parseUpgradeVersion(release.TagName)
			if candidateErr == nil && compareUpgradeVersions(candidate, current) <= 0 {
				_, _ = fmt.Fprintf(stdout, "ash is already up to date at %s\n", ashVersion)
				return 0
			}
		}
	}
	archiveAsset, manifestAsset, bundleAsset, err := selectUpgradeAssets(release, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "ash update: %v\n", err)
		return 1
	}
	manifest, err := downloadUpgradeAsset(manifestAsset)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "ash update: %v\n", err)
		return 1
	}
	bundle, err := downloadUpgradeAsset(bundleAsset)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "ash update: signature bundle unavailable: %v\n", err)
		return 1
	}
	if err := verifyUpgradeManifestSignature(manifest, bundle, release.TagName); err != nil {
		_, _ = fmt.Fprintf(stderr, "WARNING: ash update refused release %s: signature verification failed: %v\n", release.TagName, err)
		return 1
	}
	checksums, err := parseUpgradeChecksums(manifest)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "ash update: signed checksum manifest is invalid: %v\n", err)
		return 1
	}
	expected, ok := checksums[archiveAsset.Name]
	if !ok {
		_, _ = fmt.Fprintf(stderr, "ash update: signed checksum manifest has no entry for %s\n", archiveAsset.Name)
		return 1
	}
	archive, err := downloadUpgradeAsset(archiveAsset)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "ash update: %v\n", err)
		return 1
	}
	if err := verifyUpgradeChecksum(archive, expected); err != nil {
		_, _ = fmt.Fprintf(stderr, "WARNING: ash update refused release %s: %v\n", release.TagName, err)
		return 1
	}
	if err := installUpgradeArchive(archive, release.TagName, options, stdout); err != nil {
		_, _ = fmt.Fprintf(stderr, "ash update: %v\n", err)
		return 1
	}
	return 0
}

func verifyUpgradeManifestSignature(manifest, bundle []byte, version string) error {
	root, err := os.MkdirTemp("", "ash-upgrade-verify-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(root) }()
	bundlePath := filepath.Join(root, upgradeBundle)
	if err := os.WriteFile(bundlePath, bundle, 0o600); err != nil {
		return err
	}
	var legacy legacyUpgradeBundle
	if err := json.Unmarshal(bundle, &legacy); err == nil && legacy.Base64Signature != "" {
		return verifyLegacyUpgradeBundle(manifest, legacy, version)
	}
	signedBundle, err := sigstoreBundle.LoadJSONFromPath(bundlePath, sigstoreBundle.AllowCertificateChain())
	if err != nil {
		return fmt.Errorf("load Sigstore bundle: %w", err)
	}
	trustedRoot, err := sigstoreRoot.NewLiveTrustedRoot(sigstoreTUF.DefaultOptions())
	if err != nil {
		return fmt.Errorf("load Sigstore trusted root: %w", err)
	}
	identity, err := sigstoreVerify.NewShortCertificateIdentity(
		"https://token.actions.githubusercontent.com",
		"",
		"https://github.com/"+upgradeRepository+"/.github/workflows/release.yml@refs/tags/"+version,
		"",
	)
	if err != nil {
		return fmt.Errorf("configure Sigstore identity: %w", err)
	}
	verifier, err := sigstoreVerify.NewVerifier(trustedRoot, sigstoreVerify.WithTransparencyLog(1))
	if err != nil {
		return fmt.Errorf("configure Sigstore verifier: %w", err)
	}
	if _, err := verifier.Verify(signedBundle, sigstoreVerify.NewPolicy(
		sigstoreVerify.WithArtifact(bytes.NewReader(manifest)),
		sigstoreVerify.WithCertificateIdentity(identity),
	)); err != nil {
		return fmt.Errorf("verify Sigstore bundle: %w", err)
	}
	return nil
}

func verifyLegacyUpgradeBundle(manifest []byte, bundle legacyUpgradeBundle, version string) error {
	certificatePEM, err := base64.StdEncoding.DecodeString(bundle.Cert)
	if err != nil {
		return fmt.Errorf("decode legacy certificate: %w", err)
	}
	certificate, _ := pem.Decode(certificatePEM)
	if certificate == nil {
		return errors.New("legacy bundle has no certificate")
	}
	leaf, err := x509.ParseCertificate(certificate.Bytes)
	if err != nil {
		return fmt.Errorf("parse legacy certificate: %w", err)
	}
	identity := "https://github.com/" + upgradeRepository + "/.github/workflows/release.yml@refs/tags/" + version
	matchedIdentity := false
	for _, uri := range leaf.URIs {
		if uri.String() == identity {
			matchedIdentity = true
			break
		}
	}
	if !matchedIdentity || leaf.Issuer.CommonName != "sigstore-intermediate" || len(leaf.Issuer.Organization) != 1 || leaf.Issuer.Organization[0] != "sigstore.dev" {
		return errors.New("legacy certificate identity does not match the ash release workflow")
	}
	trustedRoot, err := sigstoreRoot.NewLiveTrustedRoot(sigstoreTUF.DefaultOptions())
	if err != nil {
		return fmt.Errorf("load Sigstore trusted root: %w", err)
	}
	signatureBytes, err := base64.StdEncoding.DecodeString(bundle.Base64Signature)
	if err != nil {
		return fmt.Errorf("decode legacy signature: %w", err)
	}
	verificationTime, err := verifyLegacyRekorBundle(manifest, leaf, signatureBytes, bundle.RekorBundle, trustedRoot)
	if err != nil {
		return err
	}
	verified := false
	for _, authority := range trustedRoot.FulcioCertificateAuthorities() {
		if _, err := authority.Verify(leaf, verificationTime); err == nil {
			verified = true
			break
		}
	}
	if !verified {
		return errors.New("legacy certificate is not trusted by Sigstore")
	}
	verifier, err := sigstoreSignature.LoadDefaultVerifier(leaf.PublicKey)
	if err != nil {
		return fmt.Errorf("load legacy signature verifier: %w", err)
	}
	if err := verifier.VerifySignature(bytes.NewReader(signatureBytes), bytes.NewReader(manifest)); err != nil {
		return fmt.Errorf("verify legacy manifest signature: %w", err)
	}
	return nil
}

func verifyLegacyRekorBundle(manifest []byte, leaf *x509.Certificate, signature []byte, bundle legacyRekorBundle, trustedRoot *sigstoreRoot.LiveTrustedRoot) (time.Time, error) {
	if len(bundle.Payload) == 0 || bundle.SignedEntryTimestamp == "" {
		return time.Time{}, errors.New("legacy bundle is missing Rekor evidence")
	}
	var payload legacyRekorPayload
	if err := json.Unmarshal(bundle.Payload, &payload); err != nil {
		return time.Time{}, fmt.Errorf("parse legacy Rekor payload: %w", err)
	}
	body, err := base64.StdEncoding.DecodeString(payload.Body)
	if err != nil {
		return time.Time{}, fmt.Errorf("decode legacy Rekor body: %w", err)
	}
	logID, err := hex.DecodeString(payload.LogID)
	if err != nil {
		return time.Time{}, fmt.Errorf("decode legacy Rekor log ID: %w", err)
	}
	signedEntryTimestamp, err := base64.StdEncoding.DecodeString(bundle.SignedEntryTimestamp)
	if err != nil {
		return time.Time{}, fmt.Errorf("decode legacy Rekor timestamp: %w", err)
	}
	entry, err := sigstoreTlog.NewTlogEntry(&rekorV1.TransparencyLogEntry{
		LogIndex:          payload.LogIndex,
		LogId:             &protocommon.LogId{KeyId: logID},
		KindVersion:       &rekorV1.KindVersion{Kind: "hashedrekord", Version: "0.0.1"},
		IntegratedTime:    payload.IntegratedTime,
		InclusionPromise:  &rekorV1.InclusionPromise{SignedEntryTimestamp: signedEntryTimestamp},
		CanonicalizedBody: body,
	})
	if err != nil {
		return time.Time{}, fmt.Errorf("parse legacy Rekor entry: %w", err)
	}
	if err := sigstoreTlog.VerifySET(entry, trustedRoot.RekorLogs()); err != nil {
		return time.Time{}, fmt.Errorf("verify legacy Rekor timestamp: %w", err)
	}
	verificationTime := entry.IntegratedTime()
	if verificationTime.Before(leaf.NotBefore) || verificationTime.After(leaf.NotAfter) {
		return time.Time{}, errors.New("legacy Rekor timestamp is outside the certificate validity period")
	}
	rekorCertificate, ok := entry.PublicKey().(*x509.Certificate)
	if !ok || !bytes.Equal(rekorCertificate.Raw, leaf.Raw) {
		return time.Time{}, errors.New("legacy Rekor entry certificate does not match the bundle")
	}
	if !bytes.Equal(entry.Signature(), signature) {
		return time.Time{}, errors.New("legacy Rekor entry signature does not match the bundle")
	}
	digest, algorithm, ok := entry.GetHashedRekordDigest()
	if !ok || algorithm != "sha256" {
		return time.Time{}, errors.New("legacy Rekor entry has an unsupported manifest digest")
	}
	manifestDigest := sha256.Sum256(manifest)
	if !bytes.Equal(digest, manifestDigest[:]) {
		return time.Time{}, errors.New("legacy Rekor entry digest does not match the manifest")
	}
	return verificationTime, nil
}

func installUpgradeArchive(content []byte, version string, options upgradeOptions, stdout io.Writer) error {
	staging, err := os.MkdirTemp("", "ash-upgrade-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	if err := extractUpgradeArchive(content, staging); err != nil {
		return err
	}
	binaryPath, err := findUpgradeBinary(staging, upgradeAssetName(version, runtime.GOOS, runtime.GOARCH))
	if err != nil {
		return err
	}
	candidateAssets := filepath.Join(staging, "assets")
	if err := exportUpgradeAssets(binaryPath, candidateAssets); err != nil {
		return err
	}
	home, err := osUserHomeDir()
	if err != nil {
		return err
	}
	destinationDir, err := ensureUserLocalBinDir(home)
	if err != nil {
		return err
	}
	destination := filepath.Join(destinationDir, "ash")
	// #nosec G304 -- destination is the fixed user-local ~/.local/bin/ash path.
	previousBinary, previousErr := os.ReadFile(destination)
	if previousErr != nil && !errors.Is(previousErr, os.ErrNotExist) {
		return previousErr
	}
	hadPreviousBinary := previousErr == nil
	if err := replaceUpgradeFile(binaryPath, destination, 0o755); err != nil {
		return err
	}
	if err := syncUpgradeAssets(candidateAssets, options, stdout); err != nil {
		if hadPreviousBinary {
			_ = writeUpgradeAsset(destination, previousBinary, false)
		} else {
			_ = os.Remove(destination)
		}
		return err
	}
	provisionPythonEnv(stdout)
	_, _ = fmt.Fprintf(stdout, "updated ash to %s at %s (commit %s)\n", version, destination, ashCommit)
	return nil
}

func ensureUserLocalBinDir(home string) (string, error) {
	destinationDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(destinationDir, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", destinationDir, err)
	}
	return destinationDir, nil
}

func runUpgradeAssetExport(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		_, _ = fmt.Fprintln(stderr, "invalid internal asset export arguments")
		return 1
	}
	if err := exportUpgradeAssets("", args[0]); err != nil {
		_, _ = fmt.Fprintf(stderr, "internal asset export failed: %v\n", err)
		return 1
	}
	return 0
}

func exportUpgradeAssets(binaryPath, destination string) error {
	if binaryPath != "" {
		command := execCommandContext(context.Background(), binaryPath, "--internal-export-assets", destination)
		command.Env = []string{"PATH=" + os.Getenv("PATH")}
		if _, err := command.Output(); err != nil {
			return fmt.Errorf("export candidate assets: %w", err)
		}
		return nil
	}
	assets := []struct {
		source string
		name   string
	}{
		{source: "ash_bootstrap/.ash_env", name: ".ash_env"},
		{source: "ash_bootstrap/.ash_tools", name: ".ash_tools"},
		{source: "ash_bootstrap/.ash_bashrc", name: ".ash_bashrc"},
		{source: "ash_bootstrap/.ash_zshrc", name: ".ash_zshrc"},
		{source: "ash_bootstrap/.ash_system", name: ".ash_system"},
	}
	// #nosec G703 -- destination is supplied only by the private internal export path.
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return err
	}
	for _, asset := range assets {
		content, err := readEmbeddedBootstrapAsset(asset.source)
		if err != nil {
			return err
		}
		// #nosec G703 -- asset names are fixed constants and destination is private staging.
		if err := os.WriteFile(filepath.Join(destination, asset.name), content, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func syncUpgradeAssets(candidateRoot string, options upgradeOptions, stdout io.Writer) error {
	home, err := osUserHomeDir()
	if err != nil {
		return err
	}
	destinationRoot := filepath.Join(home, ashWorkspaceDirName)
	if err := os.MkdirAll(destinationRoot, 0o700); err != nil {
		return err
	}
	assets := []string{".ash_env", ".ash_tools", ".ash_bashrc", ".ash_zshrc", ".ash_system"}
	var reader *bufio.Reader
	if !options.replace && !options.skipCustomized && stdinIsInteractive() {
		reader = bufio.NewReader(os.Stdin)
	}
	changes := make([]upgradeAssetChange, 0, len(assets))
	for _, name := range assets {
		source := filepath.Join(candidateRoot, name)
		target := filepath.Join(destinationRoot, name)
		// #nosec G304 -- source is inside private staging and uses fixed asset names.
		candidate, err := os.ReadFile(source)
		if err != nil {
			return fmt.Errorf("read candidate %s: %w", name, err)
		}
		if name == ".ash_env" {
			values, err := preservedAshEnvValues(target)
			if err != nil {
				return err
			}
			candidate = []byte(buildManagedAshEnv(values))
		}
		// #nosec G304 -- target is derived from the fixed ~/.ash asset names.
		current, readErr := os.ReadFile(target)
		if errors.Is(readErr, os.ErrNotExist) {
			changes = append(changes, upgradeAssetChange{target: target, content: candidate})
			continue
		}
		if readErr != nil {
			return readErr
		}
		baseline, err := readEmbeddedBootstrapAsset(filepath.ToSlash(filepath.Join("ash_bootstrap", name)))
		if err != nil {
			return err
		}
		if bytes.Equal(current, baseline) {
			changes = append(changes, upgradeAssetChange{target: target, content: candidate, previous: current, hadPrevious: true})
			continue
		}
		choice := byte('s')
		if options.replace {
			choice = 'r'
		} else if reader != nil {
			choice, err = promptUpgradeAsset(reader, stdout, target)
			if err != nil {
				return err
			}
		}
		switch choice {
		case 'r':
			changes = append(changes, upgradeAssetChange{target: target, content: candidate, previous: current, hadPrevious: true})
		case 'b':
			// #nosec G304 -- target is derived from the fixed ~/.ash asset names.
			previousBackup, backupErr := os.ReadFile(target + ".bak")
			if backupErr != nil && !errors.Is(backupErr, os.ErrNotExist) {
				return backupErr
			}
			changes = append(changes, upgradeAssetChange{target: target, content: candidate, previous: current, hadPrevious: true, backup: previousBackup, hadBackup: backupErr == nil, backupChoice: true})
		default:
			_, _ = fmt.Fprintf(stdout, "kept customized %s\n", target)
		}
	}
	for index, item := range changes {
		if item.backupChoice {
			if err := os.WriteFile(item.target+".bak", item.previous, 0o600); err != nil {
				rollbackUpgradeAssets(changes[:index+1])
				return fmt.Errorf("backup %s: %w", item.target, err)
			}
		}
		if err := writeUpgradeAsset(item.target, item.content, false); err != nil {
			rollbackUpgradeAssets(changes[:index+1])
			return err
		}
		switch {
		case !item.hadPrevious:
			_, _ = fmt.Fprintf(stdout, "installed %s\n", item.target)
		case item.backupChoice:
			_, _ = fmt.Fprintf(stdout, "backed up and replaced %s\n", item.target)
		case !bytes.Equal(item.previous, item.content):
			_, _ = fmt.Fprintf(stdout, "replaced %s\n", item.target)
		}
	}
	return nil
}

func rollbackUpgradeAssets(changes []upgradeAssetChange) {
	for index := len(changes) - 1; index >= 0; index-- {
		item := changes[index]
		if item.hadPrevious {
			_ = writeUpgradeAsset(item.target, item.previous, false)
		} else {
			_ = os.Remove(item.target)
		}
		if item.backupChoice {
			if item.hadBackup {
				_ = os.WriteFile(item.target+".bak", item.backup, 0o600)
			} else {
				_ = os.Remove(item.target + ".bak")
			}
		}
	}
}

func writeUpgradeAsset(destination string, content []byte, _ bool) error {
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".ash-asset-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	backup := destination + ".upgrade-old"
	if err := os.Remove(backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(destination, backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		_ = os.Rename(backup, destination)
		return err
	}
	_ = os.Remove(backup)
	return nil
}

func extractUpgradeArchive(content []byte, destination string) error {
	reader, err := gzip.NewReader(bytes.NewReader(content))
	if err != nil {
		return fmt.Errorf("open gzip archive: %w", err)
	}
	defer func() { _ = reader.Close() }()
	tarReader := tar.NewReader(reader)
	seen := make(map[string]struct{})
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar archive: %w", err)
		}
		name := filepath.Clean(filepath.FromSlash(header.Name))
		if name == "." || filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe archive path %q", header.Name)
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("duplicate archive entry %q", header.Name)
		}
		seen[name] = struct{}{}
		if header.Typeflag != 0 && header.Typeflag != tar.TypeReg {
			return fmt.Errorf("unsupported archive entry %q", header.Name)
		}
		if header.Size < 0 || header.Size > upgradeMaxEntry {
			return fmt.Errorf("archive entry %q exceeds size limit", header.Name)
		}
		path := filepath.Join(destination, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		// #nosec G304 -- path is normalized and rejected unless contained by the staging root.
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		_, copyErr := io.CopyN(file, tarReader, header.Size)
		closeErr := file.Close()
		if copyErr != nil {
			return fmt.Errorf("extract archive entry %q: %w", header.Name, copyErr)
		}
		if closeErr != nil {
			return closeErr
		}
		if header.Mode&0o111 != 0 {
			// #nosec G302 -- the extracted ash executable must retain execute permission.
			if err := os.Chmod(path, 0o700); err != nil {
				return err
			}
		}
	}
	return nil
}

func findUpgradeBinary(root, expectedName string) (string, error) {
	var matches []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		name := filepath.Base(path)
		if info.Mode().IsRegular() && (name == expectedName || name == "ash" || strings.HasPrefix(name, "ash-v")) {
			matches = append(matches, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("archive contains %d executable ash files, expected exactly one", len(matches))
	}
	return matches[0], nil
}

func replaceUpgradeFile(source, destination string, mode os.FileMode) error {
	// #nosec G304 -- source is the validated executable from private staging.
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".ash-upgrade-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	backup := destination + ".old"
	if err := os.Remove(backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(destination, backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		_ = os.Rename(backup, destination)
		return err
	}
	_ = os.Remove(backup)
	return nil
}

func promptUpgradeAsset(reader *bufio.Reader, writer io.Writer, path string) (byte, error) {
	printMenuTitle(writer, "Customized configuration")
	printHint(writer, path)
	printHint(writer, "[r]eplace  [b]ackup and replace  [s]kip (default: skip)")
	printPrompt(writer, "Choose")
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return 's', err
	}
	line = strings.ToLower(strings.TrimSpace(line))
	if line == "r" || line == "b" {
		return line[0], nil
	}
	return 's', nil
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
