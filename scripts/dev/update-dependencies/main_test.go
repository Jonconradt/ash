// Copyright (c) 2026 Jonathan L. Conradt
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func fixtureUpdater(t *testing.T, input string) *updater {
	t.Helper()
	t.Chdir(t.TempDir())
	u := &updater{
		input:  bufio.NewReader(strings.NewReader(input)),
		output: io.Discard,
		goos:   "darwin",
		lookup: func(string) (string, error) { return "", errors.New("not installed") },
	}
	u.client = &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		var body string
		if req.URL.Host == "go.dev" {
			body = `[{"version":"go1.27.4","stable":true}]`
		} else {
			found := false
			for _, item := range tools {
				if req.URL.String() == item.url {
					body = `"99.2.3"`
					fields := strings.Split(item.field, ".")
					for i := len(fields) - 1; i >= 0; i-- {
						body = fmt.Sprintf(`{%q:%s}`, fields[i], body)
					}
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("unexpected request: %s", req.URL)
			}
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	u.run = func(_ context.Context, name string, args ...string) (string, error) {
		switch name + " " + strings.Join(args, " ") {
		case "go version":
			return "go version go1.27.4 darwin/arm64", nil
		case "brew update":
			return "", nil
		case "brew info --json=v2 go":
			return `{"formulae":[{"versions":{"stable":"1.27.4"}}]}`, nil
		default:
			t.Fatalf("unexpected command: %s %v", name, args)
			return "", errors.New("unexpected command")
		}
	}
	return u
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNewerStable(t *testing.T) {
	for _, tt := range []struct {
		candidate string
		current   string
		newer     bool
		invalid   bool
	}{
		{"v2.14.0", "v2.9.0", true, false},
		{"2026.2.2", "2026.2.1", true, false},
		{"2026.2", "2026.2.0", false, false},
		{"v2.29.0", "v2.29.1-0.20261009120814-7b1b5cebe007", false, false},
		{"v2.29.1", "v2.29.1-0.20261009120814-7b1b5cebe007", true, false},
		{"0.12.10", "0.12.10", false, false},
		{"1.27.3", "1.28.0", false, false},
		{"v2.0.0-rc.1", "v1.0.0", false, true},
		{"latest", "1.0.0", false, true},
		{"1.0.0", "invalid", false, true},
	} {
		t.Run(tt.candidate+"/"+tt.current, func(t *testing.T) {
			got, err := newerStable(tt.candidate, tt.current)
			if (err != nil) != tt.invalid || got != tt.newer {
				t.Fatalf("newerStable = %v, %v; want newer=%v invalid=%v", got, err, tt.newer, tt.invalid)
			}
		})
	}
}

func TestUpdatePins(t *testing.T) {
	for _, approve := range []bool{true, false} {
		t.Run(strconv.FormatBool(approve), func(t *testing.T) {
			answer := "no\n"
			if approve {
				answer = "yes\n"
			}
			u := fixtureUpdater(t, strings.Repeat(answer, len(tools)))
			var makefile strings.Builder
			makefile.WriteString("# preserve unrelated content\nOTHER ?= unchanged\n")
			for _, item := range tools {
				fmt.Fprintf(&makefile, "%s ?= %s1.2.3 # keep comment\n", item.key, item.prefix)
			}
			writeFile(t, "Makefile", makefile.String())
			if err := u.updatePins(context.Background()); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile("Makefile")
			if err != nil {
				t.Fatal(err)
			}
			want := makefile.String()
			if approve {
				want = strings.ReplaceAll(want, "1.2.3", "99.2.3")
			}
			if string(got) != want {
				t.Fatalf("Makefile = %s; want %s", got, want)
			}
			info, err := os.Stat("Makefile")
			if err != nil || info.Mode().Perm() != 0o644 {
				t.Fatalf("Makefile permissions not preserved: %v, %v", info, err)
			}
			matches, err := filepath.Glob(".dependency-update-*")
			if err != nil || len(matches) != 0 {
				t.Fatalf("temporary files remain: %v, %v", matches, err)
			}
		})
	}
}

func TestPinLookupFailureDoesNotWrite(t *testing.T) {
	u := fixtureUpdater(t, "yes\n")
	original := tools[0].key + " ?= v1.0.0\n"
	writeFile(t, "Makefile", original)
	if err := u.updatePins(context.Background()); err == nil {
		t.Fatal("missing assignment accepted")
	}
	got, err := os.ReadFile("Makefile")
	if err != nil || string(got) != original {
		t.Fatalf("Makefile changed after failed discovery: %s, %v", got, err)
	}
}

func TestRegistryErrors(t *testing.T) {
	for _, tt := range []struct {
		status int
		body   string
	}{
		{http.StatusTooManyRequests, "{}"},
		{http.StatusOK, "invalid JSON"},
		{http.StatusOK, "{}"},
		{http.StatusOK, `{"tag_name":"v2.0.0-rc.1"}`},
	} {
		t.Run(fmt.Sprintf("%d/%s", tt.status, tt.body), func(t *testing.T) {
			u := fixtureUpdater(t, "")
			u.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tt.status, Body: io.NopCloser(strings.NewReader(tt.body))}, nil
			})
			if _, err := u.latest(context.Background(), tools[0]); err == nil {
				t.Fatal("invalid registry response accepted")
			}
		})
	}
}

