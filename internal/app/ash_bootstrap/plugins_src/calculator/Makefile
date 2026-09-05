PLUGIN_NAME := calculator
BUILD_DIR ?= ../../../../../bin/plugins
OUTPUT_PATH ?= $(BUILD_DIR)/$(PLUGIN_NAME)

.PHONY: all build test lint security clean

all: build

build:
	@mkdir -p "$(BUILD_DIR)"
	@if [ ! -d node_modules ]; then npm install; fi
	@npx tsc
	@cp dist/index.js "$(OUTPUT_PATH)"
	@chmod 755 "$(OUTPUT_PATH)"

test: build
	@node --test dist/calculator.test.js
	@node dist/index.js --expr "2 + 2 * 3" >/dev/null
	@node dist/index.js --ai-docs >/dev/null

lint:
	@if [ ! -d node_modules ]; then npm install; fi
	@npx tsc --noEmit

security:
	@npm audit --audit-level=high

clean:
	@rm -rf dist "$(OUTPUT_PATH)"
