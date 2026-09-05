PLUGIN_NAME := flip_a_coin
BUILD_DIR ?= ../../../../../bin/plugins
OUTPUT_PATH ?= $(BUILD_DIR)/$(PLUGIN_NAME)

.PHONY: all build test lint clean

all: build

build:
	@mkdir -p "$(BUILD_DIR)"
	@cargo build --release
	@cp target/release/$(PLUGIN_NAME) "$(OUTPUT_PATH)"

test:
	@cargo test --verbose

lint:
	@cargo check
	@if command -v cargo-clippy >/dev/null 2>&1; then \
		cargo clippy -- -D warnings; \
	fi

clean:
	@cargo clean
	@rm -f "$(OUTPUT_PATH)"
