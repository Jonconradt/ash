# ash

`ash` is a small Go executable for shell `command_not_found_handle()` workflows.
It sends the command text to an Ollama model using the Ollama Chat API and prints
the assistant response.

## Features

- Uses environment variable `AI` with format `ollama://host[:port]/model`
- Defaults Ollama port to `11434` when omitted
- Uses Ollama Chat API (`/api/chat`)
- Shows an ANSI-friendly thinking indicator while waiting for Ollama
- Supports `Ctrl-C` to abort an in-flight Ollama request
- Keeps chat history across calls
- Uses `.ash_system` as system prompt when present
- Supports emoji input/output (UTF-8)
- Renders markdown output to terminal styling with ANSI fallback safety

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
export AI="ollama://localhost/llama3.1"
```

Examples:

```bash
export AI="ollama://localhost/mistral"
export AI="ollama://10.0.0.20:11434/llama3.1"
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
