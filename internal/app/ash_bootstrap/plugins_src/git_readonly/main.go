// Copyright (c) 2026 Jonathan L. Conradt
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Jonconradt/ash/internal/plugin"
)

type gitPlugin struct{}

type commandSpec struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	AllowedArgs []string `json:"allowed_args"`
}

type output struct {
	Status       string        `json:"status"`
	Command      string        `json:"command,omitempty"`
	Description  string        `json:"description,omitempty"`
	Available    bool          `json:"available,omitempty"`
	Commands     []commandSpec `json:"commands,omitempty"`
	Query        string        `json:"query,omitempty"`
	Output       string        `json:"output,omitempty"`
	ErrorMessage string        `json:"error_message,omitempty"`
	ErrorReason  string        `json:"error_reason,omitempty"`
	ExitCode     int           `json:"exit_code,omitempty"`
}

var commandSpecs = []commandSpec{
	{Name: "status", Description: "Show working tree and index status.", AllowedArgs: []string{"--short", "--branch", "--porcelain", "--porcelain=v1", "--untracked-files=no", "--untracked-files=normal", "--untracked-files=all"}},
	{Name: "diff", Description: "Compare working tree, index, or commits.", AllowedArgs: []string{"--stat", "--cached", "--name-only", "--name-status", "--no-ext-diff", "--no-textconv", "--color=never"}},
	{Name: "log", Description: "Show commit history.", AllowedArgs: []string{"--oneline", "--decorate", "--graph", "--stat", "--name-only", "--name-status", "--all", "-n", "--max-count", "--format"}},
	{Name: "show", Description: "Show an object or commit.", AllowedArgs: []string{"--stat", "--name-only", "--name-status", "--no-ext-diff", "--no-textconv", "--format", "--color=never"}},
	{Name: "rev-parse", Description: "Resolve revisions and repository metadata.", AllowedArgs: []string{"--show-toplevel", "--abbrev-ref", "--verify", "HEAD"}},
	{Name: "rev-list", Description: "List reachable commits.", AllowedArgs: []string{"--count", "--all", "--oneline"}},
	{Name: "branch", Description: "List local or remote branches.", AllowedArgs: []string{"--show-current", "--all", "--remotes", "--no-color"}},
	{Name: "tag", Description: "List tags.", AllowedArgs: []string{"--list", "--sort"}},
	{Name: "remote", Description: "List configured remotes.", AllowedArgs: []string{"-v", "--verbose"}},
	{Name: "reflog", Description: "Show local reference history.", AllowedArgs: []string{"show", "--oneline", "--all"}},
	{Name: "stash", Description: "List or show stash entries.", AllowedArgs: []string{"list", "show", "--stat", "--oneline"}},
	{Name: "grep", Description: "Search tracked text in the repository.", AllowedArgs: []string{"-n", "--line-number", "-i", "--ignore-case", "-l", "--files-with-matches"}},
	{Name: "ls-files", Description: "List files known to the index.", AllowedArgs: []string{"--cached", "--deleted", "--modified", "--others", "--stage", "--exclude-standard"}},
	{Name: "ls-tree", Description: "List a tree object.", AllowedArgs: []string{"-r", "--name-only", "--full-tree"}},
	{Name: "for-each-ref", Description: "List repository references.", AllowedArgs: []string{"--format"}},
	{Name: "merge-base", Description: "Find a common ancestor.", AllowedArgs: []string{"--all"}},
	{Name: "describe", Description: "Describe a commit using tags.", AllowedArgs: []string{"--tags", "--always", "--dirty"}},
	{Name: "name-rev", Description: "Name revisions from commit IDs.", AllowedArgs: []string{"--name-only", "--no-undefined"}},
	{Name: "blame", Description: "Show line attribution for tracked text.", AllowedArgs: []string{"--line-porcelain", "--date=short"}},
	{Name: "cat-file", Description: "Inspect Git objects.", AllowedArgs: []string{"-t", "-p", "-s"}},
	{Name: "worktree", Description: "List worktrees.", AllowedArgs: []string{"list"}},
	{Name: "count-objects", Description: "Count loose and packed objects.", AllowedArgs: []string{"-v", "--verbose"}},
	{Name: "check-ignore", Description: "Check ignore rules for a path.", AllowedArgs: []string{"-v", "--verbose"}},
	{Name: "check-mailmap", Description: "Resolve mailmap identities.", AllowedArgs: []string{}},
}

