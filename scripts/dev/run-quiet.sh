#!/usr/bin/env bash
# Copyright (c) 2026 Jonathan L. Conradt
# SPDX-License-Identifier: MIT

# run-quiet.sh — run a command, echoing its output only when it fails.
#
# Used by the Makefile "quiet" verify path so a green run prints one line per
# step plus a final success marker, while a failing run surfaces the full
# output of just the step that failed.
# Set V=1, VERBOSE=1, or ASH_VERBOSE=1 to stream command output.
#
# Usage: run-quiet.sh <label> <command> [args...]
# Exit status is the command's exit status (non-zero on failure).

set -u

label="$1"
shift

if [[ "${V:-0}" == "1" || "${VERBOSE:-0}" == "1" || "${ASH_VERBOSE:-0}" == "1" ]]; then
	"$@"
	exit $?
fi

log="$(mktemp "${TMPDIR:-/tmp}/ash-verify-${label//[^A-Za-z0-9_.-]/_}.XXXXXX")"
trap 'rm -f "$log"' EXIT

printf '==> %s ... ' "$label"
"$@" >"$log" 2>&1
status=$?
if [[ "$status" -eq 0 ]]; then
	echo "ok"
	exit 0
fi

echo "FAILED (exit $status)"
echo "----- $label output -----"
cat "$log"
echo "----- end $label -----"
exit "$status"
