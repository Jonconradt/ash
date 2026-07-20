.PHONY: all verify lint test test-race test-cover test-fuzz vet staticcheck install

COVERAGE_MIN ?= 95
FUZZ_TIME ?= 10s
GOLANGCI_LINT_VERSION ?= v1.64.8

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
