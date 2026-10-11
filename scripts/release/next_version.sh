#!/usr/bin/env bash
# Copyright (c) 2026 Jonathan L. Conradt
# SPDX-License-Identifier: MIT

set -euo pipefail

channel="${1:-beta}"
latest_tag="$(git tag --list 'v[0-9]*.[0-9]*.[0-9]*' --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -n1 || true)"

if [[ -z "$latest_tag" ]]; then
  major=0
  minor=0
  patch=0
else
  if [[ ! "$latest_tag" =~ ^v([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
    echo "failed to parse latest stable tag: $latest_tag" >&2
    exit 1
  fi

  major="${BASH_REMATCH[1]}"
  minor="${BASH_REMATCH[2]}"
  patch="${BASH_REMATCH[3]}"
fi

next_minor=$((minor + 1))

case "$channel" in
  beta)
    base_version="v${major}.${next_minor}.0"
    latest_beta="$(git tag --list "${base_version}-beta.*" | grep -E "^${base_version}-beta\\.[0-9]+$" | sed -E 's/.*-beta\.//' | sort -n | tail -n1 || true)"
    beta_number=1
    if [[ -n "$latest_beta" ]]; then
      beta_number=$((latest_beta + 1))
    fi
    echo "${base_version}-beta.${beta_number}"
    ;;
  stable)
    echo "v${major}.${next_minor}.0"
    ;;
  nightly)
    base_version="v${major}.${next_minor}.0-nightly.$(date -u +%Y%m%d)"
    latest_retry="$(git tag --list "${base_version}.*" | grep -E "^${base_version//./\\.}\\.[1-9][0-9]*$" | sed -E 's/.*\.//' | sort -n | tail -n1 || true)"
    if [[ -n "$latest_retry" ]]; then
      echo "${base_version}.$((latest_retry + 1))"
    elif git show-ref --verify --quiet "refs/tags/$base_version"; then
      echo "${base_version}.1"
    else
      echo "$base_version"
    fi
    ;;
  *)
    echo "unsupported release channel: $channel (expected beta, stable, or nightly)" >&2
    exit 2
    ;;
esac
