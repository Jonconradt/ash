# ash

`ash` is a small Go executable for shell `command_not_found_handle()` workflows.
It sends the command text to an Ollama model using the Ollama Chat API and prints
the assistant response.

## Features

- Uses `AI_ENDPOINT` and `AI_MODEL` to target local or cloud Ollama Chat API endpoints
- Supports bearer authentication with `AI_AUTH_TYPE=bearer` and `AI_AUTH_TOKEN`
- Uses Ollama Chat API (`/api/chat`)
- Supports Ollama native tool calling (`tools`, `tool_calls`, `role=tool` messages)
- Shows an ANSI-friendly thinking indicator while waiting for Ollama
- Supports `Ctrl-C` to abort an in-flight Ollama request
- Keeps chat history across calls
- Uses `~/.ash/.ash_system` as the canonical system prompt file when present
- Always prepends current local date/time to the system prompt sent to the model
- Supports emoji input/output (UTF-8)
- Renders markdown output to terminal styling with ANSI fallback safety
- Can execute allowlisted Unix commands and `python3` as AI tools
- Can schedule one-off and recurring prompt invocations via `at` and `crontab`
- Maintains recurring-job inventory and management through AI tools
- Provides persistent AI workspace file access under `~/.ash`

## Build

```bash
go build -o ash ash.go
```

## Make Targets

```bash
make lint
make test
make install
make verify
make release
make release RELEASE_VERSION=v1.2.3
```

- `make lint` runs `golangci-lint` checks across the module
- `make test` runs `go test ./...`
- `make install` runs `go install ./...`
- `make verify` runs strict checks (tests, race, coverage gate, vet, staticcheck)
- `make release` runs quality checks, builds an arm64 macOS `.pkg`, validates it,
  writes a SHA-256 checksum to `dist/release/`, creates the release tag, and pushes
  it to `origin`
- If `RELEASE_VERSION` is omitted, the latest stable tag (`v<major>.<minor>.<patch>`)
  is used as the source and the next version is derived as `v<major>.<minor+1>.0`

Contributor note: run `make lint test` before submitting changes.

## Release Process

Canonical publishing is tag-driven in GitHub Actions.

1. Run `make release RELEASE_VERSION=v1.2.3` (or omit `RELEASE_VERSION` to auto-derive).
2. `make release` creates and pushes the release tag to `origin`.
3. The `release` workflow builds and validates the macOS installer package.
4. GitHub Release assets are published automatically:
   - `ash-v1.2.3-darwin-arm64.pkg`
   - `ash-v1.2.3-darwin-arm64.pkg.sha256`

For local maintainer checks without publishing, run the release build steps directly:

```bash
make release-check
make release-build release-pkg release-validate RELEASE_VERSION=v1.2.3
```

Requirements for local packaging:

- macOS with `pkgbuild` and `pkgutil` available
- Go toolchain installed

The package is currently unsigned and not notarized by design.

## Configure

Set a local Ollama target:

```bash
export AI_ENDPOINT="http://localhost:11434"
export AI_MODEL="llama3.1:latest"
```

Cloud example (authenticated):

```bash
export AI_ENDPOINT="https://ollama.example.com"
export AI_MODEL="llama3.1:latest"
export AI_AUTH_TYPE="bearer"
export AI_AUTH_TOKEN="<token>"
```

Notes:

- Cloud endpoints are inferred when the host is not `localhost` or `127.0.0.1`.
- Cloud endpoints must use `https` and require bearer auth.
- Legacy `AI=ollama://...` configuration is no longer supported.

Optional canonical system prompt file:

```text
$HOME/.ash/.ash_system
```

Example `.ash_system` content:

```text
You are a concise shell assistant. Keep answers short and practical. 🙂
```

History is stored in:

```text
$HOME/.ash_history.json
```

Optional max history messages (default: `40`):

```bash
export ASH_HISTORY_MAX=80
```

Optional AI request timeout (default: `3m`):

```bash
export AI_TIMEOUT=90s
export AI_TIMEOUT=3m
```

Verbose debug logging (off by default):

```bash
export ASH_VERBOSE=1
```

Install shell integration (wrappers plus command-not-found hook):

```bash
ash install --shell bash
ash install --shell zsh
```

Preview without writing files:

```bash
ash install --shell bash --dry-run
```

`ash install` is idempotent and appends a single managed block to your rc file.
For bash it targets `~/.bashrc`; for zsh it targets `~/.zshrc`.
During install, if `./.ash_system` or `./.ash_tools` exist in your current
directory, they are copied into `~/.ash/` and become the canonical files used
by `ash`.

When enabled, `ash` logs:

- full JSON payload sent to Ollama `/api/chat`
- Ollama response status and body
- tool loop iteration decisions
- tool invocation name/arguments and returned result payload

## Tool Execution

`ash` publishes these tools to Ollama on each request:

