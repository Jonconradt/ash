# calculator (TypeScript Plugin)

`calculator` is a reference TypeScript plugin for Ash. It evaluates complex mathematical expressions safely without using `eval()`.

## Plugin Requirements Implementation

1. **AI Documentation (`--ai-docs`)**:
   - Returns a structured JSON schema detailing capabilities (arithmetic, trigonometric, logarithmic functions), supported arguments (`--expr`, `--format`), and return schemas.
2. **Standard CLI Flags**:
   - `--version` / `-v`: Displays plugin name and version (`calculator 1.0.0`).
   - `--help` / `-h`: Displays usage examples and options.
3. **Structured Logging (`slog` compatibility)**:
   - Reads `ASH_LOG_FILE`, `ASH_LOG_FORMAT`, and `ASH_VERBOSE`.
   - Writes structured JSON / text log entries matching Ash's logging conventions with Event Identifiers (`EID`).
4. **Clean Execution**:
   - Built with TypeScript and compiled to a Node.js executable script with `#!/usr/bin/env node` shebang.
   - Evaluates mathematical expressions using a recursive descent parser.

## Building and Testing

```bash
make build   # Installs dependencies, compiles TypeScript to dist/index.js and copies to bin/plugins/
make test    # Runs validation tests against the compiled script
make lint    # Runs tsc --noEmit typechecker
make clean   # Cleans dist artifacts and binary
```
