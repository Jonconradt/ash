PLUGIN_NAME := what_time_is_it
BUILD_DIR ?= ../../../../../bin/plugins
OUTPUT_PATH ?= $(BUILD_DIR)/$(PLUGIN_NAME)

GOLANGCI_LINT_VERSION ?= v2.13.1
GOSEC_VERSION ?= v2.28.0

.PHONY: all build test lint security clean

all: build

build:
	@mkdir -p "$(BUILD_DIR)"
	@go build -o "$(OUTPUT_PATH)" .

test:
	@go test -v ./...

lint:
	@go vet ./...
	@go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run .

security:
	@go run github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION) -quiet ./...

clean:
	@rm -f "$(OUTPUT_PATH)"