func (p *gitPlugin) Name() string    { return "git_readonly" }
func (p *gitPlugin) Version() string { return "1.0.0" }

func (p *gitPlugin) AIDocs() string {
	docs := map[string]any{
		"schema_version": 1,
		"name":           p.Name(),
		"version":        p.Version(),
		"input_schema": map[string]any{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"type":    "object",
			"properties": map[string]any{
				"--list":  map[string]any{"type": "boolean", "description": "List supported read-only Git commands."},
				"--query": map[string]any{"type": "string", "description": "Query one command and its allowed arguments."},
				"--run":   map[string]any{"type": "string", "description": "Run one supported read-only Git subcommand."},
				"--arg":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Repeated allowed argument for --run."},
			},
			"additionalProperties": false,
		},
		"output_schema": map[string]any{
			"$schema":  "https://json-schema.org/draft/2020-12/schema",
			"type":     "object",
			"required": []string{"status"},
			"properties": map[string]any{
				"status":        map[string]any{"type": "string", "enum": []string{"success", "error"}},
				"command":       map[string]any{"type": "string"},
				"description":   map[string]any{"type": "string"},
				"available":     map[string]any{"type": "boolean"},
				"commands":      map[string]any{"type": "array"},
				"query":         map[string]any{"type": "string"},
				"output":        map[string]any{"type": "string"},
				"error_message": map[string]any{"type": "string"},
				"error_reason":  map[string]any{"type": "string", "enum": []string{"not_git_repository", "unsafe_git_metadata", "git_not_found", "git_failed", "command_timeout"}},
				"exit_code":     map[string]any{"type": "integer"},
			},
			"additionalProperties": false,
		},
		"Capabilities": []string{
			"Discover supported read-only Git subcommands in a compact two-step interface.",
			"Execute only a fixed allowlist of read-only Git subcommands in the current repository.",
			"Reject repository, worktree, path, config, option, and environment escape attempts.",
		},
		"Arguments": map[string]string{
			"--list":    "List the supported command names and descriptions.",
			"--query":   "Query whether a subcommand is supported and list its allowed arguments.",
			"--run":     "Execute a supported read-only subcommand.",
			"--arg":     "Repeat to pass an explicitly allowed argument to --run.",
			"--ai-docs": "Print this documentation and exit.",
			"--version": "Print version and exit.",
			"--help":    "Print help and exit.",
		},
		"Return format": map[string]string{
			"status":        "success or error",
			"commands":      "Allowed command summaries returned by --list",
			"available":     "Whether --query found an allowed command",
			"output":        "Bounded Git stdout",
			"error_message": "Actionable validation or execution error",
			"error_reason":  "Machine-readable reason: not_git_repository, unsafe_git_metadata, git_not_found, git_failed, or command_timeout",
			"exit_code":     "Git process exit code when execution fails",
		},
		"Usage guidance for the AI": "Call --list once to discover commands, use --query for a specific command, then call --run only with arguments from that command's allowed_args. This plugin never runs mutating subcommands, never accepts path or repository overrides, and only operates in a real .git directory directly below the current directory.",
	}
	data, err := json.MarshalIndent(docs, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(data)
}

type repeatedArg []string

func (r *repeatedArg) String() string { return strings.Join(*r, ",") }
func (r *repeatedArg) Set(value string) error {
	*r = append(*r, value)
	return nil
}

func findSpec(name string) (commandSpec, bool) {
	for _, spec := range commandSpecs {
		if spec.Name == name {
			return spec, true
		}
	}
	return commandSpec{}, false
}

func writeOutput(w io.Writer, payload output) int {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		_, _ = fmt.Fprintln(w, `{"status":"error","error_message":"failed to encode response"}`)
		return 1
	}
	_, _ = fmt.Fprintln(w, string(data))
	if payload.Status == "error" {
		return 1
	}
	return 0
}

