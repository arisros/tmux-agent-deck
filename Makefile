.PHONY: all build test test-race perf lint vet fmt tidy clean

# pipefail: make perf filters the test output, and must still fail with it.
SHELL := bash
.SHELLFLAGS := -o pipefail -c

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

all: test-race lint

# Writes the same bin/.rev stamp the tpm entrypoint checks, so the next tmux
# load keeps this build instead of rebuilding over it.
build:
	go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/deck ./cmd/deck
	@echo $(VERSION) > bin/.rev

test:
	go test ./...

test-race:
	go test -race ./...

# Alone, so the timings are not skewed by other packages running in parallel.
perf:
	DECK_PERF=1 go test -count=1 -run Performance -v ./test/integration/ | grep -E 'perf_test|^(ok|FAIL|---)'
	go test -run '^$$' -bench HookPath -benchmem ./internal/machine | grep -E 'Benchmark|^ok'

vet:
	go vet ./...

lint:
	golangci-lint run

fmt:
	gofmt -w .

tidy:
	go mod tidy

clean:
	rm -rf bin
