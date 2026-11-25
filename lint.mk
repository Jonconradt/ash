# Lint and static-language quality targets.

.PHONY: lint golangci-lint language-lint site-lint yaml-lint python-lint markdown-lint static-config-lint shell-lint html-lint site-locales check-static

check-static: lint

lint: language-lint site-lint yaml-lint python-lint markdown-lint plugins-lint static-config-lint golangci-lint
	@echo "lint: ok"

golangci-lint:
	@./scripts/dev/run-quiet.sh "golangci-lint" go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run --allow-serial-runners ./...

language-lint:
	@./scripts/dev/run-quiet.sh "language-catalogs" go run ./cmd/langcheck

site-lint:
	@./scripts/dev/run-quiet.sh "site-lint" sh -n site/install.sh
	@./scripts/dev/run-quiet.sh "site-js-syntax" node --check site/site.js

static-config-lint: shell-lint html-lint
	@./scripts/dev/run-quiet.sh "json-syntax" python3 scripts/dev/validate_json.py
	@./scripts/dev/run-quiet.sh "gofmt" ./scripts/dev/check_gofmt.sh
	@./scripts/dev/run-quiet.sh "actionlint" go run github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION) .github/workflows/*.yml

shell-lint:
	@./scripts/dev/run-quiet.sh "shellcheck" ./scripts/dev/check_shell.sh

html-lint:
	@./scripts/dev/run-quiet.sh "html-validate" npx --yes html-validate@$(HTML_VALIDATE_VERSION) "site/*.html" "site/*/*.html"

site-locales:
	@python3 site/generate_locales.py $(foreach locale,$(SITE_LOCALES),--locale $(locale))

yaml-lint:
	@./scripts/dev/run-quiet.sh "yaml-lint" go run github.com/google/yamlfmt/cmd/yamlfmt@$(YAMLFMT_VERSION) -lint .github/workflows/*.yml

python-lint:
	@./scripts/dev/run-quiet.sh "ruff-check" uvx ruff@$(RUFF_VERSION) check internal/app/ash_bootstrap/tools
	@./scripts/dev/run-quiet.sh "ruff-format" uvx ruff@$(RUFF_VERSION) format --check internal/app/ash_bootstrap/tools

markdown-lint:
	@./scripts/dev/run-quiet.sh "markdown-lint" npx --yes markdownlint-cli2@$(MARKDOWNLINT_CLI2_VERSION) "**/*.md" "!dist/**" "!.venv/**" "!node_modules/**" "!**/node_modules/**" "!**/target/**"
