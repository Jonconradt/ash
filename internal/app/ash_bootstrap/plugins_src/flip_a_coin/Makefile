PLUGIN_NAME := flip_a_coin
BUILD_DIR ?= ../../../../../bin/plugins
OUTPUT_PATH ?= $(BUILD_DIR)/$(PLUGIN_NAME)

.PHONY: all build test lint clean

all: build

RUN_QUIET := ../../../../../scripts/dev/run-quiet.sh

build:
	@mkdir -p "$(BUILD_DIR)"
	@$(RUN_QUIET) "cargo-build" cargo build --release
	@cp target/release/$(PLUGIN_NAME) "$(OUTPUT_PATH)"

test:
	@$(RUN_QUIET) "cargo-test" cargo test
	@echo "test: ok"

lint:
	@$(RUN_QUIET) "cargo-check" cargo check
	@if command -v cargo-clippy >/dev/null 2>&1; then \
		$(RUN_QUIET) "cargo-clippy" cargo clippy -- -D warnings; \
	fi

clean:
	@cargo clean
	@rm -f "$(OUTPUT_PATH)"
