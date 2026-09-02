# Architecture

## Project layout

- [cmd/ash](cmd/ash): thin `main` package; parses nothing itself, just calls `app.Run(...)`.
- [cmd/ash-broker](cmd/ash-broker): the standalone broker daemon binary.
- [internal/app](internal/app): the ash CLI implementation (see "Key subsystems" below). This
  is intentionally still one package rather than split by domain (provider, chat, metrics,
  attachments, shell install) — those areas share wire-level types and context-threaded state
  that would need a larger redesign to split safely; see "Known interim state" below.
- [internal/model](internal/model): shared wire-level chat/tool types (`Message`, `Attachment`,
  `ChatResponse`, `ToolDefinition`, etc.), kept dependency-free so a future `internal/provider`
  package could depend on them without importing `internal/app`. `internal/app` currently
  re-exposes these as lowercase type aliases (`type message = model.Message`) so none of its
  existing call sites needed to change.
- [internal/workspace](internal/workspace): pure `Root(home string) string` path-join helper
  for the `~/.ash` workspace directory layout; `internal/app`'s `ashWorkspaceDir()` still
  resolves `$HOME` itself (via its existing test-stub var) and delegates only the path join.
- [internal/uistyle](internal/uistyle): lipgloss-based styled print helpers for interactive
  install prompts (`PrintMenuTitle`, `PrintSuccess`, etc.) — the one fully standalone piece of
  the install UI, with no dependency on anything else in `internal/app`.
- [internal/brokerproto](internal/brokerproto): wire protocol shared by the `ash` client and
  the `ash-broker` server.

## Runtime overview

The CLI entry point in [internal/app/ash.go](internal/app/ash.go) initializes configuration, loads the system prompt, resolves history, and runs the tool loop. The main control flow is:

1. parse environment-based configuration
2. load prompt/history/allowlist state
3. run the chat/tool loop
4. render the final assistant reply

## Key subsystems

- [chat.go](internal/app/chat.go): request/response handling (`chat`/`chatStream`) and dispatch to the per-provider adapter registry; the `message`/`chatResponse`/`attachment` data model itself lives in [internal/model](internal/model).
- [ai_transport.go](internal/app/ai_transport.go): shared `http.RoundTripper` (`ashRoundTripper`) used by every SDK-based adapter's `http.Client` — implements broker-fallback, retry/backoff, and metrics once, instead of per-adapter.
- [runner.go](internal/app/runner.go): tool-loop orchestration, task state, and observation tracking.
- [ash_attachments.go](internal/app/ash_attachments.go): `--attach` CLI flag parsing, attachment loading/MIME-sniffing/size limits, and writing model-returned attachments to disk.
- [tools.go](internal/app/tools.go): tool definitions and local tool execution shim.
- What used to be one ~1860-line `support.go` is now split by topic (same `app` package, no
  exported-surface changes): [support.go](internal/app/support.go) (core types and the
  package-level test-stub var seam), [exec.go](internal/app/exec.go) (tool/subagent process
  execution), [security.go](internal/app/security.go) (prompt-injection detection and
  sanitization), [debuglog.go](internal/app/debuglog.go) (structured + rotating debug
  logging), [history.go](internal/app/history.go) (history persistence and retention
  cleanup), [scheduler.go](internal/app/scheduler.go) (cron/launchd scheduling for
  `schedule_future_prompt`), [paths.go](internal/app/paths.go) (workspace/scratch path
  resolution), [output.go](internal/app/output.go) (markdown rendering, spinner, output
  formatting).
- [install.go](internal/app/install.go): shell wrapper installation and workspace initialization.
- [ai_autoconfig.go](internal/app/ai_autoconfig.go): cloud provider/local server auto-detection and model-listing prompts used by `ash install`.
- [provider.go](internal/app/provider.go): provider adapter registry (`ollama`, `openai`, `google`, `anthropic`, `cohere`, `bedrock`) and the adapter interface tiers: `providerAdapter` (base), `byteProviderAdapter` (raw HTTP, used only by `ollamaAdapter`), `sdkProviderAdapter` (official SDK-based `Send`, used by every other adapter), `streamingProviderAdapter` (adds `SendStream`, currently implemented only by [openai.go](internal/app/openai.go)).
- [openai.go](internal/app/openai.go), [google.go](internal/app/google.go), [anthropic.go](internal/app/anthropic.go), [cohere.go](internal/app/cohere.go), [bedrock.go](internal/app/bedrock.go): per-provider adapters built on each vendor's official Go SDK, all sharing the `ai_transport.go` HTTP client so retries/broker-fallback/metrics stay consistent across providers.
- [broker.go](internal/app/broker.go): client-side broker connection logic used by the main `ash` binary; the broker server itself lives in the separate [cmd/ash-broker](cmd/ash-broker) binary (see [internal/brokerproto](internal/brokerproto) for the shared wire types) so the broker has no dependency on any AI provider SDK.
- [snooze.go](internal/app/snooze.go): persistent expiry state for pausing automatic shell routing.

## Known interim state

`internal/app` is a single package holding several domains that would ideally be their own
packages, kept together for now because they share state across a would-be package boundary:

- Provider adapters directly call `ai_transport.go`'s `newAshHTTPClient()` and
  `requestIDFromContext()`, which are themselves wired into `executionMetrics`, the broker
  client (`broker.go`), and retry/timeout config — this is core app transport infrastructure,
  not a provider-only concern. Splitting provider adapters into their own package cleanly
  requires threading an injected `*http.Client`/request-ID dependency through the adapter
  interface first (a moderately-sized redesign, deliberately deferred; see git history for the
  2026-09-01 reorganization notes on why `internal/provider` was not extracted this pass).
- `executionMetrics` (metrics.go) is threaded through `context.Context` across `chat.go`,
  `runner.go`, `tools.go`, and `ash.go` — a control-flow coupling, not just a data dependency.
- Several files (`snooze.go`, `python_env.go`, `ash_attachments.go`) rely on package-level
  test-stub function variables (`osReadFile`, `timeNow`, `execCommandContext`, etc., now in
  `support.go`/`exec.go`) for test seams — a pattern that works within one package but doesn't
  cross package boundaries cleanly.

This is intentional scope control for the cmd/+internal/ layout migration, not an oversight.
A future pass could introduce a small `Workspace`/`Clock` interface (a start already exists in
[internal/workspace](internal/workspace)) and an injected HTTP client/logger for provider
adapters, to unblock splitting these into real packages (e.g. `internal/provider`,
`internal/metrics`) without import cycles or duplicated test seams. New code should avoid
adding to the global-stub-var pattern; prefer constructor/parameter-injected dependencies.

## Operational notes

- History is stored under the user workspace directory and is retained with a bounded cleanup policy.
- Shell integrations check `~/.ash/.ash_snooze_until` before automatic wrapper and command-not-found routing. Direct `ash` invocations bypass this check.
- Debug logging can be enabled with ASH_VERBOSE and optionally written to a rotating log file.
- Tool execution is restricted by allowlist and path containment rules. Ash resolves the allowlist once per request, uses it for enforcement and tool schemas, and renders eligible managed scripts through the internal `$TOOLS_DIR_LIST` prompt substitution.
- `ash install` auto-configures `AI_ENDPOINT`/`AI_MODEL`/`AI_AUTH_TOKEN`: it checks for an already-set cloud provider API key (12 providers, see [ai_autoconfig.go](internal/app/ai_autoconfig.go)), then a running local inference server, then falls back to an interactive numbered menu; AWS Bedrock is intentionally excluded (see [issue #4](https://github.com/Jonconradt/ash/issues/4)).
