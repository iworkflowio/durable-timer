# MongoDB Timer Store Implementation

This directory contains the MongoDB implementation of the `TimerStore` interface for the distributed durable timer service.

## Requirements

**IMPORTANT**: The `CreateTimer` method requires MongoDB transactions to ensure atomic shard version checking and timer insertion. This means the MongoDB deployment must be one of:
- MongoDB replica set (recommended for production)
- MongoDB sharded cluster with mongos
- MongoDB Atlas cluster

Standalone MongoDB instances are **NOT supported** for the `CreateTimer` method as they don't support transactions, which could lead to race conditions and data consistency issues.

## Files

- `mongodb_timer_store_impl.go` - MongoDB implementation of the TimerStore interface
- `mongodb_timer_store_impl_claim_test.go` - Integration tests for the ClaimShardOwnership method
- `mongodb_test_util.go` - Test utilities for setting up MongoDB test environment
- `schema/v1.js` - MongoDB schema definition with collection and index setup (JavaScript commands)

## Database Schema

The MongoDB implementation uses a unified document design where both timer records and shard ownership records are stored in the same `timers` collection, distinguished by the `row_type` field:

- `row_type = 1`: Shard ownership documents
- `row_type = 2`: Timer documents

For shard documents, the timer-specific fields (`timer_execute_at`, `timer_uuid`) use zero values since they're part of the unique index but not semantically meaningful for shard ownership.

## MongoDB-Specific Features

- **BSON Documents**: Uses MongoDB's native BSON document format
- **Compound Indexes**: Optimized indexes for execution queries and timer lookups
- **Partial Indexes**: Uses `partialFilterExpression` to create conditional indexes
- **ACID Transactions**: The `CreateTimer` method uses MongoDB transactions to atomically read shard version and insert timer records
- **Optimistic Concurrency**: Implements version-based concurrency control with atomic updates for non-transactional operations
- **MongoDB Error Handling**: Proper handling of duplicate key errors (E11000)

## CreateTimer Implementation

The `CreateTimer` method uses MongoDB transactions to ensure atomicity:

1. **Start Transaction**: Creates a MongoDB session and begins a transaction
2. **Read Shard**: Reads the shard document to get the current version within the transaction
3. **Version Check**: Compares the actual shard version with the expected version
4. **Insert Timer**: If version matches, inserts the timer document within the same transaction
5. **Commit**: Commits the transaction if all operations succeed

This approach prevents race conditions where the shard version could change between the version check and timer insertion, ensuring data consistency at the cost of requiring transaction support.

The `CreateTimerNoLock` method does not require transactions and works with standalone MongoDB instances as it performs a simple insert without version checking.

## Configuration

The MongoDB implementation requires a `config.MongoDBConnectConfig` struct with connection parameters:

```go
config := &config.MongoDBConnectConfig{
    Host:            "localhost",
    Port:            27017,
    Database:        "timer_service",
    Username:        "timer_user",
    Password:        "timer_password",
    AuthDatabase:    "timer_service",
    MaxPoolSize:     10,
    MinPoolSize:     1,
    ConnMaxLifetime: 5 * time.Minute,
    ConnMaxIdleTime: 2 * time.Minute,
}
```

## Document Structure

### Shard Documents (row_type = 1)
```bson
{
  "_id": ObjectId("..."),
  "shard_id": 1,
  "row_type": 1,
  "timer_execute_at": ISODate("1970-01-01T00:00:01Z"),  // Zero value
  "timer_uuid": "00000000-0000-0000-0000-000000000000", // Zero value
  "shard_version": 1,
  "shard_owner_id": "owner-instance-1",
  "shard_claimed_at": ISODate("2025-07-22T14:30:00Z"),
  "shard_metadata": "{\"instanceId\":\"i-123\",\"region\":\"us-west-2\"}"
}
```

### Timer Documents (row_type = 2) - (To be implemented)
```bson
{
  "_id": ObjectId("..."),
  "shard_id": 1,
  "row_type": 2,
  "timer_execute_at": ISODate("2025-07-22T15:00:00Z"),
  "timer_uuid": "550e8400-e29b-41d4-a716-446655440000",
  "timer_id": "my-timer-123",
  "timer_namespace": "my-namespace",
  "timer_callback_url": "https://api.example.com/webhook",
  "timer_payload": {...},
  "timer_retry_policy": {...},
  "timer_callback_timeout_seconds": 30
}
```

## Indexes

The schema creates two essential indexes following the design document:

1. **Execution Index**: `{shard_id: 1, row_type: 1, timer_execute_at: 1, timer_uuid: 1}` (unique) - Optimized for timer execution queries and ensures unique shard records
2. **Timer CRUD Index**: `{shard_id: 1, row_type: 1, timer_id: 1}` (unique) - For timer lookup and CRUD operations

This minimal indexing strategy provides:
- **Optimal execution performance** for the most frequent operation (finding timers to execute)
- **Unique constraint enforcement** for both shard records and timer records
- **Efficient timer CRUD** operations via the unique timer_id index  
- **Shard lookup capability** using the execution index prefix (shard_id, row_type)
- **Concurrency safety** through unique constraints preventing duplicate inserts
- **Reduced write overhead** compared to over-indexing
- **Lower storage requirements** with fewer indexes

## Testing

Run tests with:
```bash
go test ./server/databases/mongodb/...
```

Make sure MongoDB is running and accessible with the test credentials defined in `mongodb_test_util.go`.

## Development Environment

Use the Docker Compose setup in `docker/dev-mongodb.yaml` to run a local MongoDB instance:

```bash
cd docker
docker-compose -f dev-mongodb.yaml up -d
```

## Dependencies

The MongoDB implementation uses:
- `go.mongodb.org/mongo-driver` - Official MongoDB driver for Go
- Native MongoDB error handling for duplicate key detection
- BSON for document operations
- Optimistic concurrency control with atomic updates

## Implementation Notes

- **UTC Timestamps**: All timestamps are stored and handled in UTC to avoid timezone issues
- **JSON Serialization**: Metadata is serialized to JSON strings for consistent handling
- **Atomic Operations**: Uses MongoDB's atomic update operations for concurrency safety
- **Error Handling**: Properly distinguishes between duplicate key errors and other MongoDB errors
- **Connection Pooling**: Configurable connection pool settings for production use
- **Schema Consistency**: Tests use the same `v1.js` schema file as production via Docker `mongosh` execution 