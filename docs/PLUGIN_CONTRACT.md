# Native executable plugin contract

Ash v1 supports native plugins through a language-independent executable
interface. It does not provide a public Go plugin SDK; packages under Go's
`internal/` tree are implementation details and cannot be imported by external
modules as a supported API.

## Bundled platform support

macOS builds bundle `what_time_is_it` (Go) and `calculator` (TypeScript).
The top-level `plugins-build` target excludes plugins with a `Cargo.toml` when
the target OS is macOS, including native macOS builds. Linux and FreeBSD
releases also include `flip_a_coin` (Rust). This packaging policy does not
prevent users from installing their own compatible Rust executables.
Plugin source tests still cover Rust on developer machines.

## Discovery

A plugin is an executable file installed in the user plugin directory
(`$XDG_DATA_HOME/ash/plugins`, default `~/.local/share/ash/plugins`) or a
platform package plugin directory. It must be allowlisted and not denied by
Ash policy. Ash invokes tools with direct argv arguments, without a shell.

Plugins expose these informational flags:

- `--version` prints `<name> <semver>` on stdout and exits successfully.
- `--help` prints usage and exits successfully.
- `--ai-docs` prints one JSON object on stdout and exits successfully.

The `--ai-docs` object has this v1 envelope:

| Field | Type | Meaning |
| --- | --- | --- |
| `schema_version` | integer | Discovery document version; currently `1`. |
| `name` | string | Stable executable/plugin name. |
| `version` | string | Plugin release version. |
| `input_schema` | JSON Schema object | Describes accepted CLI options and their value types. |
| `output_schema` | JSON Schema object | Describes the plugin's structured JSON output. |
| `Capabilities` | array of strings | Human-readable summary of supported tasks. |
| `Arguments` | object | Human-readable descriptions of flags and options. |
| `Return format` | object | Human-readable output field descriptions. |
| `Usage guidance for the AI` | string | Optional guidance for choosing and using the plugin. |

Both schema fields use JSON Schema Draft 2020-12 and describe JSON objects.
`input_schema` models command-line options as object properties named by their
flags (for example, `--format`). `output_schema` describes the JSON response.
Bundled plugins use a top-level `status` value of `success` or `error` and
include `error_message` for structured failures when applicable. Additional
fields and constraints are plugin-specific and must be described in the
schemas.

The discovery document is metadata, not an execution sandbox or permission
grant. Policy still controls which executables Ash may run. Plugins should
validate arguments, bound output, use network timeouts, report actionable
errors, and avoid placing credentials in output or logs.

The discovery envelope is versioned independently from the Ash release. A
future incompatible change must use a new `schema_version`; consumers must not
assume that a newer version is compatible with version 1.
