#!/usr/bin/env bash
# Copyright (c) 2026 Jonathan L. Conradt
# SPDX-License-Identifier: MIT

# Installs the developer toolchain required by `make lint test security release`.
# Supports macOS (Homebrew) and Debian/Ubuntu-based Linux (apt).

set -Eeuo pipefail

log() {
	printf '[config] %s\n' "$*"
}

fail() {
	printf '[config] error: %s\n' "$*" >&2
	exit 1
}

GOLANGCI_LINT_VERSION="${GOLANGCI_LINT_VERSION:-v2.14.0}"
GOSEC_VERSION="${GOSEC_VERSION:-v2.29.1-0.20261009120814-7b1b5cebe007}"
GOVULNCHECK_VERSION="${GOVULNCHECK_VERSION:-v1.8.0}"
STATICCHECK_VERSION="${STATICCHECK_VERSION:-2026.2.1}"
GITLEAKS_VERSION="${GITLEAKS_VERSION:-v8.30.1}"
YAMLFMT_VERSION="${YAMLFMT_VERSION:-v0.21.0}"
FPM_VERSION="${FPM_VERSION:-1.18.0}"
RUFF_VERSION="${RUFF_VERSION:-0.12.10}"
MARKDOWNLINT_CLI2_VERSION="${MARKDOWNLINT_CLI2_VERSION:-0.23.2}"
HTML_VALIDATE_VERSION="${HTML_VALIDATE_VERSION:-11.17.0}"
ACTIONLINT_VERSION="${ACTIONLINT_VERSION:-v1.7.12}"
PIP_AUDIT_VERSION="${PIP_AUDIT_VERSION:-2.10.1}"
CARGO_AUDIT_VERSION="${CARGO_AUDIT_VERSION:-0.22.2}"
NVM_VERSION="${NVM_VERSION:-v0.40.3}"
NODE_VERSION="${NODE_VERSION:-lts/*}"

