# ash

`ash` is a small Go executable for shell `command_not_found_handle()` workflows.
It sends the command text to an Ollama model using the Ollama Chat API and prints
the assistant response.

## Features

- Uses environment variable `AI` with format `ollama://host[:port]/model`
- Defaults Ollama port to `11434` when omitted
- Uses Ollama Chat API (`/api/chat`)
- Supports Ollama native tool calling (`tools`, `tool_calls`, `role=tool` messages)
- Shows an ANSI-friendly thinking indicator while waiting for Ollama
- Supports `Ctrl-C` to abort an in-flight Ollama request
- Keeps chat history across calls
- Uses `.ash_system` as system prompt when present
- Supports emoji input/output (UTF-8)
- Renders markdown output to terminal styling with ANSI fallback safety
- Can execute allowlisted Unix commands and `python3` as AI tools

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
```

- `make lint` runs `golangci-lint` checks across the module
- `make test` runs `go test ./...`
- `make install` runs `go install ./...`
- `make verify` runs strict checks (tests, race, coverage gate, vet, staticcheck)

Contributor note: run `make lint test` before submitting changes.

## Configure

Set the Ollama target:

```bash
export AI="ollama://localhost/llama3.1:latest"
```

Examples:

```bash
export AI="ollama://localhost/mistral:latest"
export AI="ollama://10.0.0.20:11434/llama3.1:latest"
```

Optional system prompt file (checked in current directory first, then `$HOME`):

```text
.ash_system
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

When enabled, `ash` logs:

- full JSON payload sent to Ollama `/api/chat`
- Ollama response status and body
- tool loop iteration decisions
- tool invocation name/arguments and returned result payload

## Tool Execution

`ash` publishes two tools to Ollama on each request:

- `run_unix_command`: executes one allowlisted Unix executable with direct argv (no shell)
- `run_python3`: executes `python3 -c <code>` with optional argv

Tool execution is local to your machine. Use a narrow allowlist.

### Allowlist configuration

Set allowlisted Unix executables with one of these methods:

1. Environment variable override:

```bash
export ASH_TOOL_ALLOWLIST="ls,ps,man,osascript"
```

2. Config file `.ash_tools` (checked in current directory first, then `$HOME`):

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
