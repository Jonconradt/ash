# Build, plugin, install, and broker targets.

.PHONY: plugins-deps plugins-build plugins-test plugins-lint plugins-clean plugins-install build restart-broker install

# Install per-plugin dependencies exactly once per make graph so plugins-lint
# and plugins-build can run concurrently without racing on npm install. The
# deps target is optional in plugin Makefiles.
plugins-deps:
	@set -eu; pids=""; status=0; \
	for dir in $(PLUGINS_SRC_DIR)/*; do \
		if [ -f "$$dir/Makefile" ] && grep -qE '^deps:' "$$dir/Makefile"; then \
			./scripts/dev/run-quiet.sh "plugin-deps:$$(basename $$dir)" $(MAKE) -C "$$dir" deps & pids="$$pids $$!"; \
		fi; \
	done; \
	for pid in $$pids; do wait "$$pid" || status=1; done; \
	[[ "$$status" -eq 0 ]]

plugins-build: plugins-deps
	@mkdir -p "$(PLUGINS_BIN_DIR)"
	@set -eu; pids=""; status=0; \
	target_goos="$(PLUGIN_TARGET_GOOS)"; \
	if [[ -z "$$target_goos" ]]; then target_goos="$$(uname -s | tr '[:upper:]' '[:lower:]')"; fi; \
	for dir in $(PLUGINS_SRC_DIR)/*; do \
		if [ -f "$$dir/Makefile" ]; then \
			if [[ "$$target_goos" == "darwin" && -f "$$dir/Cargo.toml" ]]; then \
				rm -f "$(PLUGINS_BIN_DIR)/$$(basename "$$dir")"; \
				echo "skipping Rust plugin for macOS: $$(basename "$$dir")"; \
				continue; \
			fi; \
			./scripts/dev/run-quiet.sh "plugin-build:$$(basename $$dir)" $(MAKE) -C "$$dir" build BUILD_DIR="$(abspath $(PLUGINS_BIN_DIR))" TARGET_GOOS="$(PLUGIN_TARGET_GOOS)" TARGET_GOARCH="$(PLUGIN_TARGET_GOARCH)" & pids="$$pids $$!"; \
		fi; \
	done; \
	for pid in $$pids; do wait "$$pid" || status=1; done; \
	[[ "$$status" -eq 0 ]]
	@echo "plugins-build: ok"

plugins-test: plugins-build
	@set -eu; pids=""; status=0; \
	for dir in $(PLUGINS_SRC_DIR)/*; do \
		if [ -f "$$dir/Makefile" ]; then \
			./scripts/dev/run-quiet.sh "plugin-test:$$(basename $$dir)" $(MAKE) -C "$$dir" test & pids="$$pids $$!"; \
		fi; \
	done; \
	for pid in $$pids; do wait "$$pid" || status=1; done; \
	[[ "$$status" -eq 0 ]]
	@echo "plugins-test: ok"

plugins-lint: plugins-deps
	@set -eu; pids=""; status=0; \
	for dir in $(PLUGINS_SRC_DIR)/*; do \
		if [ -f "$$dir/Makefile" ]; then \
			./scripts/dev/run-quiet.sh "plugin-lint:$$(basename $$dir)" $(MAKE) -C "$$dir" lint & pids="$$pids $$!"; \
		fi; \
	done; \
	for pid in $$pids; do wait "$$pid" || status=1; done; \
	[[ "$$status" -eq 0 ]]
	@echo "plugins-lint: ok"

plugins-clean:
	@for dir in $(PLUGINS_SRC_DIR)/*; do \
		if [ -f "$$dir/Makefile" ]; then \
			$(MAKE) -C "$$dir" clean || exit 1; \
		fi; \
	done
	@rm -rf "$(PLUGINS_BIN_DIR)"

plugins-install: plugins-build
	@mkdir -p "$(LOCAL_PLUGINS_DIR)"
	@if [ -d "$(PLUGINS_BIN_DIR)" ]; then \
		cp -f "$(PLUGINS_BIN_DIR)"/* "$(LOCAL_PLUGINS_DIR)/" 2>/dev/null || true; \
	fi

build: lint test  site-locales plugins-build plugins-install
	@mkdir -p "$(BUILD_BIN_DIR)"
	@go build -o "$(BUILD_BIN_DIR)/$(APP_NAME)" -ldflags "-X github.com/Jonconradt/ash/internal/app.ashVersion=$(BUILD_VERSION) -X github.com/Jonconradt/ash/internal/app.ashCommit=$(BUILD_COMMIT) -X github.com/Jonconradt/ash/internal/app.ashDevelopmentBuild=true" ./cmd/ash
	@go build -o "$(BUILD_BIN_DIR)/ash-broker" -ldflags "-X main.version=$(BUILD_VERSION)-dev+$(BUILD_COMMIT)" ./cmd/ash-broker
	@$(MAKE) restart-broker

# Restart the broker attached to this shell after replacing its binary.
restart-broker:
	@if [[ -n "$${ASH_BROKER_SOCKET:-}" && -n "$${ASH_BROKER_TOKEN:-}" && -n "$${ASH_BROKER_PID:-}" ]]; then \
		if ! "$(BUILD_BIN_DIR)/ash-broker" --restart "$(abspath $(BUILD_BIN_DIR)/ash-broker)"; then \
			echo "restart-broker: hot restart failed; if this shell still uses an older Homebrew broker, restart the shell once and retry"; \
			exit 1; \
		fi; \
	else \
		echo "restart-broker: no broker is attached to this shell; new shells will start the built binary when BUILD_BIN_DIR is on PATH"; \
	fi

install: test lint security plugins-build plugins-install
	@mkdir -p "$(LOCAL_BIN_DIR)"
	@go build -o "$(LOCAL_BINARY_PATH)" -ldflags "-X github.com/Jonconradt/ash/internal/app.ashVersion=$(BUILD_VERSION) -X github.com/Jonconradt/ash/internal/app.ashCommit=$(BUILD_COMMIT) -X github.com/Jonconradt/ash/internal/app.ashDevelopmentBuild=true" ./cmd/ash
	@go build -o "$(LOCAL_BIN_DIR)/ash-broker" ./cmd/ash-broker
	@$(MAKE) restart-broker
	@"$(LOCAL_BINARY_PATH)" install --shell bash --shell-integration --overwrite
