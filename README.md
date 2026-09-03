# ash

**[Visit the ash website](https://jonconradt.github.io/ash/)** for a quick introduction and installation links.

Ever wanted to ask an AI a question in the middle of working in a terminal? Don't want to launch another app just to ask one question? ASH extends the bash and zsh shells to support in-line AI prompting. When the shell does not understand your command it passes it to the AI as a prompt.

```ash
$:~ jon$ whoami
jon
$:~ jon$ When is the next full moon?

  The current moon phase is a first quarter moon (represented as 🌓 with an illumination of 8%).

  Based on the lunar cycle, a full moon occurs approximately 14-15 days after the first quarter. Given today's date is August 20, 2026, the next full moon is expected to occur around September 3-4, 2026.
```

What if you want the AI to investigate a problem? The .ash_tools file is an allow list of unix commands the AI can use (and pipe together) to accomplish investigations.

What if you have a more complex question, something that needs to be computed? If you allow ash to expose Python the AI can write temporary scripts, or use scripts in the ~/.ash/tools directory to run complex processes.

What if the AI needs temporary notes, plans, script fragments, or small working files? Ash includes a managed scratch workspace under `~/.ash/scratch/<session-id>/` that is automatically scoped to the current session. The AI never needs to remember or pass the session ID; ash resolves it automatically. Scratch files are kept inside the managed workspace and are cleaned up on exit when a directory is older than 48 hours and has not been accessed in the last 24 hours.

What if you want something to happen on a schedule or in the future? Ash can create systems jobs that run once or recur.

Natural-language routing recognizes modern question starters including `what`,
`which`, `who`, `whom`, `whose`, `where`, `when`, `why`, and `how`, together
with common auxiliary and modal forms such as `is`, `are`, `do`, `can`, `will`,
`may`, `might`, `must`, `shall`, and `ought`. Common contractions such as
`what's`, `who's`, `where's`, and `how's` are also recognized. Archaic forms
such as `whence` and `whither` are recognized as well.

On macOS, a natural-language request beginning with `say` can use the native
text-to-speech command. For example, `say something witty` sends the request to
ash and speaks the successful response. Native command forms such as
`say -v Alex hello` and `say --version` continue to run normally. Ash errors and
diagnostics remain text, and the feature falls back to normal text output when
the native `say` command is unavailable. Use text output when speech would be
inappropriate for accessibility, privacy, or automation reasons by setting
`ASH_SAY_TEXT=1`.

What if I want to run a unix command that looks like a prompt, e.g. which ash? You can snooze ash with "ash snooze 5m" and it will not interpret your command line as a prompt.

What if I am on MacOS and I want the results in my clipboard? Add pbcopy to .ash_tools. If you forget, ask the AI because it knows what to do.

Where is the system prompt? Put your system prompt in ~/.ash/.ash_system. It supports replacement of environment variables.

Does this support Ollama, OpenAI, Google, Anthropic? Yes, plus Azure OpenAI, Mistral, Cohere, Groq, xAI, DeepSeek, Together AI, OpenRouter, HuggingFace, and AWS Bedrock. During install, ash auto-detects an already-configured provider key or a local server; otherwise it shows a numbered menu so you can pick a provider (or enter a custom URL) and supply your app key. The app key will be added to ~/.ash/.ash_env

Is this thing secure and safe? It really depends on how bold you are. It runs as your user so it can read your files, but it is limited in the commands it can execute, but it can execute python (if you allow it). If you allow curl or wget and are running a naive model you could end up executing more than you wanted.

## Features

- Uses `AI_ENDPOINT` and `AI_MODEL` to target local or cloud provider endpoints
- Uses bearer authentication automatically when `AI_AUTH_TOKEN` is set
- Auto-detects provider adapters (`ollama`, `openai`, `google`, `anthropic`, `cohere`, `bedrock`) from endpoint
- `ash install` auto-configures from an already-set cloud provider API key or a running local inference server, and offers a numbered model picker
- Supports optional `AI_PROVIDER` override for advanced routing control
- Uses provider-native tool calling through per-provider adapters
- Can attach files (images or documents) to a request with `--attach <path>` (repeatable); currently encoded on the wire for OpenAI-compatible providers
- Optional streaming responses via `ASH_STREAM` for providers whose adapter supports it (currently OpenAI only)
- Enables provider-native caching by default when supported
- Shows an ANSI-friendly thinking indicator while waiting for the AI response
- Supports `Ctrl-C` to abort an in-flight request
- Keeps chat history across calls
- Uses `~/.ash/.ash_system` as the canonical system prompt file when present
- Always prepends current local date/time to the system prompt sent to the model
- Supports emoji input/output (UTF-8)
- Renders markdown output to terminal styling with ANSI fallback safety
- Can execute allowlisted Unix commands and `python3` as AI tools
- Can schedule one-off and recurring prompt invocations via `at` and `crontab`
- Maintains recurring-job inventory and management through AI tools
- Provides persistent AI workspace file access under `~/.ash`
- Provides a session-scoped scratch workspace under `~/.ash/scratch/<session-id>/` for temporary notes, plans, and helper files
- Automatically cleans stale scratch directories older than 48 hours with no access in the last 24 hours

## Build

```bash
go build -ldflags "-X main.ashVersion=v0.17.1 -X main.ashCommit=$(git rev-parse HEAD) -X main.ashDevelopmentBuild=true" -o ash .
```

Build metadata is injected through linker flags. `make build` uses the latest
release tag and Git HEAD, marking the dashboard as a development build (for
example, `ASH v0.17.1 (dev:abcd) EXECUTION SUMMARY`). Release builds pass the
tag version and Git HEAD without the development suffix.

## Make Targets

```bash
make lint
make test
make install
make verify
make sync-route-words
make version
make release
make release RELEASE_VERSION=v1.2.3
```

- `make lint` runs `golangci-lint` checks across the module
- `make security` runs `gosec` and `govulncheck` for security scanning
- `make test` runs `go test ./...`
- `make install` runs `go install ./...`
- `make verify` runs strict checks (tests, race, coverage gate, vet, staticcheck, gosec, govulncheck)
- `make sync-route-words` regenerates the ambiguous-word block in the shell assets from
  `internal/app/ash_bootstrap/route_words.txt`, the canonical list. Edit only that file; a test fails if
  the generated blocks are stale
- `make version` runs quality checks and builds installer artifacts for the selected host/targets
- `make release` runs quality checks, builds an arm64 macOS `.pkg`, validates it,
  writes a SHA-256 checksum to `dist/release/`, generates release notes from the
  Git history through `ash`, creates an annotated release tag containing those
  notes, and pushes it to `origin`
- Release publishing also creates `SHA256SUMS` and a Sigstore bundle. The
  updater requires both the signed manifest and the matching SHA-256 digest.
- Release-note generation requires `AI_ENDPOINT` and `AI_MODEL`. Make supplies
  the Git history to `ash`; `ash` does not invoke Git. The tagged notes are
  published as the GitHub Release body.
- If `RELEASE_VERSION` is omitted, the latest stable tag is used as the source
  and the next version is derived as `v<major>.<minor+1>.0`.

Contributor note: run `make lint test` before submitting changes.

## Install on Linux, macOS, or FreeBSD

Install the latest release with the verified one-liner:

```bash
curl -fsSL https://jonconradt.github.io/ash/install.sh | sh
```

The installer supports Linux, macOS, and FreeBSD on `amd64` and `arm64`. It verifies the
download against the release `SHA256SUMS` manifest and installs `ash` to
`~/.local/bin` without requiring `sudo`. Set `ASH_INSTALL_DIR` to choose a
different destination when needed.

The installer runs `ash install` for the detected shell. Bash, zsh, and Fish are
detected; unknown shells default to bash. Fish uses its standard
`$XDG_CONFIG_HOME/fish/config.fish` path (defaulting to `~/.config/fish/config.fish`).
`ash install` adds `~/.local/bin` to `PATH` from the managed `~/.ash/.ash_env`
file, so restart the shell or source its rc file afterward.

Bundled Python tools use an isolated virtualenv at `~/.ash/venv`; `ash install`
waits for its dependencies, including `yfinance`, to be installed. Debian and
Ubuntu packages declare `python3` and `python3-venv` as dependencies. When
installing from the tarball on another platform, install Python 3 with venv
support before running `ash install`.

Native packages remain available from the [latest GitHub release](https://github.com/Jonconradt/ash/releases/latest): macOS `.pkg`, and Linux `.deb`
or `.rpm` packages. FreeBSD ships as a `.tar.gz` only; install `bash` with
`pkg install bash` first. Configure `AI_ENDPOINT` and `AI_MODEL` after installation
as described below.

### Fish support

Run `ash install --shell fish` to install Fish integration. It uses Fish's native
`fish_command_not_found` hook, shares the connection broker and snooze behavior
with bash/zsh, and installs compatible natural-language collision wrappers.

Fish reserves its lowercase `test` and `type` builtins, so those commands cannot
be wrapped. Use `Test ...` or `Type ...` for ash intent, or invoke `ash` directly.
Ksh93 is not supported: it has no generic command-not-found hook; its `FPATH`
mechanism only autoloads a file whose name matches an unknown command.

## Release Process

Canonical publishing is tag-driven in GitHub Actions.

1. Run `make release RELEASE_VERSION=v1.2.3` (or omit `RELEASE_VERSION` to auto-derive).
2. `make release` creates and pushes the release tag to `origin`.
3. The `release` workflow runs `make version` in macOS, Linux, and FreeBSD packaging jobs.
4. GitHub Release assets are published automatically:

- `ash-v1.2.3-darwin-amd64.pkg`
- `ash-v1.2.3-darwin-arm64.pkg`
- `ash-v1.2.3-linux-amd64.deb`
- `ash-v1.2.3-linux-arm64.deb`
- `ash-v1.2.3-linux-amd64.rpm`
- `ash-v1.2.3-linux-arm64.rpm`
- `ash-v1.2.3-<os>-<arch>.tar.gz`
- matching `.sha256` files for each artifact
- `SHA256SUMS` and `SHA256SUMS.sigstore.json`

Installer man pages are included in release artifacts:

- macOS `.pkg`: `/usr/local/share/man/man1/ash.1`
- Linux `.deb`/`.rpm`: `/usr/share/man/man1/ash.1`
- `.tar.gz`: `usr/share/man/man1/ash.1`

On some macOS setups, `/usr/local/share/man` may not be in the default `MANPATH`.
If `man ash` does not resolve after install, run:

```bash
man -M /usr/local/share/man ash
```

For local maintainer checks without publishing, run:

```bash
make version RELEASE_VERSION=v1.2.3
```

Requirements for local packaging:

- macOS with `pkgbuild` and `pkgutil` available
- Linux packaging requires `fpm` (for `.deb`/`.rpm`) plus `dpkg-deb`/`rpm` for validation
- Go toolchain installed

The package is currently unsigned and not notarized by design.

## Configure

Running `ash install` automatically configures the AI endpoint, model, and auth token when it can:

1. If a known cloud provider API key is already set in your environment (for example `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GEMINI_API_KEY`/`GOOGLE_API_KEY`, `AZURE_OPENAI_API_KEY` + `AZURE_OPENAI_ENDPOINT`, `MISTRAL_API_KEY`, `COHERE_API_KEY`, `GROQ_API_KEY`, `XAI_API_KEY`, `DEEPSEEK_API_KEY`, `TOGETHER_API_KEY`, `OPENROUTER_API_KEY`, or `HF_TOKEN`), ash uses it directly. If more than one is set, you're prompted to pick which one to use.
2. Otherwise, ash probes for a local inference server (Ollama, LM Studio, llama.cpp/MLX/LocalAI, vLLM, text-generation-webui, Jan, or GPT4All) on its default port.
3. If neither is found, you're shown a numbered menu of cloud providers (plus the option to enter a custom URL) and prompted for an API key.

In every case, once an endpoint and key are known, ash queries the endpoint's model-listing API and lets you pick the model from a numbered menu (falling back to free-text entry if listing isn't supported). The result is written to `~/.ash/.ash_env`, which the managed install block sources from your `.bashrc`/`.zshrc`.

To configure manually instead, set a local Ollama target:

```bash
export AI_ENDPOINT="http://localhost:11434"
export AI_MODEL="llama3.1:latest"
```

For Ollama, keep the model loaded after `ash` exits to avoid the next invocation paying the model-load cost:

```bash
export OLLAMA_KEEP_ALIVE=-1
```

Restart Ollama after changing this setting. Keeping a model resident uses its memory between invocations; use a duration such as `30m` instead of `-1` when that tradeoff is preferable.

Cloud example (authenticated):

```bash
export AI_ENDPOINT="https://ollama.example.com"
export AI_MODEL="llama3.1:latest"
export AI_AUTH_TOKEN="<token>"
```

Optional advanced overrides:

```bash
export AI_PROVIDER="openai"   # optional: ollama|openai|google|anthropic|gemini
export AI_CACHE="off"         # optional: on by default when provider supports native caching
export ASH_STREAM="off"       # optional: on by default; stream responses when the provider adapter supports it (currently OpenAI only)
```

Pipeline examples:

```bash
echo "summarize the current directory" | ash
cat prompt.txt | ash
```

Common `AI_ENDPOINT` values:

- Ollama: `http://localhost:11434`
- OpenAI: `https://api.openai.com/v1`
- Anthropic: `https://api.anthropic.com/v1`
- Google Gemini (OpenAI-compatible): `https://generativelanguage.googleapis.com/v1beta/openai/`
- Azure OpenAI (OpenAI-compatible): your `AZURE_OPENAI_ENDPOINT` value
- Mistral (OpenAI-compatible): `https://api.mistral.ai/v1`
- Cohere (OpenAI-compatible): `https://api.cohere.ai/compatibility/v1`
- Groq (OpenAI-compatible): `https://api.groq.com/openai/v1`
- xAI Grok (OpenAI-compatible): `https://api.x.ai/v1`
- DeepSeek (OpenAI-compatible): `https://api.deepseek.com/v1`
- Together AI (OpenAI-compatible): `https://api.together.xyz/v1`
- OpenRouter (OpenAI-compatible): `https://openrouter.ai/api/v1`
- HuggingFace Router (OpenAI-compatible): `https://router.huggingface.co/v1`
- Cohere (native SDK): `https://api.cohere.com`
- AWS Bedrock (native SDK, Converse API): `https://bedrock-runtime.<region>.amazonaws.com` — uses the AWS credential chain (env vars, `~/.aws/credentials`, SSO, instance role) for SigV4 signing; `AI_AUTH_TOKEN` is still required by ash's generic cloud-endpoint validation but is unused by Bedrock itself, so set any placeholder value.

Notes:

- Cloud endpoints are inferred when the host is not `localhost`, `127.0.0.1`, a private/LAN address (RFC1918 IPv4, IPv6 ULA), or a `.local` mDNS hostname. Set `ASH_STRICT=1` to treat only `localhost`/loopback as non-cloud (private/LAN hosts then require `https` and `AI_AUTH_TOKEN` like any other cloud endpoint).
- Cloud endpoints must use `https` and require `AI_AUTH_TOKEN`; tokens are sent as bearer auth.
- Provider detection is automatic by endpoint host/path; use `AI_PROVIDER` only when you need to override.
- `AI_CACHE` defaults to enabled. Use `AI_CACHE=off` to disable provider-native caching.
- Legacy `AI=ollama://...` configuration is no longer supported.

### Complete environment variable reference

- `AI_ENDPOINT` (required): Base URL for the chat API endpoint.
- `AI_MODEL` (required): Model name sent to the endpoint.
- `AI_AUTH_TOKEN` (optional): When set, sent as a bearer token. Required for cloud endpoints.
- `AI_PROVIDER` (optional): Override auto-detected provider (`ollama`, `openai`, `google`, `gemini`, `anthropic`).
  Also accepts `cohere` and `bedrock` for the native SDK-based adapters (auto-detected from `api.cohere.com`/`bedrock-runtime.*.amazonaws.com` when not set explicitly).
- `AI_CACHE` (optional): Enable or disable provider-native caching (`true/false`). Default `true`.
- `ASH_ALWAYS_OPENAI_API` (optional): When true-like, routes the `ollama` provider through the OpenAI-compatible SDK adapter instead of its hand-rolled implementation. Default on; set to a falsy value (e.g. `0`/`off`) to use the hand-rolled ollama adapter.
- `ASH_STREAM` (optional): When true-like, streams the response from providers whose adapter supports it (currently OpenAI only). Default on; set to a falsy value (e.g. `0`/`off`) to disable. Has no visible effect on output today (see [docs/ash.1](docs/ash.1)).
- `ASH_ATTACHMENT_MAX_BYTES` (optional): Max size in bytes for a file passed via `--attach`. Default 10485760 (10 MiB).
- `AI` (legacy, unsupported): Deprecated and rejected; use `AI_ENDPOINT` and `AI_MODEL`.
- `AI_TIMEOUT` (optional): AI request timeout. Default `3m`.
- `ASH_HISTORY_MAX` (optional): Max retained history messages per key. Default `40`.
- `ASH_TOOL_ALLOWLIST` (optional): Comma-separated allowlisted executables for `run_unix_command`; it overrides `.ash_tools` and does not expand Ash internal tokens.
- `ASH_TOOL_TIMEOUT` (optional): Timeout for local tool execution. Default `15s`.
- `ASH_TOOL_OUTPUT_MAX` (optional): Max bytes captured from tool output. Default `8192`.
- `ASH_MAX_TOOL_ITERS` (optional): Maximum AI tool-loop iterations. Default `16`.
- `ASH_MAX_AGENTS` (optional): Maximum sub-agents generated by one parent process. Default `6`.
- `ASH_TASK_MAX` (optional): Maximum execution tasks derived from a request. Default `6`.
- `ASH_RELEVANCE_WINDOW` (optional): Number of recent tool observations included in execution state. Default `4`.
- `ASH_TASK_STALL_ROUNDS` (optional): Maximum stalled assistant rounds before stopping execution. Default `2`.
- `ASH_RETRY_MAX_ATTEMPTS` (optional): Max retry attempts for retryable AI failures. Default `3`.
- `ASH_RETRY_BASE_DELAY` (optional): Base retry backoff delay. Default `250ms`.
- `ASH_RETRY_MAX_DELAY` (optional): Maximum retry backoff delay. Default `2s`.
- `ASH_VERBOSE` (optional): Enable verbose debug logging and an execution dashboard printed before exit (`1`, `y`/`yes`, `true`, `on`, or `debug`, case-insensitive). The dashboard reports stage timings, tool-call count, total input/output tokens when available, and total realtime.
- `ASH_LOG_FILE` (optional): File path for verbose debug logs.
- `ASH_LOG_MAX_BYTES` (optional): Max log size before rotation when `ASH_LOG_FILE` is used. Default `1048576` (1 MiB).
- `ASH_LOG_FORMAT` (scheduler/internal): Log format set to `json` for scheduled invocations.
- `ASH_BROKER_SOCKET`, `ASH_BROKER_TOKEN`, and `ASH_BROKER_LEASE` (internal): Ephemeral per-shell broker settings. They are created by the installed Bash/zsh/Fish wrappers and should not be persisted or copied into scheduled jobs.
- `SESSION_ID`: Session identifier used for history and scheduled log naming (generated when missing in interactive runs).
- `ASH_SCHEDULED_TASK` (internal): Set by scheduled invocations to mark task execution context.
- `ASH_CHILD_AGENT` (internal): Marks a one-level child agent; child agents cannot create or schedule ash agents.
- `SHELL`: Used by `ash install` and install recommendations to detect bash/zsh.
- `HOME`: Determines ash workspace root under `$HOME/.ash`.
- `PATH`: Used to locate executables for tool commands and helper utilities.

### Connection reuse

On macOS, Linux, and FreeBSD, the installed Bash, zsh, and Fish wrappers lazily start one unprivileged broker per interactive shell. The wrapper passes its process ID to the broker, which remains available until that shell exits. Subshells reuse their parent shell's broker; independent shells have independent brokers.

The broker keeps a bounded HTTPS connection pool alive across separate `ash` processes and does not apply a local idle timeout to those connections. A provider can still close an idle connection; the next prompt opens a new connection automatically. Broker use is transparent and has a direct HTTPS fallback. Its private Unix socket and capability token are ephemeral shell state and are not written to cron, launchd, or other persistent scheduler configuration. Scheduled invocations therefore use direct HTTPS unless they explicitly inherit a live broker environment.

Optional canonical system prompt file:

```text
$HOME/.ash/.ash_system
```

Example `.ash_system` content:

```text
You are a concise shell assistant. Keep answers short and practical. 🙂
```

`$TOOLS_DIR_LIST` and `$IF_PYTHON_AVAILABLE` are reserved internal Ash substitutions, not environment variables. Ash computes them before expanding ordinary environment variables. `$TOOLS_DIR_LIST` lists only allowed, regular files in `~/.ash/tools/` that have both a read bit and an execute bit set.

History is stored in:

```text
$HOME/.ash/history/$SESSION_ID.json
```

Scheduled invocations write task-specific history files:

```text
$HOME/.ash/history/task_$SESSION_ID.json
```

Legacy `$HOME/.ash_history.json` is ignored.

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

Pause automatic shell prompt processing when you need a quiet period:

```bash
ash snooze
ash snooze 30s
ash snooze 10m
ash snooze off
```

Update a user-local installation from the latest stable GitHub release:

```bash
ash update
ash update --version v1.2.3
ash update --yes
ash update --skip-customized
```

The updater supports macOS, Linux, and FreeBSD on amd64 and arm64 and installs to
`~/.local/bin/ash`, the same location used by the install script. It verifies the
Sigstore keyless signature for `SHA256SUMS`
against the `Jonconradt/ash` release workflow, then verifies the selected
archive's SHA-256 digest. A missing or mismatched signature or digest is a hard
failure and leaves the existing installation unchanged.

Customized files under `~/.ash` are skipped by default. Use `--yes` to replace
them, or `--skip-customized` to make the default explicit. The updater never
overwrites system-managed installations and reports when `~/.local/bin` must be
placed earlier on `PATH`.

`ash snooze` pauses processing for five minutes by default. Custom durations use
Go duration syntax, such as `30s`, `10m`, or `1h`. The snooze is shared by the
installed shell integrations and affects automatic routing only; explicit
`ash ...` commands continue to work. The expiry is stored in
`~/.ash/.ash_snooze_until`.
After updating an existing installation, reload the shell startup file once
(`source ~/.bashrc` or `source ~/.zshrc`) so the current shell loads the
snooze-aware integration.

Installer implementation is split per shell target for maintainability:

- `internal/app/bash_install.go`
- `internal/app/zsh_install.go`

When enabled, `ash` logs structured diagnostics with Go's standard-library `log/slog` and prints an execution timing dashboard before exit. Logs include bounded metadata such as request IDs, statuses, durations, and sizes, but do not include prompts, credentials, raw tool arguments, or raw tool output.

- provider and response status
- tool loop iteration decisions
- tool invocation names, statuses, and timing

## Tool Execution

`ash` publishes these tools to the configured provider on each request:

- `run_unix_command`: executes one allowlisted Unix executable with direct argv (no shell)
- `run_python3`: when a runnable Python interpreter is available and `ASH_STRICT` is off, executes either `python3 -c <code>` or a `.py` file in the current managed scratch session with optional argv
- `run_sub_agent`: delegates one focused task to a child ash process, subject to `ASH_MAX_AGENTS`
- `schedule_future_prompt`: schedules one prompt run via a user `launchd` LaunchAgent
- `schedule_recurring_prompt`: schedules recurring prompt runs via `crontab`
- `manage_recurring_jobs`: lists, cancels, modifies, and explains ash-managed recurring jobs
- `ash_read_workspace_file`: reads a file from `~/.ash`
- `ash_write_workspace_file`: writes a file in `~/.ash` and auto-updates `~/.ash/inventory.md`

Tool execution is local to your machine. Use a narrow allowlist.

`run_python3` is separate from the Unix executable allowlist. It is published only when ash resolves its selected interpreter (`ASH_PYTHON`, the managed virtualenv, or system `python3`) and strict mode is disabled. To execute a generated script, write it with `ash_write_scratch_file` and pass the returned `absolute_path` as `script_path`; ash accepts only `.py` files inside the current scratch session. `ASH_STRICT=1` removes the tool and blocks ash-managed bundled `.py` tools as defense in depth.

Sub-agents use the same ash executable, working directory, configuration, tools, and OS permissions. Their session IDs have the form `{parent-session-id}.{six-random-characters}`. Delegation is one level only: child agents cannot publish or invoke `run_sub_agent`, schedule ash, or directly invoke ash through the built-in tools. Control-C cancels the parent and terminates active child work. On Unix, ash places each child in a process group so cancellation and timeout terminate its descendants. Tool, script, file, piped, and child output is untrusted data and must not be treated as instructions or allowed to override the system prompt or user request. Arbitrary Python or shell programs may still launch processes independently; this feature is not a sandbox. Each ash process maintains its own HTTPS connection pool. A child process cannot reuse the parent process's live TLS connection, but retries within one process reuse its HTTP client and transport.

### Allowlist configuration

Set allowlisted Unix executables with one of these methods:

1. Environment variable override:

```bash
export ASH_TOOL_ALLOWLIST="ls,ps,man,osascript"
```

1. Canonical config file `$HOME/.ash/.ash_tools`:

```text
# one per line or comma-separated
ls
ps
man
osascript
```

If both are present, `ASH_TOOL_ALLOWLIST` wins.

The standalone `$TOOLS_DIR_LIST` line in `.ash_tools` is an internal Ash directive that allows eligible managed tool scripts. Replace that line with literal bare names for a fixed restrictive script policy. Install never silently broadens an existing custom policy; when interactive, it asks before adding newly bundled entries.

### Tool safety settings

Optional settings:

```bash
export ASH_TOOL_TIMEOUT=15s
export ASH_TOOL_OUTPUT_MAX=8192
export ASH_MAX_TOOL_ITERS=16
```

The Unix tool rejects risky shell-control argument patterns and always executes directly without shell interpolation.

### Scheduling behavior

- One-off scheduling uses a user `launchd` LaunchAgent under `~/.ash/launchagents/` with a per-invocation `com.user.gonetwork.<id>.plist` filename and loads it with `launchctl`.
- Recurring scheduling uses user `crontab` entries with ash metadata markers.
- Recurring-job management (`list`, `cancel`, `modify`, `explain`) operates only on ash-owned crontab entries.
- Scheduled runs capture prompt + working directory and replay a minimal environment allowlist (`AI_ENDPOINT`, `AI_MODEL`, auth/session variables, `HOME`, `PATH`, and selected ash config vars).
- One-off scheduled runs also enable verbose logging and write JSON debug logs to `~/.ash/logs/task_$SESSION_ID.log`, rotating the file at 1 MB.
- SESSION_ID is required for default log naming and is sanitized to alphanumeric characters for filename safety.

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

<https://ollama.com/search?c=tool>

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

Zsh does not use wrapper functions. Wrappers would shadow real builtins such as
`test`, `type`, and `which` inside scripts, and zsh expands globs before command
lookup, so a prompt ending in `?` fails with `no matches found` before any
wrapper can run.

Instead, `ash install --shell zsh` registers a ZLE `accept-line` widget. ZLE
exists only in interactive shells, so scripts and `zsh -c` keep stock zsh
behavior. On Enter the widget applies fork-free guards to the raw input buffer
and passes anything that looks like a real command straight through:

- multi-line buffers, pipelines, redirection, command substitution, assignments,
  paths, and flags are never routed
- a first word that resolves as a command, function, alias, builtin, or reserved
  word is passed through, unless it is one of the ambiguous words `at`, `for`,
  `in`, `test`, `time`, `type`, `what`, `where`, `which`, `who`, `write`
- ambiguous words are resolved by `ash route --check`, the shared heuristic

When the widget decides to route, it rewrites the buffer to `ash '<original>'`.
Quoting the whole line keeps globs, reserved words, and a trailing `?` away from
zsh, so `Tell me about Go?` and `time to go home` reach ash intact.

`command_not_found_handler` remains as a fallback if the widget fails to
register, and returns 127 in non-interactive shells.

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
