BINARY     := lattice-cost
VERSION    := 1.0.0
BUILD_TIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS    := -ldflags "-X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME) -s -w"

.PHONY: all build install tidy test lint clean run demo build-all

all: tidy build

build:
	go build $(LDFLAGS) -o $(BINARY) .

install:
	go install $(LDFLAGS) .

tidy:
	go mod tidy

test:
	go test ./... -race -count=1

test-cover:
	go test ./... -race -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html

lint:
	staticcheck ./...

## run: Start the server (requires Redis on localhost:6379)
run: build
	./$(BINARY) server

## demo: Show route preview for different prompt types
demo: build
	@echo "\n--- Simple prompt ---"
	./$(BINARY) route "What is the capital of France?"
	@echo "\n--- Complex prompt ---"
	./$(BINARY) route "Analyze the security architecture of a microservices system and provide a detailed implementation plan for zero-trust networking with mTLS between all services, including code examples for Go and Python."
	@echo "\n--- Models pricing table ---"
	./$(BINARY) models

## build-all: Cross-compile for Linux, macOS, Windows
build-all:
	GOOS=linux   GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY)-linux-amd64 .
	GOOS=darwin  GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY)-darwin-amd64 .
	GOOS=darwin  GOARCH=arm64 go build $(LDFLAGS) -o dist/$(BINARY)-darwin-arm64 .
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY)-windows-amd64.exe .

clean:
	rm -f $(BINARY) $(BINARY).exe
	rm -rf dist/ coverage.out coverage.html
