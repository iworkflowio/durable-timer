# Timer Service Server

The core distributed timer service implementation, built in Go for high performance and concurrency.

## Overview

This directory contains the main timer service server that handles:
- Timer lifecycle management (create, update, delete, execute)
- Callback execution with retry logic
- Multi-database support with pluggable adapters
- High-throughput timer scheduling and execution
- Metrics collection and health monitoring

## Technology Stack

- **Language**: Go (Golang) 1.21+
- **Architecture**: Microservice with pluggable database adapters
- **Concurrency**: Goroutines for timer execution and callback processing
- **HTTP Framework**: Standard library or lightweight framework (Gin/Echo)
- **Configuration**: Viper for configuration management

## Directory Structure

```
server/
├── cmd/                   # Main applications and entry points
├── config/                # Configuration management
├── databases/             # Database adapters (8 databases supported)
├── engine/                # Timer execution engine and core logic
├── integTests/            # Integration tests
├── go.mod                 # Go module definition
├── go.sum                 # Go module checksums
└── Makefile               # Build and development commands
```

## Supported Databases

- **Distributed**: Cassandra, MongoDB, TiDB, DynamoDB
- **Traditional SQL**: MySQL, PostgreSQL, Oracle, SQL Server

## Quick Start

```bash
# Download dependencies
make deps

# Build the server
make build

# Run tests
make test-all

# Start development server with live reload
make dev

# Build Docker image
make docker
```

## Development Commands

```bash
make help           # Show all available commands
make build          # Build binary
make test           # Run unit tests
make test-integration  # Run integration tests
make lint           # Run linter
make fmt            # Format code
make run            # Build and run server
```

## Configuration

Server configuration is managed through:
- Environment variables
- Configuration files (YAML/JSON)
- Command-line flags
- Kubernetes ConfigMaps/Secrets

See `internal/config/` for configuration schema and validation.

## API

The server implements the OpenAPI 3.0.3 specification defined in `/api.yaml`:
- `POST /groups/{groupId}/timers` - Create timer
- `GET /groups/{groupId}/timers/{timerId}` - Get timer
- `PUT /groups/{groupId}/timers/{timerId}` - Update timer
- `DELETE /groups/{groupId}/timers/{timerId}` - Delete timer

## Performance

Designed for high-throughput timer processing:
- Millions of concurrent timers
- Sub-second timer execution accuracy
- Horizontal scaling through sharding
- Optimized database query patterns

## Monitoring

Built-in observability features:
- Prometheus metrics
- Structured logging
- Health check endpoints
- Distributed tracing support 