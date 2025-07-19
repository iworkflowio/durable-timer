# Distributed Durable Timer Service - CLI Makefile

.PHONY: build clean test lint fmt vet deps install help

# Build variables
BINARY_NAME=timer-cli
BUILD_DIR=build
MAIN_PATH=./cmd

# Go commands
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
GOFMT=gofmt
GOVET=$(GOCMD) vet
GOINSTALL=$(GOCMD) install

# Build the CLI binary
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)

# Clean build artifacts
clean:
	@echo "Cleaning..."
	$(GOCLEAN)
	@rm -rf $(BUILD_DIR)

# Run tests
test:
	@echo "Running tests..."
	$(GOTEST) -v ./...

# Lint code
lint:
	@echo "Running linter..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not installed, skipping..."; \
	fi

# Format code
fmt:
	@echo "Formatting code..."
	$(GOFMT) -s -w .

# Vet code
vet:
	@echo "Vetting code..."
	$(GOVET) ./...

# Download dependencies
deps:
	@echo "Downloading dependencies..."
	$(GOMOD) download
	$(GOMOD) tidy

# Install CLI binary to GOPATH/bin
install: build
	@echo "Installing $(BINARY_NAME)..."
	$(GOINSTALL) $(MAIN_PATH)

# Show help
help:
	@echo "Available commands:"
	@echo "  build     - Build the CLI binary"
	@echo "  clean     - Clean build artifacts"
	@echo "  test      - Run tests"
	@echo "  lint      - Run linter"
	@echo "  fmt       - Format code"
	@echo "  vet       - Vet code"
	@echo "  deps      - Download dependencies"
	@echo "  install   - Install CLI binary to GOPATH/bin"
	@echo "  help      - Show this help message" 