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

## Localization maintenance

- All user-visible strings emitted by `ash` and `ash-broker` must use keys from
  `internal/localize/languages/`; update every effective locale when copy or
  formatting placeholders change.
- Catalogs are UTF-8 JSON. Locale-specific catalogs may declare a `parent`
  and contain only dialect overrides; keep the resolved parent-plus-override
  catalog complete.
- `make lint` checks catalog syntax, required keys, formatting placeholders,
  and localized man pages. Keep those checks passing when adding or changing
  user-facing text.
- EIDs are universal identifiers. Never translate, rename, or add them to
  language catalogs; only the eid-injector manages EIDs.
