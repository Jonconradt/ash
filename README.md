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

## Manual usage

```bash
./ash "how do I list files by size?"
```

## License

MIT. See `LICENSE`.
