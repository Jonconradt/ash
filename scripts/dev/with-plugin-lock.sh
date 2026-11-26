#!/usr/bin/env bash
# Copyright (c) 2026 Jonathan L. Conradt
# SPDX-License-Identifier: MIT

set -euo pipefail

if [[ $# -lt 2 ]]; then
	echo "usage: $0 <plugin-name> <command> [args...]" >&2
	exit 2
fi

plugin_name="$1"
shift

lock_dir="${TMPDIR:-/tmp}/ash-plugin-${plugin_name}.lock"
release_lock() {
	rm -f -- "${lock_dir}/pid"
	rmdir -- "${lock_dir}" 2>/dev/null || true
}

acquire_lock() {
	local deadline=$((SECONDS + 300))
	while ! mkdir -- "${lock_dir}" 2>/dev/null; do
		local owner_pid
		owner_pid="$(cat "${lock_dir}/pid" 2>/dev/null || true)"
		if [[ "$owner_pid" =~ ^[0-9]+$ ]] && kill -0 "$owner_pid" 2>/dev/null; then
			if (( SECONDS >= deadline )); then
				echo "timed out waiting for plugin lock ${lock_dir} (owner pid ${owner_pid})" >&2
				exit 1
			fi
			sleep 0.1
			continue
		fi
		rm -f -- "${lock_dir}/pid"
		rmdir -- "${lock_dir}" 2>/dev/null || true
	done
	printf '%s\n' "$$" >"${lock_dir}/pid"
	trap release_lock EXIT
}

acquire_lock
set +e
"$@"
status=$?
set -e
release_lock
trap - EXIT
exit "$status"
