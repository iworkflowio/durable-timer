# DynamoDB Timer Store Implementation

This directory contains the DynamoDB implementation of the `TimerStore` interface for the distributed durable timer service.

## Files

- `dynamodb_timer_store_impl.go` - DynamoDB implementation of the TimerStore interface
- `dynamodb_timer_store_impl_claim_test.go` - Integration tests for the ClaimShardOwnership method
- `dynamodb_test_util.go` - Test utilities for setting up DynamoDB test environment
- `schema/v1.json` - DynamoDB table definition in JSON format for AWS CLI

## Database Schema

The DynamoDB implementation uses a unified table design where both timer records and shard ownership records are stored in the same `timers` table, distinguished by different sort keys:

- **Shard records**: `sort_key = "SHARD"` (with zero values for timer-specific fields)
- **Timer records**: `sort_key = "TIMER#{timer_id}"` (for actual timer data)

The table design follows the cost-optimized approach from the design document, using Local Secondary Index (LSI) instead of Global Secondary Index (GSI) to minimize costs.

## DynamoDB-Specific Features

- **NoSQL Document Storage**: Uses DynamoDB's native attribute-value pairs
- **Composite Keys**: Partition key `shard_id` + sort key `sort_key` for efficient access patterns
- **Local Secondary Index**: `ExecuteAtIndex` on `(shard_id, timer_execute_at)` for execution queries
- **Conditional Operations**: Uses `ConditionExpression` for optimistic concurrency control
- **Attribute Expressions**: Uses `UpdateExpression` and `ExpressionAttributeValues` for atomic updates
- **DynamoDB-Specific Error Handling**: Proper handling of `ConditionalCheckFailedException`

## Configuration

The DynamoDB implementation requires a `config.DynamoDBConnectConfig` struct with connection parameters:

```go
config := &config.DynamoDBConnectConfig{
    Region:          "us-east-1",
    EndpointURL:     "http://localhost:8000", // For DynamoDB Local
    AccessKeyID:     "dummy",                 // For DynamoDB Local
    SecretAccessKey: "dummy",                 // For DynamoDB Local
    TableName:       "timers",
    MaxRetries:      3,
    Timeout:         10 * time.Second,
}
```

## Item Structure

### Shard Records
```json
{
  "shard_id": {"N": "1"},
  "sort_key": {"S": "SHARD"},
  "row_type": {"N": "1"},
  "timer_execute_at": {"S": "1970-01-01T00:00:01Z"},
  "timer_uuid": {"S": "00000000-0000-0000-0000-000000000000"},
  "shard_version": {"N": "1"},
  "shard_owner_id": {"S": "owner-instance-1"},
  "shard_claimed_at": {"S": "2025-07-22T14:30:00Z"},
  "shard_metadata": {"S": "{\"instanceId\":\"i-123\",\"region\":\"us-west-2\"}"}
}
```

### Timer Records (To be implemented)
```json
{
  "shard_id": {"N": "1"},
  "sort_key": {"S": "TIMER#my-timer-123"},
  "row_type": {"N": "2"},
  "timer_execute_at": {"S": "2025-07-22T15:00:00Z"},
  "timer_uuid": {"S": "550e8400-e29b-41d4-a716-446655440000"},
  "timer_id": {"S": "my-timer-123"},
  "timer_group_id": {"S": "my-group"},
  "timer_callback_url": {"S": "https://api.example.com/webhook"},
  "timer_payload": {"S": "{...}"},
  "timer_retry_policy": {"S": "{...}"},
  "timer_callback_timeout_seconds": {"N": "30"}
}
```

## Access Patterns

The table design supports these efficient access patterns:

1. **Shard Operations**: Query by `(shard_id, "SHARD")` - O(1) lookup
2. **Timer CRUD**: Query by `(shard_id, "TIMER#{timer_id}")` - O(1) lookup  
3. **Timer Execution**: Query ExecuteAtIndex by `(shard_id, timer_execute_at range)` - Efficient range queries
4. **Shard Management**: Direct access to shard ownership records

## Testing

Run tests with:
```bash
go test ./server/databases/dynamodb/...
```

Make sure DynamoDB Local is running and accessible with the test credentials defined in `dynamodb_test_util.go`.

## Development Environment

Use the Docker Compose setup in `docker/dev-dynamodb.yaml` to run a local DynamoDB instance:

```bash
cd docker
docker-compose -f dev-dynamodb.yaml up -d
```

This starts:
- DynamoDB Local on port 8000
- Automatic table creation using the `v1.json` schema

## Dependencies

The DynamoDB implementation uses:
- `github.com/aws/aws-sdk-go-v2` - Official AWS SDK for Go v2
- DynamoDB-specific error handling for conditional operations
- AWS credential providers for both local and production use
- Native DynamoDB attribute value handling

## Implementation Notes

- **UTC Timestamps**: All timestamps are stored and handled in UTC as ISO 8601 strings
- **JSON Serialization**: Metadata is serialized to JSON strings for consistent handling
- **Atomic Operations**: Uses DynamoDB's conditional expressions for concurrency safety
- **Error Handling**: Properly distinguishes between conditional check failures and other DynamoDB errors
- **Cost Optimization**: Uses LSI instead of GSI for execution queries to reduce costs
- **Schema Consistency**: Tests use the same `v1.json` schema file as production via AWS CLI execution

## Cost Considerations

This implementation follows the design document's cost-optimized approach:
- **LSI over GSI**: Uses Local Secondary Index to avoid additional partition throughput costs
- **Single Table**: Unified design reduces table management overhead
- **Pay-per-request**: Schema uses on-demand billing for predictable costs
- **Efficient Queries**: Access patterns minimize read/write unit consumption 