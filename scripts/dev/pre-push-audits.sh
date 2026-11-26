#!/usr/bin/env bash
# Copyright (c) 2026 Jonathan L. Conradt
# SPDX-License-Identifier: MIT

set -euo pipefail

push_base="${1:-}"
manifest_regex='(^|/)(go\.mod|go\.sum|package\.json|package-lock\.json|Cargo\.toml|Cargo\.lock|requirements[^/]*\.txt)$'

if [[ -n "$push_base" ]]; then
	changed="$(git diff --name-only "$push_base"..HEAD)"
else
	changed="$(git ls-files)"
fi

if grep -Eq "$manifest_regex" <<<"$changed"; then
	make --no-print-directory dependency-audits
else
	echo "pre-push-audits: skipped (no dependency manifest changes)"
fi
