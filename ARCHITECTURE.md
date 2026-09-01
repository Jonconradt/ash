# Architecture

## Runtime overview

The CLI entry point in [ash.go](ash.go) initializes configuration, loads the system prompt, resolves history, and runs the tool loop. The main control flow is:

1. parse environment-based configuration
2. load prompt/history/allowlist state
3. run the chat/tool loop
4. render the final assistant reply

## Key subsystems

- [chat.go](chat.go): request/response handling (`chat`/`chatStream`), the `message`/`chatResponse`/`attachment` data model, and dispatch to the per-provider adapter registry.
- [ai_transport.go](ai_transport.go): shared `http.RoundTripper` (`ashRoundTripper`) used by every SDK-based adapter's `http.Client` — implements broker-fallback, retry/backoff, and metrics once, instead of per-adapter.
- [runner.go](runner.go): tool-loop orchestration, task state, and observation tracking.
- [ash_attachments.go](ash_attachments.go): `--attach` CLI flag parsing, attachment loading/MIME-sniffing/size limits, and writing model-returned attachments to disk.
- [tools.go](tools.go): tool definitions and local tool execution shim.
- [support.go](support.go): logging, history lifecycle, file-system helpers, and debug output.
- [install.go](install.go): shell wrapper installation and workspace initialization.
- [ai_autoconfig.go](ai_autoconfig.go): cloud provider/local server auto-detection and model-listing prompts used by `ash install`.
- [provider.go](provider.go): provider adapter registry (`ollama`, `openai`, `google`, `anthropic`, `cohere`, `bedrock`) and the adapter interface tiers: `providerAdapter` (base), `byteProviderAdapter` (raw HTTP, used only by `ollamaAdapter`), `sdkProviderAdapter` (official SDK-based `Send`, used by every other adapter), `streamingProviderAdapter` (adds `SendStream`, currently implemented only by [openai.go](openai.go)).
- [openai.go](openai.go), [google.go](google.go), [anthropic.go](anthropic.go), [cohere.go](cohere.go), [bedrock.go](bedrock.go): per-provider adapters built on each vendor's official Go SDK, all sharing the `ai_transport.go` HTTP client so retries/broker-fallback/metrics stay consistent across providers.
- [broker.go](broker.go): client-side broker connection logic used by the main `ash` binary; the broker server itself lives in the separate [cmd/ash-broker](cmd/ash-broker) binary (see [internal/brokerproto](internal/brokerproto) for the shared wire types) so the broker has no dependency on any AI provider SDK.
- [snooze.go](snooze.go): persistent expiry state for pausing automatic shell routing.

## Operational notes

- History is stored under the user workspace directory and is retained with a bounded cleanup policy.
- Shell integrations check `~/.ash/.ash_snooze_until` before automatic wrapper and command-not-found routing. Direct `ash` invocations bypass this check.
- Debug logging can be enabled with ASH_VERBOSE and optionally written to a rotating log file.
- Tool execution is restricted by allowlist and path containment rules. Ash resolves the allowlist once per request, uses it for enforcement and tool schemas, and renders eligible managed scripts through the internal `$TOOLS_DIR_LIST` prompt substitution.
- `ash install` auto-configures `AI_ENDPOINT`/`AI_MODEL`/`AI_AUTH_TOKEN`: it checks for an already-set cloud provider API key (12 providers, see [ai_autoconfig.go](ai_autoconfig.go)), then a running local inference server, then falls back to an interactive numbered menu; AWS Bedrock is intentionally excluded (see [issue #4](https://github.com/Jonconradt/ash/issues/4)).
