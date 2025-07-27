# Distributed Durable Timer Service - Server Makefile

.PHONY: build clean test test-integration lint fmt vet deps run dev docker help

# Build variables
BINARY_NAME=timer-server
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

# Build the server binary
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)

# Clean build artifacts
clean:
	@echo "Cleaning..."
	$(GOCLEAN)
	@rm -rf $(BUILD_DIR)

# Run unit tests
test:
	@echo "Running unit tests..."
	$(GOTEST) -v ./internal/...

# Run integration tests
test-integration:
	@echo "Running integration tests..."
	$(GOTEST) -v ./integTests/...

# Run all tests
test-all: test test-integration

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

# Run the server for development
run: build
	@echo "Running $(BINARY_NAME)..."
	./$(BUILD_DIR)/$(BINARY_NAME)

# Run the server with live reload (requires air)
dev:
	@echo "Starting development server with live reload..."
	@if command -v air >/dev/null 2>&1; then \
		air; \
	else \
		echo "air not installed, install with: go install github.com/cosmtrek/air@latest"; \
		$(MAKE) run; \
	fi

# Build Docker image
docker:
	@echo "Building Docker image..."
	docker build -t timer-server:latest -f ../docker/Dockerfile.server .

# Show help
help:
	@echo "Available commands:"
	@echo "  build             - Build the server binary"
	@echo "  clean             - Clean build artifacts"
	@echo "  test              - Run unit tests"
	@echo "  test-integration  - Run integration tests"
	@echo "  test-all          - Run all tests"
	@echo "  lint              - Run linter"
	@echo "  fmt               - Format code"
	@echo "  vet               - Vet code"
	@echo "  deps              - Download dependencies"
	@echo "  run               - Build and run the server"
	@echo "  dev               - Run with live reload (requires air)"
	@echo "  docker            - Build Docker image"
	@echo "  help              - Show this help message" 


ci-cassandra-integ-test:
	$Q go test -v ./databases/cassandra -cover -coverprofile coverage.cassandra.out -coverpkg ./databases/...

ci-mongodb-integ-test:
	$Q go test -v ./databases/mongodb -cover -coverprofile coverage.mongodb.out -coverpkg ./databases/...

ci-mysql-integ-test:
	$Q go test -v ./databases/mysql -cover -coverprofile coverage.mysql.out -coverpkg ./databases/...

ci-postgresql-integ-test:
	$Q go test -v ./databases/postgresql -cover -coverprofile coverage.postgresql.out -coverpkg ./databases/...

ci-dynamodb-integ-test:
	$Q go test -v ./databases/dynamodb -cover -coverprofile coverage.dynamodb.out -coverpkg ./databases/...

integ-cassandra-test:
	$Q go test -v ./databases/cassandra

integ-mongodb-test:
	$Q go test -v ./databases/mongodb

integ-mysql-test:
	$Q go test -v ./databases/mysql

integ-postgresql-test:
	$Q go test -v ./databases/postgresql

integ-dynamodb-test:
	$Q go test -v ./databases/dynamodb

integ-test-all:
	$Q go test -v ./databases/cassandra
	$Q go test -v ./databases/mongodb
	$Q go test -v ./databases/mysql
	$Q go test -v ./databases/postgresql
	$Q go test -v ./databases/dynamodb