node_supported() {
	local version major minor
	command -v node >/dev/null 2>&1 || return 1
	version="$(node --version)" || return 1
	if [[ ! "$version" =~ ^v([0-9]+)\.([0-9]+)\.[0-9]+$ ]]; then
		return 1
	fi
	major=$((10#${BASH_REMATCH[1]}))
	minor=$((10#${BASH_REMATCH[2]}))
	if (( (major == 22 && minor >= 22) || (major == 24 && minor >= 8) || major > 24 )); then
		return 0
	fi
	return 1
}

check_node_version() {
	local version
	if node_supported; then
		if node_is_eol; then
			version="$(node --version)"
			fail "Node.js $version is end-of-life; run 'nvm install lts/* && nvm use lts/* && nvm alias default lts/* && make config'"
		fi
		return
	fi
	if command -v node >/dev/null 2>&1; then
		version="$(node --version)"
		fail "Node.js $version is unsupported; run 'nvm install lts/* && nvm use lts/* && nvm alias default lts/* && make config'"
	fi
	fail "Node.js is not on PATH; run 'nvm install lts/* && nvm use lts/* && nvm alias default lts/* && make config'"
}

node_is_eol() {
	local version cycle eol_status
	command -v curl >/dev/null 2>&1 || return 1
	command -v python3 >/dev/null 2>&1 || return 1
	command -v node >/dev/null 2>&1 || return 1
	version="$(node --version)"
	cycle="${version#v}"
	cycle="${cycle%%.*}"
	eol_status="$(
		curl -fsSL --max-time 5 https://endoflife.date/api/nodejs.json 2>/dev/null |
			python3 -c '
import json, sys
from datetime import date
major = sys.argv[1]
try:
    rows = json.load(sys.stdin)
except Exception:
    raise SystemExit(2)
for row in rows:
    if str(row.get("cycle", "")).split(".")[0] == major:
        value = row.get("eol")
        if value is True:
            print("eol")
        elif isinstance(value, str):
            try:
                print("eol" if date.fromisoformat(value) <= date.today() else "supported")
            except ValueError:
                print("unknown")
        else:
            print("unknown")
        break
else:
    print("unknown")
' "$cycle"
	)" || return 1
	[[ "$eol_status" == "eol" ]]
}

install_linux_node() {
	node_supported && ! node_is_eol && return

	command -v curl >/dev/null 2>&1 || fail "curl is required to install Node.js ${NODE_VERSION}"
	export NVM_DIR="${NVM_DIR:-$HOME/.nvm}"
	if [[ ! -s "$NVM_DIR/nvm.sh" ]]; then
		log "node: installing NVM ${NVM_VERSION} for this user"
		curl -fsSL "https://raw.githubusercontent.com/nvm-sh/nvm/${NVM_VERSION}/install.sh" | bash
	fi
	[[ -s "$NVM_DIR/nvm.sh" ]] || fail "NVM installation failed; install Node.js ${NODE_VERSION} manually"

	# shellcheck disable=SC1091
	source "$NVM_DIR/nvm.sh"
	log "node: installing Node.js ${NODE_VERSION} via NVM"
	nvm install "$NODE_VERSION"
	nvm use "$NODE_VERSION" >/dev/null
	nvm alias default "$NODE_VERSION" >/dev/null
	node_supported || fail "Node.js ${NODE_VERSION} installation failed"
	node_is_eol && fail "Node.js $(node --version) is still end-of-life; run 'nvm install lts/* && nvm use lts/* && nvm alias default lts/* && make config'"
	log "node: active Node.js $(node --version)"
}

ensure_supported_node() {
	if node_supported; then
		if ! node_is_eol; then
			return
		fi
		log "node: active Node.js $(node --version) is end-of-life"
	fi
	if [[ "$(uname -s)" == "Linux" ]]; then
		install_linux_node
	fi
	check_node_version
}

install_macos_packages() {
	command -v brew >/dev/null 2>&1 || fail "Homebrew is required on macOS; install it from https://brew.sh and re-run"

	local pkg
	for pkg in go node python3 git uv shellcheck; do
		if brew list --formula "$pkg" >/dev/null 2>&1 || brew list --cask "$pkg" >/dev/null 2>&1; then
			log "brew: $pkg already installed"
		else
			log "brew: installing $pkg"
			brew install "$pkg"
		fi
	done

	if ! xcode-select -p >/dev/null 2>&1; then
		log "note: Xcode Command Line Tools not detected; run 'xcode-select --install' for pkgbuild/pkgutil/shasum used by 'make release'"
	fi
}

apt_install() {
	local pkg="$1"
	if dpkg -s "$pkg" >/dev/null 2>&1; then
		log "apt: $pkg already installed"
	else
		log "apt: installing $pkg"
		sudo -A apt-get install -y "$pkg"
	fi
}

install_linux_packages() {
	command -v apt-get >/dev/null 2>&1 || fail "only Debian/Ubuntu (apt-get) based Linux is supported by this script"

	local pkg
	local missing=0
	for pkg in golang-go nodejs npm python3 python3-pip git build-essential ruby ruby-dev rpm shellcheck; do
		if ! dpkg -s "$pkg" >/dev/null 2>&1; then
			missing=1
		fi
	done
	if (( missing )); then
		log "apt: updating package index"
		sudo -A apt-get update
	fi

	for pkg in golang-go nodejs npm python3 python3-pip git build-essential ruby ruby-dev rpm shellcheck; do
		apt_install "$pkg"
	done

	if command -v gem >/dev/null 2>&1 && ! gem list -i -v "$FPM_VERSION" fpm >/dev/null 2>&1; then
		log "gem: installing fpm $FPM_VERSION (required for .deb/.rpm packaging in 'make release')"
		sudo -A gem install --no-document --version "$FPM_VERSION" fpm
	fi

	if command -v uv >/dev/null 2>&1; then
		log "uv: already installed"
	else
		log "uv: installing via astral.sh installer"
		curl -LsSf https://astral.sh/uv/install.sh | sh
	fi
}

install_go_tools() {
	command -v go >/dev/null 2>&1 || fail "go is not on PATH after installation; open a new shell and re-run"

	log "go: pre-fetching lint/security tool modules"
	go install "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_LINT_VERSION}"
	go install "github.com/securego/gosec/v2/cmd/gosec@${GOSEC_VERSION}"
	go install "golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}"
	go install "github.com/zricethezav/gitleaks/v8@${GITLEAKS_VERSION}"
	go install "github.com/google/yamlfmt/cmd/yamlfmt@${YAMLFMT_VERSION}"
	go install "honnef.co/go/tools/cmd/staticcheck@${STATICCHECK_VERSION}"
	go install "github.com/rhysd/actionlint/cmd/actionlint@${ACTIONLINT_VERSION}"

	local gobin
	gobin="$(go env GOPATH)/bin"
	case ":${PATH}:" in
	*":${gobin}:"*) ;;
	*) log "note: add ${gobin} to PATH so 'staticcheck' and 'gitleaks' are directly runnable (golangci-lint/gosec/govulncheck/yamlfmt run via 'go run' and don't need this)" ;;
	esac
}

install_rust_toolchain() {
	export PATH="$HOME/.cargo/bin:$PATH"

	if command -v cargo >/dev/null 2>&1; then
		log "rust: cargo already installed ($(cargo --version))"
	else
		log "rust: installing via rustup (required by the flip_a_coin plugin)"
		curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --no-modify-path --profile minimal
		command -v cargo >/dev/null 2>&1 || fail "cargo is not on PATH after rustup installation"
	fi

	if command -v rustup >/dev/null 2>&1 && ! command -v cargo-clippy >/dev/null 2>&1; then
		log "rust: adding clippy component"
		local active_toolchain
		active_toolchain="$(rustup show active-toolchain | awk '{print $1}')"
		rustup component add --toolchain "$active_toolchain" clippy
	fi
	if ! command -v cargo-audit >/dev/null 2>&1; then
		log "rust: installing cargo-audit ${CARGO_AUDIT_VERSION}"
		cargo install cargo-audit --version "$CARGO_AUDIT_VERSION" --locked
	fi

	case ":${PATH}:" in
	*":$HOME/.cargo/bin:"*) ;;
	*) log "note: add $HOME/.cargo/bin to PATH so 'cargo' is runnable in new shells" ;;
	esac
}

