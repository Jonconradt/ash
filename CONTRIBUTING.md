# Contributing

## Before submitting

Run:

```bash
make lint test
```

Run install once to configure repository hooks:

```bash
make install
```

The install target sets Git `core.hooksPath` to `githooks`, and the pre-commit hook runs `scripts/eid-injector` on staged `.go` files before each commit.

For a full release-style verification pass, run:

```bash
make verify
```

## Testing expectations

- Add regression tests for new runtime behavior.
- Prefer table-driven tests for config and parsing logic.
- Keep tests deterministic and avoid depending on external services.

## Release expectations

- Keep the working tree clean before publishing.
- Ensure release artifacts are built and validated locally before tagging.
