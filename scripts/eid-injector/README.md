# eid-injector

Scans Go files and ensures `slog` calls include canonical `"EID", <code>` pairs.

## Usage

Update specific files:

```bash
go run ./cmd/eid-injector file1.go file2.go
```

Scan all `.go` files recursively:

```bash
go run ./cmd/eid-injector --all
```

## Command line switches

- `--all`: scan recursively from current directory and apply additional cleanup/dedup logic.

No environment variables are used.

## See also

- [README.md](../../README.md) for repository-wide setup and operations.
- [ARCHITECTURE.md](../../ARCHITECTURE.md) for runtime flow and component relationships.
- [ENVIRONMENT.md](../../ENVIRONMENT.md) for configuration keys.
- [MULTI_APP_README.md](../../MULTI_APP_README.md) for deployable binary overview.
