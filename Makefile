GOLANGCI_LINT_VERSION ?= v1.55.2
BINARY_NAME ?= tercios
BIN_DIR ?= bin
IMAGE_NAME ?= tercios
IMAGE_TAG ?= latest
DOCKER_PLATFORMS ?= linux/amd64
DOCKER_BUILDX_FLAGS ?= --load

.PHONY: build test lint tidy run docker-build docker-run docker-buildx

build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/tercios

test:
	go test ./...

lint:
	go run -modfile=tools/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run ./...

tidy:
	go mod tidy

run:
	go run ./cmd/tercios

docker-build:
	docker build -t $(IMAGE_NAME):$(IMAGE_TAG) -f Dockerfile .

docker-run:
	docker run --rm $(IMAGE_NAME):$(IMAGE_TAG) --dry-run

docker-buildx:
	docker buildx build --platform $(DOCKER_PLATFORMS) $(DOCKER_BUILDX_FLAGS) -t $(IMAGE_NAME):$(IMAGE_TAG) -f Dockerfile .
