# Test, race, coverage, fuzz, benchmark, and vet targets.

.PHONY: test site-tests site-generator-tests python-tool-tests go-test test-race test-cover test-fuzz benchmark vet staticcheck

test: plugins-test site-tests site-generator-tests python-tool-tests go-test
	@echo "test: ok"

site-tests:
	@./scripts/dev/run-quiet.sh "site-tests" node --test site/site.test.cjs

site-generator-tests:
	@./scripts/dev/run-quiet.sh "site-generator-tests" python3 -B -m unittest discover -s site -p 'test_*.py'

python-tool-tests:
	@./scripts/dev/run-quiet.sh "python-tool-tests" python3 -B -m unittest discover -s internal/app/ash_bootstrap/tools -p 'test_*.py'

go-test:
	@./scripts/dev/run-quiet.sh "go-test" go test ./...

test-race:
	@./scripts/dev/run-quiet.sh "go-test-race" go test -race ./...

test-cover:
	@./scripts/dev/run-quiet.sh "go-test-cover" go test -coverprofile=coverage.out ./...
	@cov=$$(go tool cover -func=coverage.out | awk '/^total:/ {gsub("%", "", $$3); printf "%.1f", $$3}'); \
	below=$$(awk -v c="$$cov" -v min="$(COVERAGE_MIN)" 'BEGIN {print (c+0 < min+0) ? 1 : 0}'); \
	if [ "$$below" = "0" ]; then \
		echo "test-cover: ok $$cov%"; \
	else \
		echo "test-cover: FAILED coverage $$cov% is below $(COVERAGE_MIN)%"; \
		exit 1; \
	fi

test-fuzz:
	@./scripts/dev/run-quiet.sh "go-test-fuzz" go test -fuzz=Fuzz -fuzztime=$(FUZZ_TIME) ./internal/app

# Benchmarks deliberately use one package and one CPU setting. This is the
# isolated timing mode used by make verify; do not add -p or -cpu parallelism
# here, and keep benchmark as verify's last prerequisite in the Makefile.
benchmark:
	@./scripts/dev/run-quiet.sh "benchmark" go test -p=1 -cpu=1 -bench=. -benchmem ./...

vet:
	@go vet ./...

staticcheck:
	@go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) ./...