- `run_unix_command`: executes one allowlisted Unix executable with direct argv (no shell)
- `run_python3`: executes `python3 -c <code>` with optional argv
- `schedule_future_prompt`: schedules one prompt run via a user `launchd` LaunchAgent
- `schedule_recurring_prompt`: schedules recurring prompt runs via `crontab`
- `manage_recurring_jobs`: lists, cancels, modifies, and explains ash-managed recurring jobs
- `ash_read_workspace_file`: reads a file from `~/.ash`
- `ash_write_workspace_file`: writes a file in `~/.ash` and auto-updates `~/.ash/inventory.md`

Tool execution is local to your machine. Use a narrow allowlist.

### Allowlist configuration

Set allowlisted Unix executables with one of these methods:

1. Environment variable override:

```bash
export ASH_TOOL_ALLOWLIST="ls,ps,man,osascript"
```

2. Canonical config file `$HOME/.ash/.ash_tools`:

```text
# one per line or comma-separated
ls
ps
man
osascript
```

If both are present, `ASH_TOOL_ALLOWLIST` wins.

### Tool safety settings

Optional settings:

```bash
export ASH_TOOL_TIMEOUT=15s
export ASH_TOOL_OUTPUT_MAX=8192
export ASH_MAX_TOOL_ITERS=4
```

The Unix tool rejects risky shell-control argument patterns and always executes directly without shell interpolation.

### Scheduling behavior

- One-off scheduling uses a user `launchd` LaunchAgent under `~/.ash/launchagents/` with a per-invocation `com.user.gonetwork.<id>.plist` filename and loads it with `launchctl`.
- Recurring scheduling uses user `crontab` entries with ash metadata markers.
- Recurring-job management (`list`, `cancel`, `modify`, `explain`) operates only on ash-owned crontab entries.
- Scheduled runs capture prompt + working directory and replay a minimal environment allowlist (`AI`, `HOME`, `PATH`, and selected ash config vars).
- One-off scheduled runs also enable verbose logging and write JSON debug logs to `~/.ash/logs/scheduler.log`, rotating the file at 1 MB.

### Persistent workspace files

Ash reserves `~/.ash` for cross-invocation state files created by AI tools.

- `~/.ash/inventory.md` tracks file name and purpose entries in `name | purpose` format.
- Inventory examples (such as counters) are descriptive metadata, not built-in special behavior.
- Workspace tools enforce path containment and reject paths outside `~/.ash`.

### Example: osascript tool call target

If `osascript` is allowlisted, the AI can run commands like:

```bash
osascript -e 'say "Good day!" using "Karen"'
```

### Note on model support

Your Ollama model must support tool calling. See Ollama models with tools:

https://ollama.com/search?c=tool

## Use with `command_not_found_handle`

Add this to your bash profile (`~/.bashrc` or `~/.bash_profile`):

```bash
command_not_found_handle() {
  /path/to/ash "$@"
  return $?
}
```

When bash cannot find a command, it will call `ash` with the original input.

On startup, `ash` checks whether its managed install block exists for your
current shell. If not, it prints a recommendation such as:

```text
ash is not installed for bash. Run: ash install --shell bash
```

## Zero-prefix command collision handling

`command_not_found_handle` only runs when command lookup fails. If the first word
of a natural-language prompt is also a real command (`what`, `time`, `test`,
`type`, `which`, `who`, and on zsh optionally `where`), shell lookup succeeds
and `ash` is not called.

Use selective wrappers so users can type prompts directly without a prefix.

### Conservative deterministic heuristic

For each wrapped command, apply these ordered rules:

1. Delegate when there are no args.
2. Delegate when any arg starts with `-`.
3. Delegate when any arg is path-like (`/`, `./`, `../`).
4. For `Time`, `test`, and `type`: delegate when there is exactly one arg and
   it matches `[A-Za-z0-9_.-]+`.
5. Route to `ash` when the full input ends with `?` and there are at least two args.
6. Route to `ash` when the first arg (lowercased) is one of:
   `is are am do does did can could should would will why how when where who`,
   and there are at least two args.
7. Otherwise delegate.

This is intentionally conservative to minimize false positives.

### Bash setup

Add this to your `~/.bashrc`:

