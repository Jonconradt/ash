#!/usr/bin/env bash
# Copyright (c) 2026 Jonathan L. Conradt
# SPDX-License-Identifier: MIT

set -euo pipefail

sync_interval="${CARGO_AUDIT_SYNC_INTERVAL:-86400}"
cache_dir="${XDG_CACHE_HOME:-$HOME/.cache}/ash"
marker="$cache_dir/cargo-advisory-db.sync"
db="${CARGO_ADVISORY_DB:-$HOME/.cargo/advisory-db}"
db_url="${CARGO_ADVISORY_DB_URL:-https://github.com/rustsec/advisory-db.git}"

mkdir -p "$cache_dir"

now="$(date +%s)"
if [[ "${CARGO_AUDIT_FORCE_SYNC:-0}" != "1" && -f "$marker" ]]; then
	IFS=' ' read -r marker_time _ <"$marker"
	if [[ "$marker_time" =~ ^[0-9]+$ ]] && (( now - marker_time < sync_interval )); then
		exit 0
	fi
fi

if cargo audit --help 2>/dev/null | grep -qE '(^|[[:space:]])update-db([[:space:],]|$)' ; then
	cargo audit update-db
elif [[ -d "$db/.git" ]]; then
	git -C "$db" fetch --quiet origin
elif [[ -e "$db" ]]; then
	echo "cargo advisory database path exists but is not a Git repository: $db" >&2
	exit 1
else
	mkdir -p "$(dirname "$db")"
	git clone --quiet "$db_url" "$db"
fi
printf '%s %s\n' "$now" "$(git -C "$db" rev-parse HEAD 2>/dev/null || true)" >"$marker"
