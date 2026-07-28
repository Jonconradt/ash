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

The tool execution subsystem is intentionally constrained to a local allowlist of executables. The model can only invoke commands that are explicitly allowlisted and can only pass direct argv values that do not contain blocked shell-control patterns.

## Prompt injection and tool safety

The project is designed for local, user-driven shell assistance. Treat all model-generated command requests as untrusted input. Users should:
- keep the tool allowlist as narrow as practical
- avoid granting access to sensitive commands unless absolutely necessary
- review tool output before acting on it

## Release hygiene

Releases should be built from a clean working tree and verified with the project quality gates before publication.
