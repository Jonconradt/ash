#!/usr/bin/env bash
# Copyright (c) 2026 Jonathan L. Conradt
# SPDX-License-Identifier: MIT

set -euo pipefail

node_version="${1:-lts/*}"
nvm_dir="${NVM_DIR:-$HOME/.nvm}"

if [[ ! -s "$nvm_dir/nvm.sh" ]]; then
	exit 0
fi

export NVM_DIR="$nvm_dir"
# shellcheck source=/dev/null
source "$nvm_dir/nvm.sh" >/dev/null 2>&1
nvm use "$node_version" >/dev/null 2>&1 || true

if command -v node >/dev/null 2>&1; then
	dirname "$(command -v node)"
fi
