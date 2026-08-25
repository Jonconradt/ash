#!/usr/bin/env bash
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

GOLANGCI_LINT_VERSION="${GOLANGCI_LINT_VERSION:-v2.13.1}"
GOSEC_VERSION="${GOSEC_VERSION:-v2.28.0}"
GOVULNCHECK_VERSION="${GOVULNCHECK_VERSION:-v1.1.4}"
STATICCHECK_VERSION="${STATICCHECK_VERSION:-latest}"
YAMLFMT_VERSION="${YAMLFMT_VERSION:-latest}"
RUFF_VERSION="${RUFF_VERSION:-0.12.10}"
MARKDOWNLINT_CLI2_VERSION="${MARKDOWNLINT_CLI2_VERSION:-0.23.2}"

install_macos_packages() {
	command -v brew >/dev/null 2>&1 || fail "Homebrew is required on macOS; install it from https://brew.sh and re-run"

	local pkg
	for pkg in go node python3 git uv; do
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
		sudo apt-get install -y "$pkg"
	fi
}

install_linux_packages() {
	command -v apt-get >/dev/null 2>&1 || fail "only Debian/Ubuntu (apt-get) based Linux is supported by this script"

	log "apt: updating package index"
	sudo apt-get update

	local pkg
	for pkg in golang-go nodejs npm python3 python3-pip git build-essential ruby ruby-dev rpm; do
		apt_install "$pkg"
	done

	if command -v gem >/dev/null 2>&1 && ! gem list -i fpm >/dev/null 2>&1; then
		log "gem: installing fpm (required for .deb/.rpm packaging in 'make release')"
		sudo gem install --no-document fpm
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
	go install "github.com/google/yamlfmt/cmd/yamlfmt@${YAMLFMT_VERSION}"
	go install "honnef.co/go/tools/cmd/staticcheck@${STATICCHECK_VERSION}"

	local gobin
	gobin="$(go env GOPATH)/bin"
	case ":${PATH}:" in
	*":${gobin}:"*) ;;
	*) log "note: add ${gobin} to PATH so 'staticcheck' is directly runnable (golangci-lint/gosec/govulncheck/yamlfmt run via 'go run' and don't need this)" ;;
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
	fi
}

main() {
	case "$(uname -s)" in
	Darwin)
		install_macos_packages
		;;
	Linux)
		install_linux_packages
		;;
	*)
		fail "unsupported operating system: $(uname -s)"
		;;
	esac

	install_go_tools
	warm_python_and_node_tools

	log "done: go, node/npm, python3, git, uv, golangci-lint, gosec, govulncheck, yamlfmt, staticcheck are ready"
}

main "$@"
