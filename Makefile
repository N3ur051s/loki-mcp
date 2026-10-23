# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOVET=$(GOCMD) vet
GOMOD=$(GOCMD) mod
BINARY_NAME=loki-mcp-server
BINARY_UNIX=$(BINARY_NAME)_unix
MAIN_PATH=./cmd/server
VERSION ?= 0.2.0
LDFLAGS=-s -w -X main.version=$(VERSION)

.PHONY: all build clean test test-race cover vet fmt tidy run run-http deps build-linux docker-build docker-run help

all: fmt vet test build

build:
	$(GOBUILD) -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY_NAME) -v $(MAIN_PATH)

clean:
	$(GOCLEAN)
	rm -f $(BINARY_NAME)
	rm -f $(BINARY_UNIX)

test:
	$(GOTEST) -v ./...

test-race:
	$(GOTEST) -race ./...

cover:
	$(GOTEST) -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -func=coverage.out

vet:
	$(GOVET) ./...

fmt:
	gofmt -w ./cmd ./internal ./pkg

tidy:
	$(GOMOD) tidy

run: build
	MCP_TRANSPORT=stdio ./$(BINARY_NAME)

run-http: build
	MCP_TRANSPORT=http PORT=8080 ./$(BINARY_NAME)

deps:
	$(GOCMD) mod download

# Cross compilation
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOBUILD) -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY_UNIX) -v $(MAIN_PATH)

docker-build:
	docker build --build-arg VERSION=$(VERSION) -t $(BINARY_NAME):$(VERSION) -t $(BINARY_NAME):latest .

docker-run:
	docker run --rm -p 8080:8080 -e MCP_TRANSPORT=http -e LOKI_URL=http://host.docker.internal:3100 $(BINARY_NAME):latest

help:
	@echo "Make commands:"
	@echo "  build        - Build the server binary"
	@echo "  clean        - Remove binaries and cache files"
	@echo "  test         - Run tests"
	@echo "  test-race    - Run tests with the race detector"
	@echo "  cover        - Run tests with coverage"
	@echo "  vet          - Run go vet"
	@echo "  fmt          - Format Go source files"
	@echo "  tidy         - Tidy go.mod/go.sum"
	@echo "  run          - Build and run the stdio server"
	@echo "  run-http     - Build and run the HTTP server on :8080"
	@echo "  deps         - Download dependencies"
	@echo "  build-linux  - Cross-compile for Linux"
	@echo "  docker-build - Build the Docker image"
	@echo "  docker-run   - Run the HTTP server in Docker"
	@echo "  help         - Display this help message"
