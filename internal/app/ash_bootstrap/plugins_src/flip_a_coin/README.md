# flip_a_coin (Rust Plugin)

`flip_a_coin` is a reference Rust native plugin for Ash. It simulates a fair coin toss and returns either `HEADS` or `TAILS` (or structured JSON).

## Plugin Requirements Implementation

1. **AI Documentation (`--ai-docs`)**:
   - Returns a structured JSON document describing capabilities, arguments (`--count`, `--format`), and return formats.
2. **Standard CLI Flags**:
   - `--version` / `-v`: Displays plugin name and version (`flip_a_coin 1.0.0`).
   - `--help` / `-h`: Displays usage and options.
3. **Structured Logging (`slog` compatibility)**:
   - Reads `ASH_LOG_FILE`, `ASH_LOG_FORMAT`, and `ASH_VERBOSE` from the environment.
   - Appends structured JSON or text log lines matching Ash's logging conventions with an Event Identifier (`EID`).
4. **Clean Execution and Exit**:
   - Compiles to a self-contained native binary with zero external runtime dependencies.
   - Returns exit code 0 on success.

## Building and Testing

```bash
make build   # Compiles release binary and places it into bin/plugins/
make test    # Runs cargo test
make lint    # Runs cargo check
make clean   # Cleans cargo artifacts and binary
```
