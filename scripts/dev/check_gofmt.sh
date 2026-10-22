#!/usr/bin/env bash
# Copyright (c) 2026 Jonathan L. Conradt
# SPDX-License-Identifier: MIT

set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

unformatted="$(git ls-files -z -- '*.go' | xargs -0 gofmt -l)"
if [[ -n "$unformatted" ]]; then
	printf 'Go files need gofmt:\n%s\n' "$unformatted" >&2
	exit 1
fi
