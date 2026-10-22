#!/usr/bin/env bash
# Copyright (c) 2026 Jonathan L. Conradt
# SPDX-License-Identifier: MIT

set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
configured="$(git -C "$repo_root" config --local --get core.hooksPath || true)"
case "$configured" in
	"")
		git -C "$repo_root" config --local core.hooksPath githooks
		;;
	githooks|"$repo_root/githooks")
		;;
	*)
		echo "core.hooksPath is already set to '$configured'; refusing to replace custom hooks" >&2
		echo "Install the repository hooks manually or chain them from your existing hooks path." >&2
		exit 1
		;;
esac

for hook in pre-commit pre-push; do
	if [[ ! -x "$repo_root/githooks/$hook" ]]; then
		echo "repository hook is missing or not executable: githooks/$hook" >&2
		exit 1
	fi
done

echo "Git hooks enabled: $repo_root/githooks"