func TestApprovals(t *testing.T) {
	for _, input := range []string{"", "yes", "maybe\n"} {
		u := fixtureUpdater(t, input)
		if _, err := u.approve("update?"); err == nil {
			t.Fatalf("invalid or incomplete approval %q accepted", input)
		}
	}
}

func TestGoUpgrade(t *testing.T) {
	for _, tt := range []struct {
		name      string
		goos      string
		brewFails bool
		after     string
		input     string
		wantEdit  bool
		wantError bool
		wantBrew  bool
	}{
		{"brew success", "darwin", false, "1.27.4", "yes\nyes\n", true, false, true},
		{"brew failure manual success", "darwin", true, "1.27.4", "yes\nyes\nyes\n", true, false, true},
		{"brew success but wrong PATH", "darwin", false, "1.27.3", "yes\nyes\n", false, true, true},
		{"manual upgrade declined", "darwin", true, "1.27.3", "yes\nno\n", false, true, true},
		{"upgrade declined", "darwin", false, "1.27.3", "no\n", false, false, false},
		{"linux manual upgrade", "linux", false, "1.27.4", "yes\nyes\nyes\n", true, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			u := fixtureUpdater(t, tt.input)
			u.goos = tt.goos
			u.lookup = func(name string) (string, error) {
				if name == "dnf" {
					return name, nil
				}
				return "", errors.New("not installed")
			}
			writeFile(t, "go.mod", "module example.com/test\n\ngo 1.27.3\n")
			versionChecks, brewCalls, editCalls := 0, 0, 0
			baseRun := u.run
			u.run = func(ctx context.Context, name string, args ...string) (string, error) {
				switch name + " " + strings.Join(args, " ") {
				case "go version":
					versionChecks++
					installed := "1.27.3"
					// After failure or on Linux, the upgrade only completes
					// after the manual-upgrade prompt.
					threshold := 1
					if tt.brewFails || tt.goos == "linux" {
						threshold = 2
					}
					if versionChecks > threshold {
						installed = tt.after
					}
					return "go version go" + installed + " " + tt.goos + "/arm64", nil
				case "brew upgrade go":
					brewCalls++
					if tt.brewFails {
						return "", errors.New("fixture brew failure")
					}
					return "", nil
				case "dnf --refresh repoquery --latest-limit=1 --qf %{version} golang":
					return "1.27.4\n", nil
				case "go mod edit -go=1.27.4":
					editCalls++
					return "", nil
				default:
					return baseRun(ctx, name, args...)
				}
			}
			err := u.updateGo(context.Background())
			if (err != nil) != tt.wantError || (editCalls == 1) != tt.wantEdit || (brewCalls == 1) != tt.wantBrew {
				t.Fatalf("error=%v edit=%d brew=%d; want error=%v edit=%v brew=%v", err, editCalls, brewCalls, tt.wantError, tt.wantEdit, tt.wantBrew)
			}
		})
	}
}

