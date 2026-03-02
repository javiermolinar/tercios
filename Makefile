GOLANGCI_LINT_VERSION ?= v1.55.2
BINARY_NAME ?= tercios
BIN_DIR ?= bin

.PHONY: build test lint vendor tidy run

build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/tercios

test:
	go test ./...

lint:
	go run -modfile=tools/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run ./...

vendor:
	go mod tidy
	go mod vendor

tidy:
	go mod tidy

run:
	go run ./cmd/tercios