```bash
command_not_found_handle() {
  ash "$@"
  return $?
}

_ash_should_route() {
  local cmd="$1"
  shift
  local args=("$@")
  local argc=${#args[@]}

  # Rule A
  [[ $argc -eq 0 ]] && return 1

  # Rule B
  local a
  for a in "${args[@]}"; do
    [[ "$a" == -* ]] && return 1
  done

  # Rule C
  for a in "${args[@]}"; do
    [[ "$a" == */* || "$a" == ./* || "$a" == ../* ]] && return 1
  done

  # Rule D
  if [[ "$cmd" == "Time" || "$cmd" == "test" || "$cmd" == "Test" || "$cmd" == "type" || "$cmd" == "Type" ]]; then
    if [[ $argc -eq 1 && "${args[0]}" =~ ^[A-Za-z0-9_.-]+$ ]]; then
      return 1
    fi
  fi

  local full="$cmd"
  for a in "${args[@]}"; do
    full+=" $a"
  done

  # Rule E
  [[ "$full" == *\? && $argc -ge 2 ]] && return 0

  # Rule F
  local first
  first="$(printf '%s' "${args[0]}" | tr '[:upper:]' '[:lower:]')"
  case "$first" in
    is|are|am|do|does|did|can|could|should|would|will|why|how|when|where|who)
      [[ $argc -ge 2 ]] && return 0
      ;;
  esac

  # Rule G
  return 1
}

_ash_route_or_delegate() {
  local cmd="$1"
  shift
  if _ash_should_route "$cmd" "$@"; then
    ash "$cmd" "$@"
    return $?
  fi
  command "$cmd" "$@"
}

_ash_route_or_delegate_builtin() {
  local builtin_name="$1"
  shift
  if _ash_should_route "$builtin_name" "$@"; then
    ash "$builtin_name" "$@"
    return $?
  fi
  builtin "$builtin_name" "$@"
}

# External command collisions.
what()  { _ash_route_or_delegate what  "$@"; }
What()  { _ash_route_or_delegate What  "$@"; }
which() { _ash_route_or_delegate which "$@"; }
Which() { _ash_route_or_delegate Which "$@"; }
who()   { _ash_route_or_delegate who   "$@"; }
Who()   { _ash_route_or_delegate Who   "$@"; }

# Builtin collisions.
test()  { _ash_route_or_delegate_builtin test "$@"; }
Test()  { _ash_route_or_delegate_builtin test "$@"; }
type()  { _ash_route_or_delegate_builtin type "$@"; }
Type()  { _ash_route_or_delegate_builtin type "$@"; }
Time()  { _ash_route_or_delegate Time "$@"; }
```

`time` is a reserved shell keyword in bash, so `time()` wrappers are not valid.
Use `Time ...` (capitalized wrapper) or `ash "time ..."` for AI intent.

### Zsh setup

For zsh, use `command_not_found_handler` and the same wrapper pattern:

```zsh
command_not_found_handler() {
  ash "$@"
  return $?
}

_ash_should_route() {
  local cmd="$1"
  shift
  local -a args
  args=("$@")
  local argc=${#args}

  [[ $argc -eq 0 ]] && return 1

  local a
  for a in "${args[@]}"; do
    [[ "$a" == -* ]] && return 1
  done

  for a in "${args[@]}"; do
    [[ "$a" == */* || "$a" == ./* || "$a" == ../* ]] && return 1
  done

  if [[ "$cmd" == "Time" || "$cmd" == "test" || "$cmd" == "Test" || "$cmd" == "type" || "$cmd" == "Type" ]]; then
    if [[ $argc -eq 1 && "${args[1]}" =~ '^[A-Za-z0-9_.-]+$' ]]; then
      return 1
    fi
  fi

  local full="$cmd"
  for a in "${args[@]}"; do
    full+=" $a"
  done

  [[ "$full" == *\? && $argc -ge 2 ]] && return 0

  local first
  first="$(printf '%s' "${args[1]}" | tr '[:upper:]' '[:lower:]')"
  case "$first" in
    is|are|am|do|does|did|can|could|should|would|will|why|how|when|where|who)
      [[ $argc -ge 2 ]] && return 0
      ;;
  esac

  return 1
}

_ash_route_or_delegate() {
  local cmd="$1"
  shift
  if _ash_should_route "$cmd" "$@"; then
    ash "$cmd" "$@"
    return $?
  fi
  command "$cmd" "$@"
}

_ash_route_or_delegate_builtin() {
  local builtin_name="$1"
  shift
  if _ash_should_route "$builtin_name" "$@"; then
    ash "$builtin_name" "$@"
    return $?
  fi
  builtin "$builtin_name" "$@"
}

what()  { _ash_route_or_delegate what  "$@"; }
What()  { _ash_route_or_delegate What  "$@"; }
which() { _ash_route_or_delegate which "$@"; }
Which() { _ash_route_or_delegate Which "$@"; }
who()   { _ash_route_or_delegate who   "$@"; }
Who()   { _ash_route_or_delegate Who   "$@"; }
where() { _ash_route_or_delegate_builtin where "$@"; }
Where() { _ash_route_or_delegate_builtin where "$@"; }

test()  { _ash_route_or_delegate_builtin test "$@"; }
Test()  { _ash_route_or_delegate_builtin test "$@"; }
type()  { _ash_route_or_delegate_builtin type "$@"; }
Type()  { _ash_route_or_delegate_builtin type "$@"; }
Time()  { _ash_route_or_delegate Time "$@"; }
```

In zsh, `time` is also reserved syntax, so only `Time ...` can be wrapped.

### Troubleshooting

- Prompt still hits system command:
  - Ensure wrapper functions are loaded (`type what`, `type What`).
- `time ...` prompt does not route to ash:
  - `time` is reserved syntax in bash/zsh. Use `Time ...` or `ash "time ..."`.
- Prompt unexpectedly routed to command instead of ash:
  - Add a trailing `?` or use a recognized first-arg question word.
- Legit command routed to ash:
  - Add a flag (`-x`) or explicit path argument, or tighten wrappers for your workflow.
- zsh users seeing no fallback:
  - Use `command_not_found_handler`, not bash's `command_not_found_handle`.

## Manual usage

```bash
./ash "how do I list files by size?"
```

## License

MIT. See `LICENSE`.
