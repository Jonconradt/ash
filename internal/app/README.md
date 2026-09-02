# internal/app

Directory contents for `internal/app`, the ash CLI implementation. For the full
architecture writeup (data flow, provider adapters, known interim state), see
[ARCHITECTURE.md](../../ARCHITECTURE.md) at the repo root.

This is currently a single package (`package app`); the groupings below are by
file, not by sub-package.

## Entry point and dispatch

- `ash.go` — CLI arg parsing/dispatch, top-level `Run()`, shared constants.

## Chat / AI providers

- `chat.go` — `chat`/`chatStream`, provider adapter dispatch (wire types live in `internal/model`).
- `ai_transport.go` — shared `http.RoundTripper` (retry, broker-fallback, metrics) for every SDK-based adapter.
- `ai_autoconfig.go` (+ `ai_autoconfig_test.go`) — cloud/local provider auto-detection and model-listing prompts for `ash install`.
- `config.go` (+ `config_test.go`) — `AI_*` env var parsing, provider detection, system prompt/allowlist loading.
- `provider.go` — provider adapter registry and interface tiers.
- `openai.go`, `anthropic.go`, `google.go`, `cohere.go`, `bedrock.go`, `ollama.go` — per-provider adapters.
- `broker.go` (+ `broker_test.go`) — client-side connection-reuse broker (see `cmd/ash-broker`).

## Tool execution

- `runner.go` — tool-loop orchestration, task state, observation tracking.
- `tools.go` — tool definitions and the local tool execution shim.
- `exec.go` — subprocess/sub-agent execution helpers (moved out of `support.go`).
- `scheduler.go` — cron/launchd scheduling for `schedule_future_prompt`.
- `paths.go` — workspace/scratch path resolution.

## Support / cross-cutting

- `support.go` — core shared types and the package-level test-stub var seam (`osReadFile`, `timeNow`, etc.).
- `security.go` — prompt-injection detection and untrusted-content sanitization.
- `debuglog.go` — structured + rotating debug logging (`ASH_VERBOSE`).
- `history.go` — conversation history persistence and retention cleanup.
- `metrics.go` (+ `metrics_test.go`, `metrics_scratch_test.go`) — execution metrics and the verbose-mode dashboard.
- `output.go` (+ `output_format_test.go`) — markdown rendering, spinner, assistant output formatting.
- `route.go` (+ `route_test.go`) — shell command-not-found routing decisions.
- `snooze.go` — persistent expiry state for pausing automatic shell routing.
- `setup.go` (+ `setup_test.go`) — user-facing setup/config error messages.
- `python_env.go` (+ `python_env_test.go`, `python_execution_test.go`) — managed Python venv provisioning for bundled tools.

## Install / shell integration

- `install.go`, `install_targets.go` — shell wrapper installation orchestration and per-shell registry.
- `bash_install.go`, `zsh_install.go`, `fish_install.go` (+ `bash_route_test.go`) — per-shell install targets.
- `bootstrap_assets.go` — `//go:embed` of `ash_bootstrap/` (see [ash_bootstrap/README.md](ash_bootstrap/README.md)).

## Attachments and updates

- `ash_attachments.go` (+ `ash_attachments_test.go`) — `--attach` flag parsing, attachment loading/MIME-sniffing.
- `update.go` (+ `update_test.go`) — self-update (`ash update`), Sigstore verification.

## Platform-specific

- `process_group_unix.go` — process group setup/teardown (build tag: unix-like OSes).

## Tests and fixtures

- `*_test.go` files alongside the code they test, plus `ash_test.go`/`ash_golden_test.go`/`prompt_test.go`/`history_test.go`/`request_id_test.go`/`scratch_exec_test.go` for broader CLI-level and golden-output tests.
- `testdata/` — golden test fixtures.
