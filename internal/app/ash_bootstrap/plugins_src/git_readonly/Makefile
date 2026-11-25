PLUGIN_NAME := git_readonly
BUILD_DIR ?= ../../../../../bin/plugins
OUTPUT_PATH ?= $(BUILD_DIR)/$(PLUGIN_NAME)

TARGET_GOOS ?=
TARGET_GOARCH ?=

GOLANGCI_LINT_VERSION ?= v2.14.0
GOSEC_VERSION ?= v2.29.0

.PHONY: all build test lint security clean

all: build

RUN_QUIET := ../../../../../scripts/dev/run-quiet.sh

build:
	@mkdir -p "$(BUILD_DIR)"
	@if [ -n "$(TARGET_GOOS)" ] && [ -n "$(TARGET_GOARCH)" ]; then \
		$(RUN_QUIET) "go-build" env GOOS="$(TARGET_GOOS)" GOARCH="$(TARGET_GOARCH)" CGO_ENABLED=0 go build -o "$(OUTPUT_PATH)" .; \
	else \
		$(RUN_QUIET) "go-build" go build -o "$(OUTPUT_PATH)" .; \
	fi

test:
	@$(RUN_QUIET) "go-test" go test ./...
	@echo "test: ok"

lint:
	@$(RUN_QUIET) "go-vet" go vet ./...
	@$(RUN_QUIET) "golangci-lint" go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run --allow-serial-runners .
	@echo "lint: ok"

security:
	@go run github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION) -quiet ./...

clean:
	@rm -f "$(OUTPUT_PATH)"
