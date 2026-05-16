.PHONY: build vet test lint fmt run install tidy clean all

all: fmt vet lint test build

build:
	go build ./...

vet:
	go vet ./...

test:
	go test ./...

lint:
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "golangci-lint not installed; install: https://golangci-lint.run/usage/install/"; exit 1; }
	golangci-lint run ./...

fmt:
	gofmt -s -w .

run:
	go run ./cmd/claudama
