#!/usr/bin/env python3
# Copyright (c) 2026 Jonathan L. Conradt
# SPDX-License-Identifier: MIT

from __future__ import annotations

import json
import subprocess
import sys
from pathlib import Path


def reject_duplicate_keys(pairs: list[tuple[str, object]]) -> dict[str, object]:
    result: dict[str, object] = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"duplicate object key: {key}")
        result[key] = value
    return result


def main() -> int:
    result = subprocess.run(
        ["git", "ls-files", "-z", "--", "*.json"],
        check=True,
        stdout=subprocess.PIPE,
    )
    paths = [Path(path.decode()) for path in result.stdout.split(b"\0") if path]
    errors = 0
    for path in paths:
        try:
            with path.open(encoding="utf-8") as source:
                json.load(source, object_pairs_hook=reject_duplicate_keys)
        except (OSError, UnicodeError, json.JSONDecodeError, ValueError) as error:
            print(f"{path}: {error}", file=sys.stderr)
            errors += 1
    if errors:
        return 1
    print(f"validated {len(paths)} tracked JSON files")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
