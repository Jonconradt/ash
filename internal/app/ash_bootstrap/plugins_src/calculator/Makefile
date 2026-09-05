PLUGIN_NAME := calculator
BUILD_DIR ?= ../../../../../bin/plugins
OUTPUT_PATH ?= $(BUILD_DIR)/$(PLUGIN_NAME)

.PHONY: all build test lint security clean

all: build

RUN_QUIET := ../../../../../scripts/dev/run-quiet.sh

build:
	@mkdir -p "$(BUILD_DIR)"
	@if [ ! -d node_modules ]; then $(RUN_QUIET) "npm-install" npm install; fi
	@$(RUN_QUIET) "tsc" npx tsc
	@cp dist/index.js "$(OUTPUT_PATH)"
	@chmod 755 "$(OUTPUT_PATH)"

test: build
	@$(RUN_QUIET) "node-test" node --test dist/calculator.test.js
	@node dist/index.js --expr "2 + 2 * 3" >/dev/null
	@node dist/index.js --ai-docs >/dev/null
	@echo "test: ok"

lint:
	@if [ ! -d node_modules ]; then $(RUN_QUIET) "npm-install" npm install; fi
	@$(RUN_QUIET) "tsc-noemit" npx tsc --noEmit
	@echo "lint: ok"

security:
	@npm audit --audit-level=high

clean:
	@rm -rf dist "$(OUTPUT_PATH)"
