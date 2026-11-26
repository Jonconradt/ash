PLUGIN_NAME := calculator
BUILD_DIR ?= ../../../../../bin/plugins
OUTPUT_PATH ?= $(BUILD_DIR)/$(PLUGIN_NAME)

.PHONY: all build build-locked deps deps-locked test lint lint-locked security clean

all: build

RUN_QUIET := ../../../../../scripts/dev/run-quiet.sh

deps:
	@../../../../../scripts/dev/with-plugin-lock.sh "$(PLUGIN_NAME)" $(MAKE) --no-print-directory deps-locked

deps-locked:
	@if [ ! -d node_modules ]; then $(RUN_QUIET) "npm-install" npm install; fi

build:
	@../../../../../scripts/dev/with-plugin-lock.sh "$(PLUGIN_NAME)" $(MAKE) --no-print-directory build-locked BUILD_DIR="$(BUILD_DIR)" OUTPUT_PATH="$(OUTPUT_PATH)"

build-locked:
	@mkdir -p "$(BUILD_DIR)"
	@if [ ! -d node_modules ]; then $(RUN_QUIET) "npm-install" npm install; fi
	@$(RUN_QUIET) "tsc" npx tsc
	@tmp_output="$(OUTPUT_PATH).tmp.$$"; \
	cp dist/index.js "$$tmp_output"; \
	chmod 755 "$$tmp_output"; \
	mv -f "$$tmp_output" "$(OUTPUT_PATH)"

test: build
	@$(RUN_QUIET) "node-test" node --test dist/calculator.test.js
	@node dist/index.js --expr "2 + 2 * 3" >/dev/null
	@node dist/index.js --ai-docs >/dev/null
	@echo "test: ok"

lint:
	@../../../../../scripts/dev/with-plugin-lock.sh "$(PLUGIN_NAME)" $(MAKE) --no-print-directory lint-locked

lint-locked:
	@if [ ! -d node_modules ]; then $(RUN_QUIET) "npm-install" npm install; fi
	@$(RUN_QUIET) "tsc-noemit" npx tsc --noEmit
	@echo "lint: ok"

security:
	@npm audit --audit-level=high

clean:
	@rm -rf dist "$(OUTPUT_PATH)"
