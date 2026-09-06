PLUGIN_NAME := flip_a_coin
BUILD_DIR ?= ../../../../../bin/plugins
OUTPUT_PATH ?= $(BUILD_DIR)/$(PLUGIN_NAME)

# TARGET_GOOS/TARGET_GOARCH mirror the top-level Makefile's RELEASE_GOOS/RELEASE_ARCH
# and select a Rust target triple for cross-compiled release builds; left empty (the
# default for local dev builds) this builds natively via plain `cargo build --release`.
TARGET_GOOS ?=
TARGET_GOARCH ?=

.PHONY: all build test lint clean

all: build

RUN_QUIET := ../../../../../scripts/dev/run-quiet.sh

# Same-OS cross-arch builds (e.g. linux/amd64 host -> linux/arm64 target, or either
# macOS arch on the other) use `cargo build --target` with rustup + a cross-linker
# when needed. Cross-OS builds (freebsd from a Linux host) need `cross` (cross-rs),
# which cross-compiles inside a Docker image with the target's own libc/sysroot.
build:
	@mkdir -p "$(BUILD_DIR)"
	@host_os="$$(uname -s | tr '[:upper:]' '[:lower:]')"; \
	case "$$(uname -m)" in \
		x86_64) host_arch=amd64 ;; \
		arm64|aarch64) host_arch=arm64 ;; \
		*) host_arch="$$(uname -m)" ;; \
	esac; \
	target_os="$${TARGET_GOOS:-$$host_os}"; \
	target_arch="$${TARGET_GOARCH:-$$host_arch}"; \
	case "$$target_os-$$target_arch" in \
		darwin-amd64) triple=x86_64-apple-darwin ;; \
		darwin-arm64) triple=aarch64-apple-darwin ;; \
		linux-amd64) triple=x86_64-unknown-linux-gnu ;; \
		linux-arm64) triple=aarch64-unknown-linux-gnu ;; \
		freebsd-amd64) triple=x86_64-unknown-freebsd ;; \
		*) echo "flip_a_coin: unsupported target $$target_os/$$target_arch"; exit 1 ;; \
	esac; \
	if [ "$$target_os" = "$$host_os" ] && [ "$$target_arch" = "$$host_arch" ]; then \
		$(RUN_QUIET) "cargo-build" cargo build --release; \
		bin_path="target/release/$(PLUGIN_NAME)"; \
	elif [ "$$target_os" = "$$host_os" ]; then \
		rustup target add "$$triple" >/dev/null 2>&1 || true; \
		if [ "$$target_os" = "linux" ] && [ "$$target_arch" = "arm64" ]; then \
			export CARGO_TARGET_AARCH64_UNKNOWN_LINUX_GNU_LINKER=aarch64-linux-gnu-gcc; \
		fi; \
		$(RUN_QUIET) "cargo-build-cross" cargo build --release --target "$$triple"; \
		bin_path="target/$$triple/release/$(PLUGIN_NAME)"; \
	else \
		command -v cross >/dev/null 2>&1 || { \
			echo "flip_a_coin: 'cross' is required to build $$target_os/$$target_arch from a $$host_os host"; \
			echo "install with: cargo install cross --git https://github.com/cross-rs/cross --locked"; \
			exit 1; \
		}; \
		$(RUN_QUIET) "cross-build" cross build --release --target "$$triple"; \
		bin_path="target/$$triple/release/$(PLUGIN_NAME)"; \
	fi; \
	cp "$$bin_path" "$(OUTPUT_PATH)"

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
