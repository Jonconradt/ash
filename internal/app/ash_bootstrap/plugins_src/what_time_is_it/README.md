# what_time_is_it (Go Plugin)

`what_time_is_it` is the reference Go native plugin for Ash. It provides the AI model with accurate date, time, timezone, and epoch timestamps without embedding static time information in the system prompt.

## Plugin Interface Implementation

This plugin satisfies the Go plugin contract by implementing the `plugin.Plugin` interface defined in `internal/plugin/plugin.go`:

1. **AI Documentation (`--ai-docs`)**:
   - Returns a structured JSON schema detailing capabilities, supported flags (`--format`, `--timezone`), and return value format.
2. **Standard CLI Flags**:
   - `--version` / `-v`: Displays plugin name and semantic version (`1.0.0`).
   - `--help` / `-h`: Displays usage instructions and available parameters.
3. **Structured Logging (`slog`)**:
   - Initializes a structured logger using Go's `log/slog` connected to `ASH_LOG_FILE`.
   - Supports `ASH_LOG_FORMAT` (`json` or `text`) and `ASH_VERBOSE` log level control.
   - Tags every log entry with a unique Error/Event Identifier (`EID`).
4. **Lifecycle & Clean Shutdown**:
   - Listens for `SIGINT` / `SIGTERM` signals via `signal.NotifyContext`.
   - Flushes output and exits cleanly on process termination or context deadline cancellation.

## Building and Testing

```bash
make build   # Builds the binary into the plugins output directory
make test    # Runs unit and integration tests
make lint    # Runs golangci-lint
make clean   # Cleans built artifacts
```
