.PHONY: all verify lint test test-race test-cover test-fuzz vet staticcheck install version release release-check release-build release-pkg release-validate release-publish release-artifacts release-build-one release-pkg-one release-validate-one

COVERAGE_MIN ?= 95
FUZZ_TIME ?= 10s
GOLANGCI_LINT_VERSION ?= v1.64.8
APP_NAME ?= ash
RELEASE_ARCH ?= arm64
RELEASE_OUTPUT_DIR ?= dist/release
RELEASE_PACKAGE_DIR ?= $(RELEASE_OUTPUT_DIR)
RELEASE_TARGET_ARCHES ?= amd64 arm64
RELEASE_TARGET_OSES ?= auto
RELEASE_GOOS ?= darwin
RELEASE_FORMAT ?= pkg
LATEST_RELEASE_TAG ?= $(shell git tag --list 'v[0-9]*.[0-9]*.[0-9]*' --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$$' | head -n1)
AUTO_RELEASE_VERSION ?= $(shell ./scripts/release/next_version.sh)
RELEASE_VERSION ?= $(AUTO_RELEASE_VERSION)
RELEASE_PKG_NAME ?= $(APP_NAME)-$(RELEASE_VERSION)-darwin-$(RELEASE_ARCH).pkg
RELEASE_PKG_PATH ?= $(RELEASE_PACKAGE_DIR)/$(RELEASE_PKG_NAME)
RELEASE_INSTALL_PATH ?= /usr/local/bin
RELEASE_ARTIFACT_BASE ?= $(APP_NAME)-$(RELEASE_VERSION)-$(RELEASE_GOOS)-$(RELEASE_ARCH)
RELEASE_BINARY_PATH ?= $(RELEASE_OUTPUT_DIR)/$(RELEASE_ARTIFACT_BASE)

all: verify install

verify: test test-race test-cover vet staticcheck

lint:
	go run github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run ./...

test:
	bash -n sample_bashrc.txt
	go test ./...

test-race:
	go test -race ./...

test-cover:
	go test -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out | awk '/^total:/ {gsub("%", "", $$3); if ($$3 + 0 < $(COVERAGE_MIN)) {printf("coverage %.1f%% is below %s%%\n", $$3, "$(COVERAGE_MIN)"); exit 1} else {printf("coverage %.1f%% meets %s%%\n", $$3, "$(COVERAGE_MIN)")}}'

test-fuzz:
	go test -fuzz=Fuzz -fuzztime=$(FUZZ_TIME) ./...

vet:
	go vet ./...

staticcheck:
	@if command -v staticcheck >/dev/null 2>&1; then \
		if ! staticcheck ./...; then \
			echo "staticcheck failed (toolchain mismatch or local setup issue); skipping"; \
		fi; \
	else \
		echo "staticcheck not installed; skipping"; \
	fi

install: test lint
	go install ./...
	ash install --shell bash

version: release-check release-artifacts

release: release-check release-build release-pkg release-validate release-publish

release-check: lint test
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
	GOOS=darwin GOARCH=$(RELEASE_ARCH) CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$(RELEASE_OUTPUT_DIR)/$(APP_NAME)" ./...

release-build-one:
	@mkdir -p "$(RELEASE_OUTPUT_DIR)"
	GOOS=$(RELEASE_GOOS) GOARCH=$(RELEASE_ARCH) CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$(RELEASE_BINARY_PATH)" ./...

release-pkg:
	@mkdir -p "$(RELEASE_PACKAGE_DIR)"
	@./scripts/package/macos/build_pkg.sh \
		--app-name "$(APP_NAME)" \
		--version "$(RELEASE_VERSION)" \
		--binary "$(RELEASE_OUTPUT_DIR)/$(APP_NAME)" \
		--install-path "$(RELEASE_INSTALL_PATH)" \
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
				--output "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).pkg"; \
			;; \
		deb) \
			./scripts/package/linux/build_deb.sh \
				--app-name "$(APP_NAME)" \
				--version "$(RELEASE_VERSION)" \
				--arch "$(RELEASE_ARCH)" \
				--binary "$(RELEASE_BINARY_PATH)" \
				--install-path "$(RELEASE_INSTALL_PATH)" \
				--output "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).deb"; \
			;; \
		rpm) \
			./scripts/package/linux/build_rpm.sh \
				--app-name "$(APP_NAME)" \
				--version "$(RELEASE_VERSION)" \
				--arch "$(RELEASE_ARCH)" \
				--binary "$(RELEASE_BINARY_PATH)" \
				--install-path "$(RELEASE_INSTALL_PATH)" \
				--output "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).rpm"; \
			;; \
		tar.gz) \
			tar -C "$(RELEASE_OUTPUT_DIR)" -czf "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).tar.gz" "$(notdir $(RELEASE_BINARY_PATH))"; \
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
		--app-name "$(APP_NAME)"
	@shasum -a 256 "$(RELEASE_PKG_PATH)" > "$(RELEASE_PKG_PATH).sha256"

