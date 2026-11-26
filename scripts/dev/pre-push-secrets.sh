#!/usr/bin/env bash
# Copyright (c) 2026 Jonathan L. Conradt
# SPDX-License-Identifier: MIT

set -euo pipefail

push_base="${1:-}"
gitleaks_version="${2:?gitleaks version required}"

if [[ -n "$push_base" ]]; then
	log_opts="$push_base..HEAD"
else
	log_opts="HEAD"
fi

./scripts/dev/run-quiet.sh "secrets-range" \
	go run "github.com/zricethezav/gitleaks/v8@$gitleaks_version" \
	git --platform github --redact --verbose --log-opts="$log_opts" .