warm_python_and_node_tools() {
	if command -v uv >/dev/null 2>&1; then
		export PATH="$HOME/.local/bin:$PATH"
		log "uv: pre-fetching ruff ${RUFF_VERSION}"
		uvx "ruff@${RUFF_VERSION}" --version >/dev/null
	fi

	if command -v npx >/dev/null 2>&1; then
		log "npm: pre-fetching markdownlint-cli2 ${MARKDOWNLINT_CLI2_VERSION}"
		npx --yes "markdownlint-cli2@${MARKDOWNLINT_CLI2_VERSION}" --version >/dev/null
		log "npm: pre-fetching html-validate ${HTML_VALIDATE_VERSION}"
		npx --yes "html-validate@${HTML_VALIDATE_VERSION}" --version >/dev/null
	fi

	if command -v uvx >/dev/null 2>&1; then
		log "uv: pre-fetching pip-audit ${PIP_AUDIT_VERSION}"
		uvx --from "pip-audit==${PIP_AUDIT_VERSION}" pip-audit --version >/dev/null
	fi
}

main() {
	case "$(uname -s)" in
	Darwin)
		install_macos_packages
		check_node_version
		;;
	Linux)
		ensure_supported_node
		install_linux_packages
		;;
	*)
		fail "unsupported operating system: $(uname -s)"
		;;
	esac

	command -v node >/dev/null 2>&1 || fail "Node.js is not on PATH after installation"
	check_node_version
	install_go_tools
	install_rust_toolchain
	warm_python_and_node_tools

	log "done: Go, Node/npm, Python, git, uv, Rust/cargo, ShellCheck, Go static/security tools, actionlint, HTML/Markdown validators, and dependency auditors are ready"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
	main "$@"
fi
