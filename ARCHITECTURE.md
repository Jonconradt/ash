# Architecture

## Runtime overview

The CLI entry point in [ash.go](ash.go) initializes configuration, loads the system prompt, resolves history, and runs the tool loop. The main control flow is:

1. parse environment-based configuration
2. load prompt/history/allowlist state
3. run the chat/tool loop
4. render the final assistant reply

## Key subsystems

- [chat.go](chat.go): request/response handling, transient retry logic, and transport-level error translation.
- [runner.go](runner.go): tool-loop orchestration, task state, and observation tracking.
- [tools.go](tools.go): tool definitions and local tool execution shim.
- [support.go](support.go): logging, history lifecycle, file-system helpers, and debug output.
- [install.go](install.go): shell wrapper installation and workspace initialization.
- [ai_autoconfig.go](ai_autoconfig.go): cloud provider/local server auto-detection and model-listing prompts used by `ash install`.
- [provider.go](provider.go): provider adapter registry (`ollama`, `openai`, `google`, `anthropic`) and per-provider request/response translation.
- [snooze.go](snooze.go): persistent expiry state for pausing automatic shell routing.

## Operational notes

- History is stored under the user workspace directory and is retained with a bounded cleanup policy.
- Shell integrations check `~/.ash/.ash_snooze_until` before automatic wrapper and command-not-found routing. Direct `ash` invocations bypass this check.
- Debug logging can be enabled with ASH_VERBOSE and optionally written to a rotating log file.
- Tool execution is restricted by allowlist and path containment rules. Ash resolves the allowlist once per request, uses it for enforcement and tool schemas, and renders eligible managed scripts through the internal `$TOOLS_DIR_LIST` prompt substitution.
- `ash install` auto-configures `AI_ENDPOINT`/`AI_MODEL`/`AI_AUTH_TOKEN`: it checks for an already-set cloud provider API key (12 providers, see [ai_autoconfig.go](ai_autoconfig.go)), then a running local inference server, then falls back to an interactive numbered menu; AWS Bedrock is intentionally excluded (see [issue #4](https://github.com/Jonconradt/ash/issues/4)).