release-validate-one:
	@case "$(RELEASE_FORMAT)" in \
		pkg) \
			./scripts/package/macos/validate_pkg.sh \
				--pkg "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).pkg" \
				--install-path "$(RELEASE_INSTALL_PATH)" \
				--app-name "$(APP_NAME)"; \
			shasum -a 256 "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).pkg" > "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).pkg.sha256"; \
			;; \
		deb) \
			./scripts/package/linux/validate_deb.sh \
				--pkg "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).deb" \
				--install-path "$(RELEASE_INSTALL_PATH)" \
				--app-name "$(APP_NAME)"; \
			shasum -a 256 "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).deb" > "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).deb.sha256"; \
			;; \
		rpm) \
			./scripts/package/linux/validate_rpm.sh \
				--pkg "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).rpm" \
				--install-path "$(RELEASE_INSTALL_PATH)" \
				--app-name "$(APP_NAME)"; \
			shasum -a 256 "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).rpm" > "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).rpm.sha256"; \
			;; \
		tar.gz) \
			tar -tzf "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).tar.gz" | grep -Eq "^$(notdir $(RELEASE_BINARY_PATH))$$"; \
			shasum -a 256 "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).tar.gz" > "$(RELEASE_PACKAGE_DIR)/$(RELEASE_ARTIFACT_BASE).tar.gz.sha256"; \
			;; \
		*) \
			echo "unsupported RELEASE_FORMAT=$(RELEASE_FORMAT)"; \
			exit 1; \
			;; \
	esac

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
	git push origin "$$current_branch"; \
	echo "pushed branch $$current_branch to origin"
	@head_sha="$$(git rev-parse HEAD)"; \
	local_sha="$$(git rev-parse -q --verify "refs/tags/$(RELEASE_VERSION)^{}" 2>/dev/null || true)"; \
	if [[ -n "$$local_sha" && "$$local_sha" != "$$head_sha" ]]; then \
		echo "local tag $(RELEASE_VERSION) already exists and points to $$local_sha, not HEAD ($$head_sha)"; \
		exit 1; \
	fi; \
	if [[ -z "$$local_sha" ]]; then \
		git tag -a "$(RELEASE_VERSION)" -m "release $(RELEASE_VERSION)"; \
		echo "created local tag $(RELEASE_VERSION)"; \
	else \
		echo "local tag $(RELEASE_VERSION) already exists at HEAD"; \
	fi
	@remote_sha="$$(git ls-remote --tags origin "refs/tags/$(RELEASE_VERSION)^{}" | awk '{print $$1}')"; \
	head_sha="$$(git rev-parse HEAD)"; \
	if [[ -n "$$remote_sha" ]]; then \
		if [[ "$$remote_sha" == "$$head_sha" ]]; then \
			echo "remote tag $(RELEASE_VERSION) already exists at HEAD; nothing to push"; \
		else \
			echo "remote tag $(RELEASE_VERSION) already exists and points to $$remote_sha, not HEAD ($$head_sha)"; \
			echo "choose a new RELEASE_VERSION or move the tag manually"; \
			exit 1; \
		fi; \
	else \
		git push origin "refs/tags/$(RELEASE_VERSION):refs/tags/$(RELEASE_VERSION)"; \
		echo "pushed tag $(RELEASE_VERSION) to origin"; \
	fi
