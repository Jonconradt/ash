// Copyright (c) 2026 Jonathan L. Conradt
// SPDX-License-Identifier: MIT

// This updater deliberately uses only the standard library so it can run with
// the locally installed Go even when go.mod requires a newer toolchain.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type tool struct {
	key    string
	url    string
	field  string
	prefix string
}

func githubTool(key, repo string) tool {
	return tool{key, "https://api.github.com/repos/" + repo + "/releases/latest", "tag_name", "v"}
}

var tools = []tool{
	githubTool("GOLANGCI_LINT_VERSION", "golangci/golangci-lint"),
	{"RUFF_VERSION", "https://pypi.org/pypi/ruff/json", "info.version", ""},
	{"MARKDOWNLINT_CLI2_VERSION", "https://registry.npmjs.org/markdownlint-cli2/latest", "version", ""},
	{"HTML_VALIDATE_VERSION", "https://registry.npmjs.org/html-validate/latest", "version", ""},
	githubTool("GOSEC_VERSION", "securego/gosec"),
	{"GOVULNCHECK_VERSION", "https://proxy.golang.org/golang.org/x/vuln/@latest", "Version", "v"},
	{"STATICCHECK_VERSION", "https://api.github.com/repos/dominikh/go-tools/releases/latest", "tag_name", ""},
	githubTool("YAMLFMT_VERSION", "google/yamlfmt"),
	githubTool("GITLEAKS_VERSION", "gitleaks/gitleaks"),
	githubTool("ACTIONLINT_VERSION", "rhysd/actionlint"),
	{"PIP_AUDIT_VERSION", "https://pypi.org/pypi/pip-audit/json", "info.version", ""},
	{"CARGO_AUDIT_VERSION", "https://crates.io/api/v1/crates/cargo-audit", "crate.max_stable_version", ""},
}

var versionPattern = regexp.MustCompile(`^v?([0-9]+)\.([0-9]+)(?:\.([0-9]+))?(-[0-9A-Za-z.-]+)?$`)

type releaseVersion struct {
	parts      [3]int
	prerelease bool
}

func parseVersion(value string) (releaseVersion, error) {
	var result releaseVersion
	match := versionPattern.FindStringSubmatch(value)
	if match == nil {
		return result, fmt.Errorf("invalid release version %q", value)
	}
	for i := range result.parts {
		if match[i+1] == "" {
			continue
		}
		n, err := strconv.Atoi(match[i+1])
		if err != nil {
			return result, err
		}
		result.parts[i] = n
	}
	result.prerelease = match[4] != ""
	return result, nil
}

// Only the candidate must be stable; existing pins can be pseudo-versions.
func newerStable(candidate, current string) (bool, error) {
	next, err := parseVersion(candidate)
	if err != nil {
		return false, err
	}
	if next.prerelease {
		return false, fmt.Errorf("latest release %q is not stable", candidate)
	}
	old, err := parseVersion(current)
	if err != nil {
		return false, err
	}
	for i := range next.parts {
		if next.parts[i] != old.parts[i] {
			return next.parts[i] > old.parts[i], nil
		}
	}
	return old.prerelease, nil
}

type updater struct {
	input  *bufio.Reader
	output io.Writer
	client *http.Client
	run    func(context.Context, string, ...string) (string, error)
	lookup func(string) (string, error)
	goos   string
}

func (u *updater) fetch(ctx context.Context, url string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "ash-dependency-updater")
	resp, err := u.client.Do(req)
	if err != nil {
		return fmt.Errorf("check %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("check %s: HTTP %s (check connectivity or registry rate limits)", url, resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", url, err)
	}
	return nil
}

