#!/usr/bin/env bash
set -euo pipefail

latest_tag="$(git tag --list 'v[0-9]*.[0-9]*.[0-9]*' --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -n1 || true)"

if [[ -z "$latest_tag" ]]; then
  echo "v0.1.0"
  exit 0
fi

if [[ ! "$latest_tag" =~ ^v([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
  echo "failed to parse latest stable tag: $latest_tag" >&2
  exit 1
fi

major="${BASH_REMATCH[1]}"
minor="${BASH_REMATCH[2]}"
next_minor=$((minor + 1))

echo "v${major}.${next_minor}.0"
