# Security Policy

## Supported versions

Security fixes are applied to the latest release line. Older release branches are not guaranteed to receive security updates unless explicitly stated.

## Reporting a vulnerability

Please report suspected vulnerabilities privately by emailing the maintainer or by opening a security advisory through GitHub.

Do not create public issues for security problems. Include:

- a concise description of the issue
- affected version or commit
- steps to reproduce
- any suggested mitigation

## Safety model

The tool execution subsystem is intentionally constrained to a local allowlist of executables. The model can only invoke commands that are explicitly allowlisted and can only pass direct argv values that do not contain blocked shell-control patterns. This is an execution boundary, not a sandbox: ash and any allowlisted tool run with the full OS permissions of the invoking user. Allowlisting a broad-capability executable (`python3`, `curl`, `osascript`, etc.) grants the model everything that executable can do as you.

`$TOOLS_DIR_LIST` and `$IF_PYTHON_AVAILABLE` in `.ash_system` are reserved internal Ash substitutions, never environment-derived variables. The former can list only allowed regular files under `~/.ash/tools/` with both read and execute permission bits; it is prompt guidance, not the enforcement boundary.

## Prompt injection and tool safety

The project is designed for local, user-driven shell assistance. Treat all model-generated command requests, and all tool/file/script/pipeline/child-agent output fed back to the model, as untrusted input. By default, ash mitigates this only by instructing the model (via system-prompt guidance) never to follow instructions found in that data — a soft control, since it depends on the model's own adherence and is not a guaranteed defense against a sufficiently crafted injection.

Set `ASH_STRICT=1` (also accepts `true`, `yes`, `y`, `on`, `strict`) to enable an additional, opt-in layer: recognized English-language prompt-injection phrases (for example "ignore previous instructions", "you are now", "jailbreak") are replaced with a blocked-content marker before untrusted tool output and file reads are shown to the model, and that content is explicitly labeled `UNTRUSTED_*_BEGIN/END`. This is pattern-based detection, not a comprehensive filter — it does not catch paraphrased, translated, encoded, or novel injection attempts, and can also produce false positives on legitimate text that happens to match. Treat `ASH_STRICT` as defense-in-depth on top of a narrow allowlist and careful review, not a substitute for either.

Users should:

- keep the tool allowlist as narrow as practical, and avoid allowlisting powerful general-purpose interpreters (`python3`, shells, etc.) unless you trust every script that can reach them
- set `ASH_STRICT=1` to disable ash's dedicated `run_python3` tool and ash-managed bundled `.py` tools, in addition to its untrusted-content hardening
- set `ASH_STRICT=1` in environments where untrusted data (web content, third-party files, unreviewed scripts) is likely to reach a tool call
- avoid granting access to sensitive or destructive commands unless absolutely necessary
- review tool output before acting on it, and treat any instruction-like text inside that output as an attack, not a legitimate request

See the [Security Model and Environment Variables](https://github.com/Jonconradt/ash/wiki/Security-Model-and-Environment-Variables) wiki page for the full threat model, the complete list of security-relevant environment variables, source-code references, and known weaknesses with suggested mitigations.

## Release hygiene

Releases should be built from a clean working tree and verified with the project quality gates before publication.
