# PostgreSQL Timer Store Implementation

This directory contains the PostgreSQL implementation of the `TimerStore` interface for the distributed durable timer service.

## Files

- `postgresql_timer_store_impl.go` - PostgreSQL implementation of the TimerStore interface
- `postgresql_timer_store_impl_claim_test.go` - Integration tests for the ClaimShardOwnership method
- `postgresql_test_util.go` - Test utilities for setting up PostgreSQL test environment
- `schema/v1.sql` - PostgreSQL schema definition for the unified timers table

## Database Schema

The PostgreSQL implementation uses a unified table design where both timer records and shard ownership records are stored in the same `timers` table, distinguished by the `row_type` field:

- `row_type = 1`: Shard ownership records
- `row_type = 2`: Timer records

For shard records, the timer-specific fields (`timer_execute_at`, `timer_uuid`) use zero values since they're part of the primary key but not semantically meaningful for shard ownership.

## PostgreSQL-Specific Features

- **UUID Type**: Uses PostgreSQL's native UUID type for `timer_uuid` field
- **JSONB**: Uses JSONB for JSON fields (better performance than JSON)
- **Parametrized Queries**: Uses `$1, $2, ...` parameter placeholders

> **Note**: Hash partitioning is not included in the development schema for simplicity. In production, you may want to add `PARTITION BY HASH (shard_id)` and create appropriate partitions for better performance with large datasets.

## Configuration

The PostgreSQL implementation requires a `config.PostgreSQLConnectConfig` struct with connection parameters:

```go
config := &config.PostgreSQLConnectConfig{
    Host:            "localhost",
    Port:            5432,
    Database:        "timer_service",
    Username:        "timer_user", 
    Password:        "timer_password",
    SSLMode:         "disable",
    MaxOpenConns:    10,
    MaxIdleConns:    5,
    ConnMaxLifetime: 5 * time.Minute,
}
```

## Testing

Run tests with:
```bash
go test ./server/databases/postgresql/...
```

Make sure PostgreSQL is running and accessible with the test credentials defined in `postgresql_test_util.go`.

## Development Environment

Use the Docker Compose setup in `docker/dev-postgresql.yaml` to run a local PostgreSQL instance:

```bash
cd docker
docker-compose -f dev-postgresql.yaml up -d
```

## Dependencies

The PostgreSQL implementation uses:
- `github.com/lib/pq` - PostgreSQL driver for Go
- Native PostgreSQL error handling for duplicate key detection 