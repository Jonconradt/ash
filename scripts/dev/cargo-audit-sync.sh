#!/usr/bin/env bash
# Copyright (c) 2026 Jonathan L. Conradt
# SPDX-License-Identifier: MIT

set -euo pipefail

sync_interval="${CARGO_AUDIT_SYNC_INTERVAL:-86400}"
cache_dir="${XDG_CACHE_HOME:-$HOME/.cache}/ash"
marker="$cache_dir/cargo-advisory-db.sync"
db="${CARGO_ADVISORY_DB:-$HOME/.cargo/advisory-db}"

mkdir -p "$cache_dir"

now="$(date +%s)"
if [[ "${CARGO_AUDIT_FORCE_SYNC:-0}" != "1" && -f "$marker" ]]; then
	IFS=' ' read -r marker_time _ <"$marker"
	if [[ "$marker_time" =~ ^[0-9]+$ ]] && (( now - marker_time < sync_interval )); then
		exit 0
	fi
fi

cargo audit update-db
printf '%s %s\n' "$now" "$(git -C "$db" rev-parse HEAD 2>/dev/null || true)" >"$marker"
