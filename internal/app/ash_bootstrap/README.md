# ash_bootstrap

Assets embedded into the `ash` binary via `//go:embed` in
[bootstrap_assets.go](../bootstrap_assets.go) and installed into `~/.ash/` (and
the user's shell rc file) by `ash install`. Edit files here directly — do not
edit the copies under `~/.ash/`, which are overwritten on install/update.

## Shell wrapper templates

- `.ash_bashrc`, `.ash_zshrc`, `.ash_fish.fish` — per-shell wrapper functions (command-not-found routing, natural-language command collisions like `what`/`which`/`time`).
- `rc-source-bash.sh`, `rc-source-zsh.sh`, `rc-source-fish.fish` — the managed snippet appended to `~/.bashrc`/`~/.zshrc`/`~/.config/fish/config.fish` that sources the wrapper above.
- `route_words.txt` — canonical list of ambiguous English words used by the shell routing heuristic; regenerate the wrapper's embedded copy with `make sync-route-words` after editing this file.

## Runtime config templates

- `.ash_env` — managed `AI_ENDPOINT`/`AI_MODEL`/`AI_AUTH_TOKEN`/PATH exports, written by `ash install`'s interactive/auto-detect flow.
- `.ash_system` — default system prompt, supports `$IF_PYTHON_AVAILABLE` and `$VARIABLE` expansion.
- `.ash_tools` — default tool allowlist.

## Prompt fragments

- `prompt-instructions/python-available.txt`, `prompt-instructions/python-unavailable.txt` — conditional guidance spliced into the system prompt depending on whether a Python interpreter is available.

## Bundled tools

- `tools/` — Python scripts installed into `~/.ash/tools/` and made available to the model as callable tools (`headlines.py`, `wikipedia.py`, `yfinance.py`), plus `requirements.txt` and `test_tool_docs.py` (verifies each tool documents its own usage).
