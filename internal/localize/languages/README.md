# Language pack guide

`en_US.json` is the source catalog and the base language for every translation.
Add or update its message keys first, then translate those messages in the
language catalogs. Keep the JSON files UTF-8 encoded.

## Catalog structure

Each file is named for its locale, such as `es_ES.json`, and has this shape:

```json
{
  "locale": "es_ES",
  "messages": {
    "install.already_present": "La instalación de ash ya existe en %s"
  }
}
```

The `locale` value must match the filename. `messages` maps stable, descriptive
keys to user-facing text. When a catalog is a dialect or regional variation of
another catalog, add a `parent` field and include only the messages that differ:

```json
{
  "locale": "es_MX",
  "parent": "es_ES",
  "messages": {
    "some.regional.message": "Regional wording"
  }
}
```

Parent messages are loaded first and child messages override them. A parent
chain must be acyclic and ultimately resolve to a language catalog based on
the `en_US` source. A language's base translation (for example, `es_ES`) should
translate every key from `en_US.json`; use sparse catalogs for regional
overrides (for example, `es_MX` inheriting from `es_ES`).

## Maintenance rules

- Add every new user-visible `ash` or `ash-broker` string to `en_US.json`
  before using its key in Go code, then translate it in the relevant language
  catalogs.
- Keep message keys stable and use the same key for the same meaning in every
  locale. Do not put language names, EIDs, or other identifiers in place of
  message keys.
- Preserve all formatting placeholders and their argument order, such as `%s`,
  `%v`, and `%q`. Do not translate placeholder syntax or change the arguments
  passed by the Go call site.
- Keep regional catalogs sparse: declare the intended parent and include only
  actual regional wording changes. Do not copy the entire parent catalog into
  a dialect pack.
- EIDs are universal and managed only by the eid-injector. Never translate,
  modify, or add EIDs to language catalogs.
- Run `make language-lint` after catalog edits. It checks JSON, required keys,
  parent relationships, formatting placeholders, and localized man pages;
  its errors identify the locale and missing or mismatched keys.
- Update the corresponding translated man page under `docs/man/<locale>/`
  when changing the user-facing command documentation.
