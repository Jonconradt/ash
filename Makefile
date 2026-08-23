.PHONY: all verify build lint yaml-lint python-lint markdown-lint test test-race test-cover test-fuzz vet staticcheck gosec govulncheck security install setup-hooks version release release-check release-build release-pkg release-validate release-notes release-publish release-watch release-dashboard release-artifacts release-build-one release-pkg-one release-validate-one release-checksums

SHELL := /bin/bash

COVERAGE_MIN ?= 60
FUZZ_TIME ?= 10s
GOLANGCI_LINT_VERSION ?= v2.13.1
RUFF_VERSION ?= 0.12.10
MARKDOWNLINT_CLI2_VERSION ?= 0.23.2
GOSEC_VERSION ?= v2.28.0
GOVULNCHECK_VERSION ?= v1.1.4
APP_NAME ?= ash
RELEASE_ARCH ?= arm64
RELEASE_OUTPUT_DIR ?= dist/release
RELEASE_PACKAGE_DIR ?= $(RELEASE_OUTPUT_DIR)
RELEASE_TARGET_ARCHES ?= amd64 arm64
RELEASE_TARGET_OSES ?= auto
RELEASE_GOOS ?= darwin
RELEASE_FORMAT ?= pkg
RELEASE_WATCH ?= 1
RELEASE_WATCH_STRICT ?= 1
BUILD_VERSION ?= dev
BUILD_COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null || printf 'unknown')
RELEASE_COMMIT ?= $(BUILD_COMMIT)
LATEST_RELEASE_TAG ?= $(shell git tag --list 'v[0-9]*.[0-9]*.[0-9]*' --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$$' | head -n1)
AUTO_RELEASE_VERSION ?= $(shell ./scripts/release/next_version.sh)
RELEASE_VERSION ?= $(AUTO_RELEASE_VERSION)
RELEASE_PKG_NAME ?= $(APP_NAME)-$(RELEASE_VERSION)-darwin-$(RELEASE_ARCH).pkg
RELEASE_PKG_PATH ?= $(RELEASE_PACKAGE_DIR)/$(RELEASE_PKG_NAME)
RELEASE_INSTALL_PATH ?= /usr/local/bin
MAN_PAGE_PATH ?= docs/ash.1
MAN_INSTALL_PATH_LINUX ?= /usr/share/man/man1
MAN_INSTALL_PATH_MACOS ?= /usr/local/share/man/man1
TARBALL_MAN_PATH ?= usr/share/man/man1
RELEASE_NOTES_PATH ?= $(RELEASE_OUTPUT_DIR)/release-notes.md
RELEASE_ARTIFACT_BASE ?= $(APP_NAME)-$(RELEASE_VERSION)-$(RELEASE_GOOS)-$(RELEASE_ARCH)
BINARY_EXT = $(if $(filter windows,$(RELEASE_GOOS)),.exe,)
RELEASE_BINARY_PATH ?= $(RELEASE_OUTPUT_DIR)/$(RELEASE_ARTIFACT_BASE)$(BINARY_EXT)

all: verify install

verify: test test-race test-cover vet staticcheck security test-fuzz benchmark

build: lint test
	@go install -ldflags "-X main.ashVersion=$(BUILD_VERSION) -X main.ashCommit=$(BUILD_COMMIT)" .

lint: yaml-lint python-lint markdown-lint
	@go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run ./...

yaml-lint:
	@echo "Validating GitHub Actions workflows..."
	@go run github.com/google/yamlfmt/cmd/yamlfmt@latest -lint .github/workflows/*.yml

python-lint:
	@uvx ruff@$(RUFF_VERSION) check ash_bootstrap/tools
	@uvx ruff@$(RUFF_VERSION) format --check ash_bootstrap/tools

markdown-lint:
	@npx --yes markdownlint-cli2@$(MARKDOWNLINT_CLI2_VERSION) README.md ARCHITECTURE.md CONTRIBUTING.md SECURITY.md AGENTS.md scripts/eid-injector/README.md

security: gosec govulncheck

gosec:
	@go run github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION) -quiet ./...

govulncheck:
	@go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

test:
	@bash -n sample_bashrc.txt
	@go test ./...

test-race:
	@go test -race ./...

test-cover:
	@go test -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out | awk '/^total:/ {gsub("%", "", $$3); if ($$3 + 0 < $(COVERAGE_MIN)) {printf("coverage %.1f%% is below %s%%\n", $$3, "$(COVERAGE_MIN)"); exit 1} else {printf("coverage %.1f%% meets %s%%\n", $$3, "$(COVERAGE_MIN)")}}'

test-fuzz:
	@go test -fuzz=Fuzz -fuzztime=$(FUZZ_TIME) .

benchmark:
	@go test -bench=. -benchmem ./...

vet:
	@go vet ./...

staticcheck:
	@if command -v staticcheck >/dev/null 2>&1; then \
		if ! staticcheck ./...; then \
			echo "staticcheck failed (toolchain mismatch or local setup issue); skipping"; \
		fi; \
	else \
		echo "staticcheck not installed; skipping"; \
	fi

install: test lint  gosec 
	@go install ./...
	@ash install --shell bash --overwrite

version: release-check release-artifacts

release: release-check release-build release-pkg release-validate release-notes release-publish release-watch

release-check: lint test gosec govulncheck
	@if [[ -n "$$(git status --porcelain)" ]]; then \
		echo "git working tree is dirty; commit or stash changes before release"; \
		git status --short; \
		exit 1; \
	fi
	@echo "Using RELEASE_VERSION=$(RELEASE_VERSION)"
	@if [[ -z "$(LATEST_RELEASE_TAG)" ]]; then \
		echo "No stable release tags found; defaulting from baseline v0.1.0"; \
	else \
		echo "Latest stable release tag: $(LATEST_RELEASE_TAG)"; \
	fi
	@if ! [[ "$(RELEASE_VERSION)" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.-]+)?$$ ]]; then \
		echo "RELEASE_VERSION must look like vX.Y.Z (optionally with suffix), got: $(RELEASE_VERSION)"; \
		exit 1; \
	fi

release-build:
	@mkdir -p "$(RELEASE_OUTPUT_DIR)"
	GOOS=darwin GOARCH=$(RELEASE_ARCH) CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.ashVersion=$(RELEASE_VERSION) -X main.ashCommit=$(RELEASE_COMMIT)" -o "$(RELEASE_OUTPUT_DIR)/$(APP_NAME)" .

release-build-one:
	@mkdir -p "$(RELEASE_OUTPUT_DIR)"
	GOOS=$(RELEASE_GOOS) GOARCH=$(RELEASE_ARCH) CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.ashVersion=$(RELEASE_VERSION) -X main.ashCommit=$(RELEASE_COMMIT)" -o "$(RELEASE_BINARY_PATH)" .

release-pkg:
	@mkdir -p "$(RELEASE_PACKAGE_DIR)"
	@./scripts/package/macos/build_pkg.sh \
		--app-name "$(APP_NAME)" \
		--version "$(RELEASE_VERSION)" \
		--binary "$(RELEASE_OUTPUT_DIR)/$(APP_NAME)" \
		--install-path "$(RELEASE_INSTALL_PATH)" \
		--man-page "$(MAN_PAGE_PATH)" \
		--man-install-path "$(MAN_INSTALL_PATH_MACOS)" \
		--output "$(RELEASE_PKG_PATH)"

release-pkg-one:
	@mkdir -p "$(RELEASE_PACKAGE_DIR)"
	@case "$(RELEASE_FORMAT)" in \
		pkg) \
			if [[ "$(RELEASE_GOOS)" != "darwin" ]]; then \
				echo "pkg format requires RELEASE_GOOS=darwin"; \
				exit 1; \
			fi; \
			./scripts/package/macos/build_pkg.sh \
				--app-name "$(APP_NAME)" \
				--version "$(RELEASE_VERSION)" \
				--binary "$(RELEASE_BINARY_PATH)" \
				--install-path "$(RELEASE_INSTALL_PATH)" \
				--man-page "$(MAN_PAGE_PATH)" \
				--man-install-path "$(MAN_INSTALL_PATH_MACOS)" \
				--output "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).pkg"; \
			;; \
		deb) \
			./scripts/package/linux/build_deb.sh \
				--app-name "$(APP_NAME)" \
				--version "$(RELEASE_VERSION)" \
				--arch "$(RELEASE_ARCH)" \
				--binary "$(RELEASE_BINARY_PATH)" \
				--install-path "$(RELEASE_INSTALL_PATH)" \
				--man-page "$(MAN_PAGE_PATH)" \
				--man-install-path "$(MAN_INSTALL_PATH_LINUX)" \
				--output "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).deb"; \
			;; \
		rpm) \
			./scripts/package/linux/build_rpm.sh \
				--app-name "$(APP_NAME)" \
				--version "$(RELEASE_VERSION)" \
				--arch "$(RELEASE_ARCH)" \
				--binary "$(RELEASE_BINARY_PATH)" \
				--install-path "$(RELEASE_INSTALL_PATH)" \
				--man-page "$(MAN_PAGE_PATH)" \
				--man-install-path "$(MAN_INSTALL_PATH_LINUX)" \
				--output "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).rpm"; \
			;; \
		tar.gz) \
			if [[ ! -f "$(MAN_PAGE_PATH)" ]]; then \
				echo "man page not found: $(MAN_PAGE_PATH)"; \
				exit 1; \
			fi; \
			tmp_dir="$$(mktemp -d)"; \
			trap 'rm -rf "$$tmp_dir"' EXIT; \
			cp "$(RELEASE_BINARY_PATH)" "$$tmp_dir/$(notdir $(RELEASE_BINARY_PATH))"; \
			mkdir -p "$$tmp_dir/$(TARBALL_MAN_PATH)"; \
			install -m 0644 "$(MAN_PAGE_PATH)" "$$tmp_dir/$(TARBALL_MAN_PATH)/$(APP_NAME).1"; \
			tar -C "$$tmp_dir" -czf "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).tar.gz" "$(notdir $(RELEASE_BINARY_PATH))" "$(TARBALL_MAN_PATH)/$(APP_NAME).1"; \
			;; \
		msi) \
			if [[ "$(RELEASE_GOOS)" != "windows" ]]; then \
				echo "msi format requires RELEASE_GOOS=windows"; \
				exit 1; \
			fi; \
			./scripts/package/windows/build_msi.sh \
				--app-name "$(APP_NAME)" \
				--version "$(RELEASE_VERSION)" \
				--arch "$(RELEASE_ARCH)" \
				--binary "$(RELEASE_BINARY_PATH)" \
				--output "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).msi"; \
			;; \
		*) \
			echo "unsupported RELEASE_FORMAT=$(RELEASE_FORMAT)"; \
			exit 1; \
			;; \
	esac

release-validate:
	@./scripts/package/macos/validate_pkg.sh \
		--pkg "$(RELEASE_PKG_PATH)" \
		--install-path "$(RELEASE_INSTALL_PATH)" \
		--man-path "$(MAN_INSTALL_PATH_MACOS)/$(APP_NAME).1" \
		--app-name "$(APP_NAME)"
	@shasum -a 256 "$(RELEASE_PKG_PATH)" > "$(RELEASE_PKG_PATH).sha256"

release-validate-one:
	@case "$(RELEASE_FORMAT)" in \
		pkg) \
			./scripts/package/macos/validate_pkg.sh \
				--pkg "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).pkg" \
				--install-path "$(RELEASE_INSTALL_PATH)" \
				--man-path "$(MAN_INSTALL_PATH_MACOS)/$(APP_NAME).1" \
				--app-name "$(APP_NAME)"; \
			shasum -a 256 "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).pkg" > "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).pkg.sha256"; \
			;; \
		deb) \
			./scripts/package/linux/validate_deb.sh \
				--pkg "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).deb" \
				--install-path "$(RELEASE_INSTALL_PATH)" \
				--man-path "$(MAN_INSTALL_PATH_LINUX)/$(APP_NAME).1" \
				--app-name "$(APP_NAME)"; \
			shasum -a 256 "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).deb" > "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).deb.sha256"; \
			;; \
		rpm) \
			./scripts/package/linux/validate_rpm.sh \
				--pkg "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).rpm" \
				--install-path "$(RELEASE_INSTALL_PATH)" \
				--man-path "$(MAN_INSTALL_PATH_LINUX)/$(APP_NAME).1" \
				--app-name "$(APP_NAME)"; \
			shasum -a 256 "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).rpm" > "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).rpm.sha256"; \
			;; \
		tar.gz) \
			tar -tzf "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).tar.gz" | grep -Eq "^$(notdir $(RELEASE_BINARY_PATH))$$"; \
			tar -tzf "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).tar.gz" | grep -Eq "^$(TARBALL_MAN_PATH)/$(APP_NAME)\.1$$"; \
			shasum -a 256 "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).tar.gz" > "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).tar.gz.sha256"; \
			;; \
		msi) \
			./scripts/package/windows/validate_msi.sh \
				--pkg "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).msi" \
				--app-name "$(APP_NAME)"; \
			shasum -a 256 "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).msi" > "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).msi.sha256"; \
			;; \
		*) \
			echo "unsupported RELEASE_FORMAT=$(RELEASE_FORMAT)"; \
			exit 1; \
			;; \
	esac

release-checksums:
	@set -euo pipefail; \
	manifest="$(RELEASE_PACKAGE_DIR)/SHA256SUMS"; \
	test -s "$$manifest"; \
	for artifact in "$(RELEASE_PACKAGE_DIR)"/ash-*.tar.gz; do \
		test -f "$$artifact"; \
		name="$$(basename "$$artifact")"; \
		count="$$(grep -Ec "^[0-9a-fA-F]{64}[[:space:]]+$$name$$" "$$manifest")"; \
		test "$$count" -eq 1; \
	done

release-notes:
	@mkdir -p "$(RELEASE_OUTPUT_DIR)"
	@set -o pipefail; \
	previous_tag="$(LATEST_RELEASE_TAG)"; \
	if [[ -n "$$previous_tag" ]]; then \
		git_log="$$(git log "$$previous_tag..HEAD" --oneline --no-decorate --max-count=200)"; \
	else \
		git_log="$$(git log --oneline --no-decorate --max-count=200)"; \
	fi; \
	if [[ -z "$$git_log" ]]; then \
		echo "cannot generate release notes: git history is empty" >&2; \
		exit 1; \
	fi; \
	tmp_path="$(RELEASE_NOTES_PATH).tmp"; \
	trap 'rm -f "$$tmp_path"' EXIT; \
	{ \
		printf '%s\n\n' 'You are preparing release notes for ASH $(RELEASE_VERSION).'; \
		printf '%s\n' 'Summarize the supplied commits into concise, accurate Markdown for a GitHub Release.'; \
		printf '%s\n' 'Group changes by user-facing theme. Highlight important features and fixes.'; \
		printf '%s\n' 'Call out breaking changes and required configuration migrations.'; \
		printf '%s\n' 'Omit routine dependency, test, formatting, and internal-only commits unless they affect users.'; \
		printf '%s\n' 'Do not invent details that are not supported by the supplied history. Start directly with the release notes.'; \
		printf '%s\n\n' 'The release includes multi-provider support, stricter prompt-injection defenses, broker connection reuse, session-scoped scratch workspaces, execution metrics, snooze support, safer pipelines and tool execution, and improved shell and terminal output. Mention the migration from legacy AI configuration to AI_ENDPOINT and AI_MODEL when supported by the history.'; \
		printf '%s\n' 'Commit history:'; \
		printf '%s\n' "$$git_log"; \
	} | NO_COLOR=1 "$(RELEASE_OUTPUT_DIR)/$(APP_NAME)" > "$$tmp_path"; \
	if [[ ! -s "$$tmp_path" ]]; then \
		echo "cannot generate release notes: ash produced empty output" >&2; \
		exit 1; \
	fi; \
	mv "$$tmp_path" "$(RELEASE_NOTES_PATH)"; \
	echo "generated release notes: $(RELEASE_NOTES_PATH)"

release-artifacts:
	@target_oses="$(RELEASE_TARGET_OSES)"; \
	if [[ "$$target_oses" == "auto" ]]; then \
		case "$$(uname -s)" in \
			Darwin) target_oses="darwin" ;; \
			Linux) target_oses="linux" ;; \
			*) echo "unsupported host OS for auto release artifacts"; exit 1 ;; \
		esac; \
	fi; \
	for os_name in $$target_oses; do \
		case "$$os_name" in \
			darwin) \
				for arch in $(RELEASE_TARGET_ARCHES); do \
					$(MAKE) release-build-one RELEASE_GOOS=darwin RELEASE_ARCH=$$arch RELEASE_VERSION=$(RELEASE_VERSION); \
					$(MAKE) release-pkg-one RELEASE_GOOS=darwin RELEASE_ARCH=$$arch RELEASE_FORMAT=pkg RELEASE_VERSION=$(RELEASE_VERSION); \
					$(MAKE) release-validate-one RELEASE_GOOS=darwin RELEASE_ARCH=$$arch RELEASE_FORMAT=pkg RELEASE_VERSION=$(RELEASE_VERSION); \
					$(MAKE) release-pkg-one RELEASE_GOOS=darwin RELEASE_ARCH=$$arch RELEASE_FORMAT=tar.gz RELEASE_VERSION=$(RELEASE_VERSION); \
					$(MAKE) release-validate-one RELEASE_GOOS=darwin RELEASE_ARCH=$$arch RELEASE_FORMAT=tar.gz RELEASE_VERSION=$(RELEASE_VERSION); \
				done; \
				;; \
			linux) \
				for arch in $(RELEASE_TARGET_ARCHES); do \
					$(MAKE) release-build-one RELEASE_GOOS=linux RELEASE_ARCH=$$arch RELEASE_VERSION=$(RELEASE_VERSION); \
					$(MAKE) release-pkg-one RELEASE_GOOS=linux RELEASE_ARCH=$$arch RELEASE_FORMAT=deb RELEASE_VERSION=$(RELEASE_VERSION); \
					$(MAKE) release-validate-one RELEASE_GOOS=linux RELEASE_ARCH=$$arch RELEASE_FORMAT=deb RELEASE_VERSION=$(RELEASE_VERSION); \
					$(MAKE) release-pkg-one RELEASE_GOOS=linux RELEASE_ARCH=$$arch RELEASE_FORMAT=rpm RELEASE_VERSION=$(RELEASE_VERSION); \
					$(MAKE) release-validate-one RELEASE_GOOS=linux RELEASE_ARCH=$$arch RELEASE_FORMAT=rpm RELEASE_VERSION=$(RELEASE_VERSION); \
					$(MAKE) release-pkg-one RELEASE_GOOS=linux RELEASE_ARCH=$$arch RELEASE_FORMAT=tar.gz RELEASE_VERSION=$(RELEASE_VERSION); \
					$(MAKE) release-validate-one RELEASE_GOOS=linux RELEASE_ARCH=$$arch RELEASE_FORMAT=tar.gz RELEASE_VERSION=$(RELEASE_VERSION); \
				done; \
				;; \
			windows) \
				for arch in $(RELEASE_TARGET_ARCHES); do \
					$(MAKE) release-build-one RELEASE_GOOS=windows RELEASE_ARCH=$$arch RELEASE_VERSION=$(RELEASE_VERSION); \
					$(MAKE) release-pkg-one RELEASE_GOOS=windows RELEASE_ARCH=$$arch RELEASE_FORMAT=msi RELEASE_VERSION=$(RELEASE_VERSION); \
					$(MAKE) release-validate-one RELEASE_GOOS=windows RELEASE_ARCH=$$arch RELEASE_FORMAT=msi RELEASE_VERSION=$(RELEASE_VERSION); \
				done; \
				;; \
			*) \
				echo "unsupported RELEASE_TARGET_OSES entry: $$os_name"; \
				exit 1; \
				;; \
		esac; \
	done

release-publish:
	@current_branch="$$(git rev-parse --abbrev-ref HEAD)"; \
	if [[ "$$current_branch" == "HEAD" ]]; then \
		echo "cannot publish release from detached HEAD"; \
		exit 1; \
	fi; \
	./scripts/release/push_with_retry.sh "pushing branch $$current_branch to origin" git push origin "$$current_branch" && \
	echo "pushed branch $$current_branch to origin"
	@head_sha="$$(git rev-parse HEAD)"; \
	local_sha="$$(git rev-parse -q --verify "refs/tags/$(RELEASE_VERSION)^{}" 2>/dev/null || true)"; \
	if [[ -n "$$local_sha" && "$$local_sha" != "$$head_sha" ]]; then \
		echo "local tag $(RELEASE_VERSION) already exists and points to $$local_sha, not HEAD ($$head_sha)"; \
		exit 1; \
	fi; \
	if [[ -z "$$local_sha" ]]; then \
		git tag -a "$(RELEASE_VERSION)" -F "$(RELEASE_NOTES_PATH)"; \
		echo "created local tag $(RELEASE_VERSION)"; \
	else \
		echo "local tag $(RELEASE_VERSION) already exists at HEAD"; \
	fi
	@echo "pushing tag $(RELEASE_VERSION) to origin"; \
	./scripts/release/push_with_retry.sh "pushing tag $(RELEASE_VERSION) to origin" git push origin "refs/tags/$(RELEASE_VERSION):refs/tags/$(RELEASE_VERSION)" && \
	echo "pushed tag $(RELEASE_VERSION) to origin"

release-watch:
	@if [[ "$(RELEASE_WATCH)" != "1" ]]; then \
		echo "release watch disabled (set RELEASE_WATCH=1 to enable)"; \
		exit 0; \
	fi
	@if ! command -v gh >/dev/null 2>&1; then \
		echo "gh CLI not found; skipping remote release watch"; \
		exit 0; \
	fi
	@repo="$$(gh repo view --json nameWithOwner --jq .nameWithOwner 2>/dev/null || true)"; \
	if [[ -z "$$repo" ]]; then \
		echo "unable to resolve GitHub repository from gh; skipping remote release watch"; \
		exit 0; \
	fi; \
	echo "launching release dashboard for tag $(RELEASE_VERSION)"; \
	if ! ./scripts/release/release_dashboard.sh --tag "$(RELEASE_VERSION)" --repo "$$repo"; then \
		if [[ "$(RELEASE_WATCH_STRICT)" == "1" ]]; then \
			exit 1; \
		fi; \
		echo "release workflow reported failure, but continuing because RELEASE_WATCH_STRICT=$(RELEASE_WATCH_STRICT)"; \
	fi

release-dashboard:
	@./scripts/release/release_dashboard.sh --tag "$(RELEASE_VERSION)"
