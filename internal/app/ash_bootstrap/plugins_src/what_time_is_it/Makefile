PLUGIN_NAME := what_time_is_it
BUILD_DIR ?= ../../../../../bin/plugins
OUTPUT_PATH ?= $(BUILD_DIR)/$(PLUGIN_NAME)

GOLANGCI_LINT_VERSION ?= v2.13.1
GOSEC_VERSION ?= v2.28.0

.PHONY: all build test lint security clean

all: build

RUN_QUIET := ../../../../../scripts/dev/run-quiet.sh

build:
	@mkdir -p "$(BUILD_DIR)"
	@$(RUN_QUIET) "go-build" go build -o "$(OUTPUT_PATH)" .

test:
	@$(RUN_QUIET) "go-test" go test ./...
	@echo "test: ok"

lint:
	@$(RUN_QUIET) "go-vet" go vet ./...
	@$(RUN_QUIET) "golangci-lint" go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run .
	@echo "lint: ok"

security:
	@go run github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION) -quiet ./...

clean:
	@rm -f "$(OUTPUT_PATH)"
