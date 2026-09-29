.PHONY: all build test test-race lint vet fmt tidy clean

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

all: test-race lint

build:
	go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/deck ./cmd/deck

test:
	go test ./...

test-race:
	go test -race ./...

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
