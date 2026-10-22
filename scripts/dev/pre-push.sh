#!/usr/bin/env bash
# Copyright (c) 2026 Jonathan L. Conradt
# SPDX-License-Identifier: MIT

set -Eeuo pipefail

repo_root="$(git rev-parse --show-toplevel)"
tmp_root="$(mktemp -d "${TMPDIR:-/tmp}/ash-pre-push.XXXXXX")"
worktree=""
cleanup() {
	if [[ -n "$worktree" && -d "$worktree" ]]; then
		git -C "$repo_root" worktree remove --force "$worktree"
	fi
	rmdir "$tmp_root"
}
trap cleanup EXIT

while read -r local_ref local_oid remote_ref remote_oid; do
	[[ -n "${local_ref:-}" ]] || continue
	if [[ "$local_oid" =~ ^0+$ ]]; then
		continue
	fi
	if ! [[ "$local_oid" =~ ^[[:xdigit:]]{40}([[:xdigit:]]{24})?$ ]]; then
		echo "invalid pushed object ID for $local_ref: $local_oid" >&2
		exit 1
	fi
	if ! git -C "$repo_root" cat-file -e "${local_oid}^{commit}" 2>/dev/null; then
		echo "pushed commit is not available locally: $local_oid" >&2
		exit 1
	fi

	push_base=""
	if ! [[ "$remote_oid" =~ ^0+$ ]]; then
		if ! push_base="$(git -C "$repo_root" merge-base "$remote_oid" "$local_oid")"; then
			echo "cannot find common ancestor for $remote_ref and $local_ref" >&2
			exit 1
		fi
	fi

	worktree="$tmp_root/revision"
	git -C "$repo_root" worktree add --quiet --detach "$worktree" "$local_oid"
	echo "Validating pushed commit $local_oid ($local_ref)"
	if ! (cd "$worktree" && make pre-push PUSH_BASE="$push_base"); then
		echo "pre-push validation failed for $local_ref ($local_oid); push aborted" >&2
		exit 1
	fi
	git -C "$repo_root" worktree remove --force "$worktree"
	worktree=""
done
