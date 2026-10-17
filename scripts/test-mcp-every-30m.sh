#!/usr/bin/env bash

set -u

prompt='Tell me about snippets mentioning pizza'
interval_seconds=1800

if ! command -v ash >/dev/null 2>&1; then
  printf 'ash is not on PATH\n' >&2
  exit 127
fi

if [[ -z "${ASH_BROKER_SOCKET:-}" || -z "${ASH_BROKER_TOKEN:-}" ]]; then
  printf 'Run this script from an Ash-enabled interactive shell so it can use remote MCP servers.\n' >&2
  exit 1
fi

while true; do
  printf '\n[%s] Asking ash: %s\n' "$(date '+%Y-%m-%d %H:%M:%S %z')" "$prompt"
  command ash -- "$prompt"
  status=$?
  if (( status != 0 )); then
    printf '[%s] ash exited with status %d\n' "$(date '+%Y-%m-%d %H:%M:%S %z')" "$status" >&2
  fi
  sleep "$interval_seconds"
done