func validateArg(spec commandSpec, arg string) bool {
	if arg == "" || len(arg) > 256 {
		return false
	}
	if strings.ContainsAny(arg, "\x00\r\n") {
		return false
	}
	if strings.Contains(arg, "..") || strings.Contains(arg, "~") {
		return false
	}
	for _, blocked := range []string{"-C", "--git-dir", "--work-tree", "--namespace", "--exec-path", "--config-env", "--config", "-c"} {
		if arg == blocked || strings.HasPrefix(arg, blocked+"=") {
			return false
		}
	}

	for _, allowed := range spec.AllowedArgs {
		if arg == allowed {
			return true
		}
		if allowed == "-n" && (strings.HasPrefix(arg, "-n") && arg != "-n") {
			return true
		}
		if allowed == "--max-count" && strings.HasPrefix(arg, "--max-count=") {
			return true
		}
		if allowed == "--format" && strings.HasPrefix(arg, "--format=") {
			return true
		}
		if allowed == "--sort" && strings.HasPrefix(arg, "--sort=") {
			return true
		}
	}

	switch spec.Name {
	case "log", "show", "rev-list", "rev-parse", "ls-tree", "merge-base", "describe", "name-rev", "blame", "cat-file", "grep":
		if !strings.HasPrefix(arg, "-") && !strings.ContainsAny(arg, `/\`) {
			return true
		}
	}
	return false
}

func currentRepository() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("determine current directory: %w", err)
	}
	info, err := os.Lstat(filepath.Join(cwd, ".git"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", errors.New("not in a git repository: the current directory has no .git entry")
		}
		return "", fmt.Errorf("cannot inspect .git metadata: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("unsafe git repository metadata: .git exists but is not a real directory")
	}
	return cwd, nil
}

func (p *gitPlugin) Run(ctx context.Context, args []string, stdout, stderr io.Writer, logger *slog.Logger) int {
	logger.Debug("running git_readonly plugin", "args", args, "EID", "gRoP001")

	flags := flag.NewFlagSet("git_readonly", flag.ContinueOnError)
	flags.SetOutput(stderr)
	list := flags.Bool("list", false, "List supported read-only Git commands")
	query := flags.String("query", "", "Query one supported read-only Git command")
	runName := flags.String("run", "", "Run one supported read-only Git command")
	var runArgs repeatedArg
	flags.Var(&runArgs, "arg", "Allowed argument for --run; may be repeated")
	if err := flags.Parse(args); err != nil {
		logger.Warn("failed to parse arguments", "error", err, "EID", "gRoP002")
		return writeOutput(stdout, output{Status: "error", ErrorMessage: "invalid arguments"})
	}
	if flags.NArg() != 0 {
		return writeOutput(stdout, output{Status: "error", ErrorMessage: "positional arguments are not accepted"})
	}

	if *list {
		if *query != "" || *runName != "" || len(runArgs) != 0 {
			return writeOutput(stdout, output{Status: "error", ErrorMessage: "--list cannot be combined with --query or --run"})
		}
		return writeOutput(stdout, output{Status: "success", Commands: commandSpecs})
	}

	if *query != "" {
		if *runName != "" || len(runArgs) != 0 {
			return writeOutput(stdout, output{Status: "error", ErrorMessage: "--query cannot be combined with --run"})
		}
		spec, ok := findSpec(*query)
		if !ok {
			return writeOutput(stdout, output{Status: "success", Query: *query, Available: false})
		}
		return writeOutput(stdout, output{Status: "success", Query: *query, Available: true, Command: spec.Name, Description: spec.Description})
	}

	if *runName == "" {
		if len(runArgs) != 0 {
			return writeOutput(stdout, output{Status: "error", ErrorMessage: "--arg requires --run"})
		}
		return writeOutput(stdout, output{Status: "success", Commands: commandSpecs})
	}

	spec, ok := findSpec(*runName)
	if !ok {
		return writeOutput(stdout, output{Status: "error", Command: *runName, ErrorMessage: "Git subcommand is not in the read-only allowlist"})
	}
	for _, arg := range runArgs {
		if !validateArg(spec, arg) {
			return writeOutput(stdout, output{Status: "error", Command: spec.Name, ErrorMessage: fmt.Sprintf("argument %q is not allowed for git %s", arg, spec.Name)})
		}
	}

	cwd, err := currentRepository()
	if err != nil {
		reason := "not_git_repository"
		message := "Not in a Git repository: the current directory has no .git entry. Run Ash from the repository root or ensure the current directory is the repository root."
		if strings.Contains(err.Error(), "unsafe git repository metadata") {
			reason = "unsafe_git_metadata"
			message = "Unsafe Git repository metadata: .git exists but is not a real directory. Ash will not follow Git metadata links or files."
		} else if strings.Contains(err.Error(), "cannot inspect .git metadata") {
			message = fmt.Sprintf("Not in a Git repository: cannot inspect .git metadata. %v", err)
		}
		return writeOutput(stdout, output{Status: "error", Command: spec.Name, ErrorMessage: message, ErrorReason: reason})
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return writeOutput(stdout, output{Status: "error", Command: spec.Name, ErrorMessage: "Cannot execute Git: the git executable was not found in PATH. The repository was detected, but no read-only Git command could run.", ErrorReason: "git_not_found"})
	}

	runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// #nosec G204 -- gitPath comes from LookPath; spec.Name and every runArg are allowlist-validated above.
	cmd := exec.CommandContext(runCtx, gitPath, append([]string{spec.Name}, runArgs...)...)
	cmd.Dir = cwd
	cmd.Stdin = strings.NewReader("")
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"TMPDIR=" + os.TempDir(),
		"LANG=C",
		"LC_ALL=C",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_PAGER=cat",
		"GIT_EXTERNAL_DIFF=",
	}

	var stdoutBuffer, stderrBuffer strings.Builder
	cmd.Stdout = &stdoutBuffer
	cmd.Stderr = &stderrBuffer
	err = cmd.Run()
	if runCtx.Err() == context.DeadlineExceeded {
		return writeOutput(stdout, output{Status: "error", Command: spec.Name, ErrorMessage: "Git command timed out after 10 seconds. No repository files or metadata were changed.", ErrorReason: "command_timeout"})
	}
	if err != nil {
		exitCode := 1
		if cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		}
		message := strings.TrimSpace(stderrBuffer.String())
		if message == "" {
			message = err.Error()
		}
		if len(message) > 2048 {
			message = message[:2048]
		}
		return writeOutput(stdout, output{Status: "error", Command: spec.Name, ErrorMessage: message, ErrorReason: "git_failed", ExitCode: exitCode})
	}

	text := stdoutBuffer.String()
	if len(text) > 1024*1024 {
		text = text[:1024*1024] + "\n[output truncated]"
	}
	if !slices.Contains([]string{"status", "diff", "log", "show", "rev-parse", "rev-list", "branch", "tag", "remote", "reflog", "stash", "grep", "ls-files", "ls-tree", "for-each-ref", "merge-base", "describe", "name-rev", "blame", "cat-file", "worktree", "count-objects", "check-ignore", "check-mailmap"}, spec.Name) {
		return writeOutput(stdout, output{Status: "error", Command: spec.Name, ErrorMessage: "unexpected subcommand"})
	}
	logger.Debug("completed git_readonly execution", "command", spec.Name, "EID", "gRoP003")
	return writeOutput(stdout, output{Status: "success", Command: spec.Name, Output: text})
}

func main() {
	plugin.Main(&gitPlugin{})
}
