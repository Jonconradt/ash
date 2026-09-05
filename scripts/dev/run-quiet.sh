#!/usr/bin/env bash
# run-quiet.sh — run a command, echoing its output only when it fails.
#
# Used by the Makefile "quiet" verify path so a green run prints one line per
# step plus a final success marker, while a failing run surfaces the full
# output of just the step that failed.
#
# Usage: run-quiet.sh <label> <command> [args...]
# Exit status is the command's exit status (non-zero on failure).

set -u

label="$1"
shift

if [[ "${V:-${VERBOSE:-0}}" == "1" ]]; then
	"$@"
	exit $?
fi

log="$(mktemp -t "ash-verify-${label//[^A-Za-z0-9_.-]/_}")"
trap 'rm -f "$log"' EXIT

printf '==> %s ... ' "$label"
if "$@" >"$log" 2>&1; then
	echo "ok"
	exit 0
fi

status=$?
echo "FAILED (exit $status)"
echo "----- $label output -----"
cat "$log"
echo "----- end $label -----"
exit "$status"
