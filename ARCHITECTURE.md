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

## Operational notes

- History is stored under the user workspace directory and is retained with a bounded cleanup policy.
- Debug logging can be enabled with ASH_VERBOSE and optionally written to a rotating log file.
- Tool execution is restricted by allowlist and path containment rules.