func TestGoDirectiveBehindOrAhead(t *testing.T) {
	for _, tt := range []struct {
		required  string
		input     string
		wantEdit  bool
		wantError bool
	}{
		{"1.27.3", "yes\n", true, false},
		{"1.27.3", "no\n", false, false},
		{"1.27.4", "", false, false},
		{"1.28.0", "", false, true},
	} {
		t.Run(tt.required+"/"+tt.input, func(t *testing.T) {
			u := fixtureUpdater(t, tt.input)
			writeFile(t, "go.mod", "module example.com/test\n\ngo "+tt.required+"\n")
			edit := false
			baseRun := u.run
			u.run = func(ctx context.Context, name string, args ...string) (string, error) {
				if name == "go" && reflect.DeepEqual(args, []string{"mod", "edit", "-go=1.27.4"}) {
					edit = true
					return "", nil
				}
				return baseRun(ctx, name, args...)
			}
			err := u.updateGo(context.Background())
			if (err != nil) != tt.wantError || edit != tt.wantEdit {
				t.Fatalf("error=%v edit=%v; want error=%v edit=%v", err, edit, tt.wantError, tt.wantEdit)
			}
		})
	}
}

func TestLinuxPackageGo(t *testing.T) {
	for _, tt := range []struct {
		manager string
		output  string
		want    string
	}{
		{"apt-cache", "  Candidate: 2:1.27.4-1ubuntu1\n  1.28.0-1 100\n", "1.27.4"},
		{"dnf", "1.27.3\n1.27.4\n", "1.27.4"},
		{"pacman", "Name : go\nVersion : 1.27.4-1\n", "1.27.4"},
	} {
		t.Run(tt.manager, func(t *testing.T) {
			u := fixtureUpdater(t, "")
			u.goos = "linux"
			u.lookup = func(name string) (string, error) {
				if name == tt.manager {
					return name, nil
				}
				return "", errors.New("not installed")
			}
			u.run = func(_ context.Context, name string, args ...string) (string, error) {
				if name == "apt-cache" && args[0] == "depends" {
					return "golang-go\n  Depends: golang-1.27-go\n", nil
				}
				return tt.output, nil
			}
			got, err := u.packageGo(context.Background())
			if err != nil || got != tt.want {
				t.Fatalf("packageGo = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
	u := fixtureUpdater(t, "")
	u.goos = "linux"
	if _, err := u.packageGo(context.Background()); err == nil {
		t.Fatal("unsupported package manager accepted")
	}
	if _, err := packageVersion("(none)"); err == nil {
		t.Fatal("missing package candidate accepted")
	}
}

func TestUpdateModuleCommands(t *testing.T) {
	for _, failure := range []string{"", "get", "tidy", "decline"} {
		t.Run(failure, func(t *testing.T) {
			answer := "yes\n"
			if failure == "decline" {
				answer = "no\n"
			}
			u := fixtureUpdater(t, answer)
			writeFile(t, "go.mod", "module example.com/test\n\ngo 1.27.4\n")
			var makefile strings.Builder
			for _, item := range tools {
				fmt.Fprintf(&makefile, "%s ?= %s99.2.3\n", item.key, item.prefix)
			}
			writeFile(t, "Makefile", makefile.String())
			baseRun := u.run
			var commands []string
			u.run = func(ctx context.Context, name string, args ...string) (string, error) {
				command := name + " " + strings.Join(args, " ")
				if command == "go get -u ./..." || command == "go mod tidy" {
					commands = append(commands, command)
					if (failure == "get" && args[0] == "get") || (failure == "tidy" && args[0] == "mod") {
						return "", errors.New("fixture module failure")
					}
					return "", nil
				}
				return baseRun(ctx, name, args...)
			}
			err := u.update(context.Background())
			want := []string{"go get -u ./...", "go mod tidy"}
			switch failure {
			case "get":
				want = want[:1]
			case "decline":
				want = nil
			}
			if !reflect.DeepEqual(commands, want) || (err != nil) != (failure == "get" || failure == "tidy") {
				t.Fatalf("commands=%v error=%v; want commands=%v", commands, err, want)
			}
		})
	}
}

func TestReplaceFileRejectsConcurrentChanges(t *testing.T) {
	t.Chdir(t.TempDir())
	writeFile(t, "Makefile", "concurrent edit")
	if err := replaceFile("Makefile", []byte("original"), []byte("update")); err == nil {
		t.Fatal("concurrent change overwritten")
	}
}
