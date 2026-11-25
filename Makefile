SHELL := /bin/bash
.DEFAULT_GOAL := all

include config.mk
include lint.mk
include test.mk
include security.mk
include build.mk
include release.mk

# --- Top-level quality gates ---

.PHONY: all verify verify-ci config hooks-install sync-route-words update-dependencies
.PHONY: verify-lint verify-test verify-race verify-cover verify-vet verify-staticcheck verify-security verify-fuzz verify-benchmark

all: verify install

config:
	@GOLANGCI_LINT_VERSION=$(GOLANGCI_LINT_VERSION) \
	GOSEC_VERSION=$(GOSEC_VERSION) \
	GOVULNCHECK_VERSION=$(GOVULNCHECK_VERSION) \
	STATICCHECK_VERSION=$(STATICCHECK_VERSION) \
	GITLEAKS_VERSION=$(GITLEAKS_VERSION) \
	YAMLFMT_VERSION=$(YAMLFMT_VERSION) \
	RUFF_VERSION=$(RUFF_VERSION) \
	MARKDOWNLINT_CLI2_VERSION=$(MARKDOWNLINT_CLI2_VERSION) \
	HTML_VALIDATE_VERSION=$(HTML_VALIDATE_VERSION) \
	NODE_VERSION=$(NODE_VERSION) \
	NVM_VERSION=$(NVM_VERSION) \
	ACTIONLINT_VERSION=$(ACTIONLINT_VERSION) \
	PIP_AUDIT_VERSION=$(PIP_AUDIT_VERSION) \
	CARGO_AUDIT_VERSION=$(CARGO_AUDIT_VERSION) \
	./scripts/dev/install_dev_tools.sh
	@$(MAKE) --no-print-directory hooks-install

hooks-install:
	@./scripts/dev/install_hooks.sh

# verify runs the full quality gate quietly. Each correctness phase is a
# prerequisite that can run in parallel under -j. benchmark is deliberately
# last: it depends on every other phase, and runs only after they finish, so
# benchmark timing is not distorted by concurrent test, lint, or security work.
verify: verify-lint verify-test verify-race verify-cover verify-vet verify-staticcheck verify-security verify-fuzz verify-benchmark
	@echo "verify: all checks passed"

verify-lint:
	@./scripts/dev/run-quiet.sh "lint" $(MAKE) --no-print-directory check-static

verify-test:
	@./scripts/dev/run-quiet.sh "test" $(MAKE) --no-print-directory test

verify-race:
	@./scripts/dev/run-quiet.sh "test-race" $(MAKE) --no-print-directory test-race

verify-cover:
	@./scripts/dev/run-quiet.sh "test-cover" $(MAKE) --no-print-directory test-cover

verify-vet:
	@./scripts/dev/run-quiet.sh "vet" $(MAKE) --no-print-directory vet

verify-staticcheck:
	@./scripts/dev/run-quiet.sh "staticcheck" $(MAKE) --no-print-directory staticcheck

verify-security:
	@./scripts/dev/run-quiet.sh "security" $(MAKE) --no-print-directory check-security

verify-fuzz:
	@./scripts/dev/run-quiet.sh "test-fuzz" $(MAKE) --no-print-directory test-fuzz

verify-benchmark: verify-lint verify-test verify-race verify-cover verify-vet verify-staticcheck verify-security verify-fuzz
	@./scripts/dev/run-quiet.sh "benchmark" $(MAKE) --no-print-directory benchmark

# Used by scheduled release automation before it creates a tag.
verify-ci:
	@$(MAKE) --no-print-directory pre-push

sync-route-words:
	@cd internal/app && go run ../../cmd/ash --internal-sync-route-words

update-dependencies:
	@UPDATE_DEPENDENCIES_ASSUME_YES="$(UPDATE_DEPENDENCIES_ASSUME_YES)" GO111MODULE=off GOTOOLCHAIN=local GOWORK=off go run ./scripts/dev/update-dependencies/main.go
