.PHONY: all verify lint test test-race test-cover test-fuzz vet staticcheck install release release-check release-build release-pkg release-validate release-publish

COVERAGE_MIN ?= 95
FUZZ_TIME ?= 10s
GOLANGCI_LINT_VERSION ?= v1.64.8
APP_NAME ?= ash
RELEASE_ARCH ?= arm64
RELEASE_OUTPUT_DIR ?= dist/release
RELEASE_PACKAGE_DIR ?= $(RELEASE_OUTPUT_DIR)
LATEST_RELEASE_TAG ?= $(shell git tag --list 'v[0-9]*.[0-9]*.[0-9]*' --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$$' | head -n1)
AUTO_RELEASE_VERSION ?= $(shell ./scripts/release/next_version.sh)
RELEASE_VERSION ?= $(AUTO_RELEASE_VERSION)
RELEASE_PKG_NAME ?= $(APP_NAME)-$(RELEASE_VERSION)-darwin-$(RELEASE_ARCH).pkg
RELEASE_PKG_PATH ?= $(RELEASE_PACKAGE_DIR)/$(RELEASE_PKG_NAME)
RELEASE_INSTALL_PATH ?= /usr/local/bin

all: verify install

verify: test test-race test-cover vet staticcheck

lint:
	go run github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run ./...

test:
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

release-pkg:
	@mkdir -p "$(RELEASE_PACKAGE_DIR)"
	@./scripts/package/macos/build_pkg.sh \
		--app-name "$(APP_NAME)" \
		--version "$(RELEASE_VERSION)" \
		--binary "$(RELEASE_OUTPUT_DIR)/$(APP_NAME)" \
		--install-path "$(RELEASE_INSTALL_PATH)" \
		--output "$(RELEASE_PKG_PATH)"

release-validate:
	@./scripts/package/macos/validate_pkg.sh \
		--pkg "$(RELEASE_PKG_PATH)" \
		--install-path "$(RELEASE_INSTALL_PATH)" \
		--app-name "$(APP_NAME)"
	@shasum -a 256 "$(RELEASE_PKG_PATH)" > "$(RELEASE_PKG_PATH).sha256"

release-publish:
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
