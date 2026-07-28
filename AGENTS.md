# AGENTS

## Developer Workflow Requirements

Before considering any code change complete, always run:

```bash
make lint test
```

Resolve all reported issues before finalizing work.

## Notes

- Do not skip lint or tests.
- Keep changes compatible with the existing Makefile quality gates.

## Debugging log messages

The EID is a unique ID for every logging call, use it to find the source of the log message
