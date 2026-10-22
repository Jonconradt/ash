#!/usr/bin/env bash
# Copyright (c) 2026 Jonathan L. Conradt
# SPDX-License-Identifier: MIT

set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"
command -v shellcheck >/dev/null 2>&1 || {
	echo "shellcheck is required; run make config" >&2
	exit 1
}

while IFS= read -r -d '' path; do
	case "$path" in
	internal/app/ash_bootstrap/rc-source-bash.sh|internal/app/ash_bootstrap/rc-source-zsh.sh)
		continue
		;;
	esac
	shellcheck --severity=warning "$path"
done < <(git ls-files -z -- '*.sh' 'githooks/pre-commit' 'githooks/pre-push')