func (u *updater) latest(ctx context.Context, item tool) (string, error) {
	var data map[string]any
	if err := u.fetch(ctx, item.url, &data); err != nil {
		return "", err
	}
	var value any = data
	for _, key := range strings.Split(item.field, ".") {
		object, ok := value.(map[string]any)
		if !ok {
			return "", fmt.Errorf("%s: missing field %s", item.key, item.field)
		}
		value = object[key]
	}
	version, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s: missing version %s", item.key, item.field)
	}
	version = item.prefix + strings.TrimPrefix(version, "v")
	if _, err := newerStable(version, version); err != nil {
		return "", fmt.Errorf("%s: %w", item.key, err)
	}
	return version, nil
}

func (u *updater) approve(message string) (bool, error) {
	if _, err := fmt.Fprintf(u.output, "%s [y/N]: ", message); err != nil {
		return false, err
	}
	answer, err := u.input.ReadString('\n')
	if err != nil {
		return false, fmt.Errorf("approval requires interactive input: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true, nil
	case "", "n", "no":
		return false, nil
	default:
		return false, fmt.Errorf("unrecognized answer %q; enter yes or no", strings.TrimSpace(answer))
	}
}

func pinPattern(key string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^(` + regexp.QuoteMeta(key) + `[ \t]+\?=[ \t]+)([^ \t\r\n]+)([^\r\n]*)$`)
}

func (u *updater) updatePins(ctx context.Context) error {
	original, err := os.ReadFile("Makefile")
	if err != nil {
		return err
	}
	updated := string(original)
	type update struct {
		item    tool
		current string
		latest  string
	}
	var changes []update
	// Discover every version before prompting or writing, so a failed lookup
	// cannot leave a partially updated set of pins.
	for _, item := range tools {
		matches := pinPattern(item.key).FindAllStringSubmatch(updated, -1)
		if len(matches) != 1 {
			return fmt.Errorf("expected exactly one %s assignment in Makefile", item.key)
		}
		current := matches[0][2]
		latest, err := u.latest(ctx, item)
		if err != nil {
			return err
		}
		newer, err := newerStable(latest, current)
		if err != nil {
			return fmt.Errorf("%s: %w", item.key, err)
		}
		if _, err := fmt.Fprintf(u.output, "%s: pinned %s; latest stable %s\n", item.key, current, latest); err != nil {
			return err
		}
		if newer {
			changes = append(changes, update{item, current, latest})
		}
	}
	for _, change := range changes {
		yes, err := u.approve(fmt.Sprintf("Update %s from %s to %s?", change.item.key, change.current, change.latest))
		if err != nil {
			return err
		}
		if yes {
			updated = pinPattern(change.item.key).ReplaceAllStringFunc(updated, func(line string) string {
				match := pinPattern(change.item.key).FindStringSubmatch(line)
				return match[1] + change.latest + match[3]
			})
		}
	}
	if updated == string(original) {
		return nil
	}
	return replaceFile("Makefile", original, []byte(updated))
}

func replaceFile(path string, original, updated []byte) (err error) {
	current, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, original) {
		return fmt.Errorf("%s changed during approval; refusing to overwrite it", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(".", ".dependency-update-*")
	if err != nil {
		return err
	}
	defer func() {
		if cleanupErr := os.Remove(tmp.Name()); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
			err = errors.Join(err, cleanupErr)
		}
	}()
	if _, err := tmp.Write(updated); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (u *updater) installedGo(ctx context.Context) (string, error) {
	output, err := u.run(ctx, "go", "version")
	if err != nil {
		return "", err
	}
	fields := strings.Fields(output)
	if len(fields) < 3 || fields[0] != "go" || fields[1] != "version" {
		return "", fmt.Errorf("unexpected go version output: %q", output)
	}
	version := strings.TrimPrefix(fields[2], "go")
	_, err = newerStable(version, version)
	return version, err
}

func packageVersion(output string) (string, error) {
	// Package versions may have an epoch and distribution revision.
	re := regexp.MustCompile(`^(?:[0-9]+:)?([0-9]+\.[0-9]+(?:\.[0-9]+)?)(?:[-+~].*)?$`)
	latest := ""
	for _, field := range strings.Fields(output) {
		match := re.FindStringSubmatch(field)
		if match == nil {
			continue
		}
		if latest == "" {
			latest = match[1]
			continue
		}
		newer, err := newerStable(match[1], latest)
		if err != nil {
			return "", err
		}
		if newer {
			latest = match[1]
		}
	}
	if latest == "" {
		return "", fmt.Errorf("no Go release version in package-manager output: %q", output)
	}
	return latest, nil
}

func (u *updater) packageGo(ctx context.Context) (string, error) {
	if u.goos == "darwin" {
		if _, err := u.run(ctx, "brew", "update"); err != nil {
			return "", err
		}
		output, err := u.run(ctx, "brew", "info", "--json=v2", "go")
		if err != nil {
			return "", err
		}
		var data struct {
			Formulae []struct {
				Versions struct {
					Stable string `json:"stable"`
				} `json:"versions"`
			} `json:"formulae"`
		}
		if err := json.Unmarshal([]byte(output), &data); err != nil {
			return "", err
		}
		if len(data.Formulae) != 1 {
			return "", errors.New("homebrew did not return exactly one Go formula")
		}
		version := data.Formulae[0].Versions.Stable
		_, err = newerStable(version, version)
		return version, err
	}
	if u.goos != "linux" {
		return "", fmt.Errorf("unsupported operating system %s", u.goos)
	}
	for _, manager := range []string{"apt-cache", "dnf", "pacman"} {
		if _, err := u.lookup(manager); err != nil {
			continue
		}
		var output string
		var err error
		switch manager {
		case "apt-cache":
			// golang-go is a metapackage: query the actual versioned compiler.
			output, err = u.run(ctx, manager, "depends", "golang-go")
			if err == nil {
				pkg := regexp.MustCompile(`\bgolang-[0-9]+\.[0-9]+-go\b`).FindString(output)
				if pkg == "" {
					return "", errors.New("apt-cache: golang-go has no versioned compiler dependency; refresh package indexes")
				}
				output, err = u.run(ctx, manager, "policy", pkg)
			}
			if err == nil {
				match := regexp.MustCompile(`(?m)^\s*Candidate:\s*(.+)$`).FindStringSubmatch(output)
				if match == nil {
					return "", errors.New("apt-cache: no Go candidate; run sudo apt-get update")
				}
				output = match[1]
			}
		case "dnf":
			output, err = u.run(ctx, manager, "--refresh", "repoquery", "--latest-limit=1", "--qf", "%{version}", "golang")
		case "pacman":
			output, err = u.run(ctx, manager, "-Si", "go")
			if err == nil {
				match := regexp.MustCompile(`(?m)^Version\s*:\s*(.+)$`).FindStringSubmatch(output)
				if match == nil {
					return "", errors.New("pacman: no Go version; refresh package databases")
				}
				output = match[1]
			}
		}
		if err != nil {
			return "", err
		}
		return packageVersion(output)
	}
	return "", errors.New("unsupported Linux package manager; supported: apt, dnf, pacman")
}

func (u *updater) updateGo(ctx context.Context) error {
	installed, err := u.installedGo(ctx)
	if err != nil {
		return err
	}
	var releases []struct {
		Version string `json:"version"`
		Stable  bool   `json:"stable"`
	}
	if err := u.fetch(ctx, "https://go.dev/dl/?mode=json", &releases); err != nil {
		return err
	}
	if len(releases) == 0 || !releases[0].Stable {
		return errors.New("go.dev returned no latest stable release")
	}
	latest := strings.TrimPrefix(releases[0].Version, "go")
	available, err := u.packageGo(ctx)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(u.output, "Go: installed %s; package candidate %s; latest upstream stable %s\n", installed, available, latest); err != nil {
		return err
	}
	target := latest
	newer, err := newerStable(available, target)
	if err != nil {
		return err
	}
	if newer {
		target = available
	}
	needsUpgrade, err := newerStable(target, installed)
	if err != nil {
		return err
	}
	if needsUpgrade {
		yes, err := u.approve("Upgrade Go to " + target + "?")
		if err != nil {
			return err
		}
		if !yes {
			if _, err := fmt.Fprintln(u.output, "Go upgrade declined; retaining the installed compiler."); err != nil {
				return err
			}
		} else {
			if u.goos == "darwin" {
				if _, err := u.run(ctx, "brew", "upgrade", "go"); err != nil {
					if _, writeErr := fmt.Fprintf(u.output, "Homebrew Go upgrade failed: %v\n", err); writeErr != nil {
						return writeErr
					}
				}
			}
			installed, err = u.installedGo(ctx)
			if err != nil {
				return err
			}
			stillOld, err := newerStable(target, installed)
			if err != nil {
				return err
			}
			if stillOld {
				yes, err := u.approve("Upgrade Go to at least " + target + " manually using your package manager, ensure go on PATH is updated, then enter yes to recheck (Linux: refresh package indexes first). Continue?")
				if err != nil {
					return err
				}
				if !yes {
					return errors.New("go upgrade not completed")
				}
				installed, err = u.installedGo(ctx)
				if err != nil {
					return err
				}
				stillOld, err = newerStable(target, installed)
				if err != nil {
					return err
				}
				if stillOld {
					return fmt.Errorf("installed Go %s is still older than %s; no Go directive update applied", installed, target)
				}
			}
		}
	}
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return err
	}
	match := regexp.MustCompile(`(?m)^go ([^ \r\n]+)\s*$`).FindSubmatch(data)
	if match == nil {
		return errors.New("go.mod has no Go directive")
	}
	required := string(match[1])
	behind, err := newerStable(required, installed)
	if err != nil {
		return err
	}
	if behind {
		return fmt.Errorf("installed Go %s is older than go.mod requirement %s; upgrade Go before continuing", installed, required)
	}
	newer, err = newerStable(installed, required)
	if err != nil || !newer {
		return err
	}
	yes, err := u.approve(fmt.Sprintf("Update go.mod Go directive from %s to installed Go %s?", required, installed))
	if err != nil || !yes {
		return err
	}
	_, err = u.run(ctx, "go", "mod", "edit", "-go="+installed)
	return err
}

func (u *updater) update(ctx context.Context) error {
	if err := u.updateGo(ctx); err != nil {
		return err
	}
	if err := u.updatePins(ctx); err != nil {
		return err
	}
	yes, err := u.approve("Run go get -u ./... and go mod tidy? This may update go.mod and go.sum")
	if err != nil {
		return err
	}
	if yes {
		for _, args := range [][]string{{"get", "-u", "./..."}, {"mod", "tidy"}} {
			if _, err := u.run(ctx, "go", args...); err != nil {
				return err
			}
		}
	}
	_, err = fmt.Fprintln(u.output, "Dependency checks complete. Review and commit any changes before releasing; run make config to refresh installed development tools.")
	return err
}

func runCommand(ctx context.Context, name string, args ...string) (string, error) {
	if _, err := fmt.Fprintf(os.Stdout, "+ %s %s\n", name, strings.Join(args, " ")); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GO111MODULE=on", "GOWORK=off", "LC_ALL=C")
	cmd.Stdin = os.Stdin
	var output bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &output)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return output.String(), nil
}

func main() {
	u := updater{
		input:  bufio.NewReader(os.Stdin),
		output: os.Stdout,
		client: &http.Client{Timeout: 30 * time.Second},
		run:    runCommand,
		lookup: exec.LookPath,
		goos:   runtime.GOOS,
	}
	if err := u.update(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "update-dependencies: %v\nChanges already applied are retained; review git diff before retrying.\n", err)
		os.Exit(1)
	}
}
