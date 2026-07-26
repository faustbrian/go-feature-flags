GO ?= go
GOLANGCI_LINT ?= go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2
GOVULNCHECK ?= go run golang.org/x/vuln/cmd/govulncheck@v1.6.0
STATICCHECK ?= go run honnef.co/go/tools/cmd/staticcheck@v0.7.0
ACTIONLINT ?= go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12
FUZZ_TIME ?= 2s
BENCH_TIME ?= 100ms

.PHONY: actionlint architecture benchmark check check-all coverage docs format \
	format-check fuzz integration leak lint mutation race staticcheck test \
	tidy-check vet vuln

format:
	gofmt -w .

format-check:
	test -z "$$(gofmt -l .)"

tidy-check:
	$(GO) mod tidy -diff

test:
	$(GO) test ./... -count=1

race:
	$(GO) test -race ./... -count=1

coverage:
	./scripts/check-coverage.sh

fuzz:
	./scripts/check-fuzz.sh "$(FUZZ_TIME)"

mutation:
	./scripts/check-mutation.sh

benchmark:
	$(GO) test . -run '^$$' -bench Benchmark -benchmem \
		-benchtime="$(BENCH_TIME)"

leak:
	$(GO) test . -run '^TestNoGoroutineLeaks$$' -count=1

integration:
	$(GO) test -tags=integration ./postgres ./valkey -count=1

docs:
	./scripts/check-docs.sh

vet:
	$(GO) vet ./...

actionlint:
	$(ACTIONLINT)

architecture:
	./scripts/check-architecture.sh

lint:
	$(GOLANGCI_LINT) run --timeout=5m ./...

staticcheck:
	$(STATICCHECK) ./...

vuln:
	$(GOVULNCHECK) ./...

check: tidy-check format-check vet architecture test race coverage fuzz \
	mutation leak benchmark docs actionlint

check-all: check lint staticcheck vuln
