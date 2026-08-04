#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  push_with_retry.sh <description> <git push ...>
EOF
}

if [[ $# -lt 2 ]]; then
  usage >&2
  exit 1
fi

description="$1"
shift

max_attempts=4
delay=2
attempt=1

while true; do
  if GIT_TERMINAL_PROMPT=0 GIT_SSH_COMMAND="ssh -o BatchMode=yes -o ConnectTimeout=15 -o ServerAliveInterval=15 -o ServerAliveCountMax=2" "$@"; then
    exit 0
  fi

  if [[ "$attempt" -ge "$max_attempts" ]]; then
    echo "$description failed after $attempt attempts" >&2
    exit 1
  fi

  echo "$description failed on attempt $attempt; retrying in $delay s" >&2
  sleep "$delay"
  attempt=$((attempt + 1))
  delay=$((delay * 2))
done