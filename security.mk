# Security, secret, and pre-push quality targets.

.PHONY: pre-push pre-push-security pre-push-audits pre-push-secrets check-security security npm-audit cargo-audit pip-audit gosec govulncheck

# Run the same fail-closed checks on the exact commit(s) being pushed. The
# phases are plain prerequisites so the automatic -j parallelism runs them
# concurrently inside one make graph (shared prerequisites run exactly once).
pre-push: lint pre-push-security test pre-push-secrets
	@echo "pre-push: all checks passed"

# Static security analysis always runs; the network-backed dependency audits
# run only when the pushed commits change a dependency manifest. `make
# security` (verify, install, CI) still runs every audit unconditionally.
pre-push-security: gosec pre-push-audits

pre-push-audits:
	@./scripts/dev/pre-push-audits.sh "$(PUSH_BASE)"

# Aggregate of the network-backed dependency audits; prerequisites so they
# parallelize under -j.
.PHONY: dependency-audits
dependency-audits: govulncheck npm-audit cargo-audit pip-audit

pre-push-secrets: # gitleaks range scan
	@./scripts/dev/pre-push-secrets.sh "$(PUSH_BASE)" "$(GITLEAKS_VERSION)"

check-security: security
	@./scripts/dev/run-quiet.sh "secrets-history" go run github.com/zricethezav/gitleaks/v8@$(GITLEAKS_VERSION) git --platform github --redact --verbose --log-opts=--all .
	@./scripts/dev/run-quiet.sh "secrets-tree" go run github.com/zricethezav/gitleaks/v8@$(GITLEAKS_VERSION) dir --redact --verbose .

security: gosec govulncheck npm-audit cargo-audit pip-audit
	@echo "security: ok"

npm-audit:
	@./scripts/dev/run-quiet.sh "npm-audit" npm --prefix internal/app/ash_bootstrap/plugins_src/calculator audit --audit-level=high

cargo-audit:
	@./scripts/dev/run-quiet.sh "cargo-audit" bash -c 'cd internal/app/ash_bootstrap/plugins_src/flip_a_coin && cargo audit'

pip-audit:
	@./scripts/dev/run-quiet.sh "pip-audit" uvx --from pip-audit==$(PIP_AUDIT_VERSION) pip-audit -r internal/app/ash_bootstrap/tools/requirements.txt

gosec:
	@./scripts/dev/run-quiet.sh "gosec" go run github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION) -quiet ./...

govulncheck:
	@./scripts/dev/run-quiet.sh "govulncheck" go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...
