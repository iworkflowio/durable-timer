# Timer Service CLI

Command-line interface for the distributed timer service, built in Go using the Cobra framework.

## Overview

The CLI provides a comprehensive command-line interface for:
- Timer CRUD operations (create, read, update, delete)
- Bulk timer management and import/export
- System administration and configuration
- Development and testing utilities
- Database migration and maintenance

## Technology Stack

- **Language**: Go (Golang) 1.21+
- **Framework**: Cobra CLI framework
- **Configuration**: Viper for configuration management
- **HTTP Client**: Standard library with retry logic

## Directory Structure

```
cli/
├── cmd/                    # CLI command definitions
├── internal/               # CLI-specific internal packages
├── go.mod                  # Go module definition
├── go.sum                  # Go module checksums
└── Makefile                # Build commands
```

## Installation

### From Source
```bash
# Clone and build
git clone <repository>
cd cli
make build

# Install to GOPATH/bin
make install
```

### Using Go Install
```bash
go install github.com/indeed/durable-timer/cli@latest
```

## Quick Start

```bash
# Build the CLI
make build

# Run from build directory
./build/timer-cli --help

# Or install and run
make install
timer-cli --help
```

## Available Commands

### Timer Management
```bash
# Create a timer
timer-cli timer create --group notifications --id user-123 \
  --execute-at "2024-12-20T15:30:00Z" \
  --callback-url "https://api.example.com/webhook"

# Get timer details
timer-cli timer get --group notifications --id user-123

# Update timer
timer-cli timer update --group notifications --id user-123 \
  --execute-at "2024-12-21T15:30:00Z"

# Delete timer
timer-cli timer delete --group notifications --id user-123
```

### Bulk Operations
```bash
# Import timers from JSON file
timer-cli bulk import --file timers.json

# Export timers to JSON file
timer-cli bulk export --group notifications --file export.json

# List timers with filters
timer-cli bulk list --group notifications --limit 100
```

### System Administration
```bash
# Check server health
timer-cli admin health

# Validate configuration
timer-cli admin config validate --file config.yaml

# Database migration
timer-cli admin migrate --database postgres --up

# View server metrics
timer-cli admin metrics
```

### Development Utilities
```bash
# Test callback endpoint
timer-cli dev test-callback --url "https://api.example.com/webhook"

# Generate sample timers
timer-cli dev generate --count 1000 --group test

# Benchmark server performance
timer-cli dev benchmark --concurrent 10 --duration 60s
```

## Configuration

The CLI can be configured through:

### Configuration File
```yaml
# ~/.timer-cli.yaml
server:
  url: "https://timer-service.example.com"
  timeout: "30s"
  retries: 3

auth:
  token: "your-api-token"
  
output:
  format: "json"  # json, yaml, table
  verbose: false
```

### Environment Variables
```bash
export TIMER_SERVER_URL="https://timer-service.example.com"
export TIMER_API_TOKEN="your-api-token"
export TIMER_OUTPUT_FORMAT="json"
```

### Command-line Flags
```bash
timer-cli --server-url "https://localhost:8080" \
          --output json \
          timer get --group test --id timer-1
```

## Development Commands

```bash
make help           # Show all available commands
make build          # Build CLI binary
make test           # Run tests
make lint           # Run linter
make fmt            # Format code
make install        # Install to GOPATH/bin
```

## Examples

### Timer Lifecycle
```bash
# Create a notification timer
timer-cli timer create \
  --group notifications \
  --id "user-reminder-123" \
  --execute-at "2024-12-20T15:30:00Z" \
  --callback-url "https://api.example.com/notify" \
  --payload '{"userId": "123", "message": "Meeting reminder"}'

# Check timer status
timer-cli timer get --group notifications --id "user-reminder-123"

# Update execution time
timer-cli timer update \
  --group notifications \
  --id "user-reminder-123" \
  --execute-at "2024-12-20T16:00:00Z"

# Delete timer
timer-cli timer delete --group notifications --id "user-reminder-123"
```

### Bulk Import
```bash
# Create timers.json
echo '[
  {
    "timerId": "batch-1",
    "executeAt": "2024-12-20T15:30:00Z",
    "callbackUrl": "https://api.example.com/webhook1",
    "payload": {"type": "reminder"}
  },
  {
    "timerId": "batch-2", 
    "executeAt": "2024-12-20T16:00:00Z",
    "callbackUrl": "https://api.example.com/webhook2",
    "payload": {"type": "notification"}
  }
]' > timers.json

# Import all timers
timer-cli bulk import --group batch-job --file timers.json
```

## Output Formats

### JSON (default)
```json
{
  "timerId": "user-123",
  "groupId": "notifications",
  "executeAt": "2024-12-20T15:30:00Z",
  "callbackUrl": "https://api.example.com/webhook",
  "status": "pending"
}
```

### Table
```
TIMER ID    GROUP         EXECUTE AT           STATUS
user-123    notifications 2024-12-20T15:30:00Z pending
user-456    reminders     2024-12-20T16:00:00Z pending
```

### YAML
```yaml
timerId: user-123
groupId: notifications
executeAt: "2024-12-20T15:30:00Z"
callbackUrl: "https://api.example.com/webhook"
status: pending
``` 