# Timer Service Database Design

## Overview

This document describes the database design for the Distributed Durable Timer Service, focusing on distributed database implementations that support horizontal scaling to millions of concurrent timers. The design emphasizes partitioning strategies that enable efficient timer operations across distributed systems.

## Core Design Principles

### 1. Deterministic Partitioning
- **Partition Key**: `shardId` - computed deterministically from `groupId` and `timerId`
- **Benefits**: Enables direct shard targeting for all operations, eliminates scatter-gather queries
- **Consistency**: Same input always produces same shard placement

### 2. Single Table Design
- **Rationale**: Minimizes cross-table operations, simplifies data model, reduces operational complexity
- **Scalability**: Leverages database-native partitioning for horizontal scaling
- **Performance**: Co-locates related data for efficient queries

### 3. Time-Optimized Indexing
- **Local Index**: `executeAt` timestamp within each partition (shard)
- **Query Pattern**: Enables efficient range scans for timer execution scheduling within each shard
- **Sorting**: Natural time-based ordering for execution workflows
- **Scope**: All time-based queries are shard-specific, eliminating need for cross-shard indexes

## Partitioning Strategy

### Shard ID Calculation

```
shardId = hash(timerId) % group.numShards
```

**Implementation Steps**:
1. Client provides `groupId` and `timerId`
2. System looks up `numShards` configuration for the group
3. Hash `timerId` using consistent hash function (e.g., CRC32, SHA1)
4. Apply modulo operation: `shardId = hash(timerId) % numShards`
5. Use `shardId` as partition key for database operations

**Example**:
```
groupId: "notifications"  (configured with numShards: 1024)
timerId: "user-reminder-123"
hash("user-reminder-123") = 0x7B2C4A1E = 2067390238
shardId = 2067390238 % 1024 = 286
```

### Group Configuration

Groups support different scale requirements:

| Group Type | Use Case | numShards | Expected Load |
|------------|----------|-----------|---------------|
| small | Development/Testing | 16 | < 10K timers |
| medium | Standard Production | 256 | < 1M timers |
| large | High-Volume | 1024 | < 10M timers |
| xlarge | Massive Scale | 4096 | < 100M timers |

**Benefits**:
- **Flexibility**: Different groups can use different shard counts
- **Isolation**: Workloads don't interfere with each other
- **Scaling**: Can adjust shard count per group as needed

## Table Schema Design

### Core Timer Table

```sql
-- Conceptual schema (adapted per database)
CREATE TABLE timers (
    shard_id        INT NOT NULL,           -- Partition key
    timer_id        VARCHAR(255) NOT NULL,  -- Primary key component  
    group_id        VARCHAR(255) NOT NULL,  -- Group identifier
    execute_at      TIMESTAMP NOT NULL,     -- Execution time (indexed)
    callback_url    VARCHAR(2048) NOT NULL, -- HTTP callback URL
    payload         JSON,                   -- Custom payload data
    retry_policy    JSON,                   -- Retry configuration
    callback_timeout VARCHAR(32),           -- Timeout duration
    created_at      TIMESTAMP NOT NULL,     -- Creation timestamp
    updated_at      TIMESTAMP NOT NULL,     -- Last update timestamp
    executed_at     TIMESTAMP,              -- Execution timestamp (nullable)
    
    PRIMARY KEY (shard_id, timer_id)
) PARTITION BY HASH(shard_id);

-- Local index for time-based queries
CREATE INDEX idx_timers_execute_at ON timers (shard_id, execute_at);
```

### Field Details

| Field | Type | Purpose | Notes |
|-------|------|---------|--------|
| `shard_id` | INT | Partitioning key | Computed from groupId + timerId |
| `timer_id` | VARCHAR(255) | Timer identifier | Unique within group |
| `group_id` | VARCHAR(255) | Group identifier | For configuration lookup |
| `execute_at` | TIMESTAMP | Execution time | Indexed for range queries |
| `callback_url` | VARCHAR(2048) | HTTP endpoint | Target for timer execution |
| `payload` | JSON | Custom data | Flexible schema |
| `retry_policy` | JSON | Retry configuration | Per-timer retry settings |
| `callback_timeout` | VARCHAR(32) | Request timeout | Duration string (e.g., "30s") |
| `created_at` | TIMESTAMP | Creation time | Audit trail |
| `updated_at` | TIMESTAMP | Last modification | Optimistic locking |
| `executed_at` | TIMESTAMP | Execution time | Nullable, set when executed |

## Index Strategy: Local vs Global

### Why Local Indexes Are Sufficient

**No Cross-Shard Queries Needed**:
- All CRUD operations know both `groupId` and `timerId`, allowing direct shard targeting
- Timer execution processes each shard independently in parallel
- No use cases require querying across all shards simultaneously

**Query Patterns**:
```sql
-- All queries are shard-specific:
WHERE shard_id = ? AND timer_id = ?           -- Direct lookup
WHERE shard_id = ? AND execute_at <= ?        -- Time-based execution query
```

**Performance Benefits**:
- **O(1) Operations**: Direct shard targeting eliminates scatter-gather queries
- **Parallel Processing**: Independent shard processing scales linearly
- **Lower Latency**: Single-shard operations avoid cross-shard coordination
- **Simplified Consistency**: No cross-shard consistency concerns

**Database Implementation Notes**:
- **Cassandra, TiDB**: Native local indexes within partitions
- **MongoDB**: Compound indexes that include shardId effectively create local behavior
- **DynamoDB**: GSI with shardId as partition key acts as local index (avoids LSI 10GB limit)

## Database-Specific Implementations

### Apache Cassandra

**Table Definition**:
```cql
CREATE TABLE timers (
    shard_id int,
    execute_at timestamp,
    timer_id text,
    group_id text,
    callback_url text,
    payload text,            -- JSON serialized
    retry_policy text,       -- JSON serialized
    callback_timeout text,
    created_at timestamp,
    updated_at timestamp,
    executed_at timestamp,
    PRIMARY KEY (shard_id, execute_at, timer_id)
) WITH CLUSTERING ORDER BY (execute_at ASC, timer_id ASC);

-- Secondary index for timer CRUD operations
CREATE INDEX idx_timer_id ON timers (shard_id, timer_id);
```

**Key Features**:
- **Partitioning**: Native support via `shard_id` partition key
- **Time-Optimized Clustering**: `execute_at` as primary clustering column for high-frequency execution queries
- **Uniqueness**: `timer_id` in clustering key ensures uniqueness when timers have same execute_at
- **Secondary Index**: Local index on `(shard_id, timer_id)` for CRUD operations
- **Consistency**: Tunable consistency levels (QUORUM recommended)
- **Scaling**: Automatic data distribution across nodes

**Query Patterns**:
```cql
-- Timer execution query (optimized - leverages clustering order)
-- HIGH FREQUENCY: Executed every few seconds per shard
SELECT * FROM timers 
WHERE shard_id = ? AND execute_at <= ?
ORDER BY execute_at ASC;

-- Direct timer lookup (uses secondary index)  
-- LOWER FREQUENCY: User-driven CRUD operations
SELECT * FROM timers 
WHERE shard_id = ? AND timer_id = ?;
```

**Design Rationale**:
- **Primary Optimization**: Timer execution is the core high-frequency operation of the service
- **Query Frequency**: Execution scans happen continuously (every few seconds), CRUD operations are occasional
- **Storage Order**: Physical storage sorted by execute_at enables extremely fast range scans
- **Trade-off**: Accept secondary index cost for less frequent CRUD operations to optimize core execution performance
- **Performance**: Execution queries leverage clustering for O(log n) performance, CRUD uses local secondary index

### MongoDB

**Collection Structure**:
```javascript
// Collection: timers
{
  _id: ObjectId(),  // MongoDB default
  shardId: NumberInt(286),
  timerId: "user-reminder-123", 
  groupId: "notifications",
  executeAt: ISODate("2024-12-20T15:30:00Z"),
  callbackUrl: "https://api.example.com/webhook",
  payload: {
    userId: "user123",
    action: "send_reminder"
  },
  retryPolicy: {
    maxRetries: 3,
    initialInterval: "30s"
  },
  callbackTimeout: "30s",
  createdAt: ISODate("2024-12-19T10:00:00Z"),
  updatedAt: ISODate("2024-12-19T10:00:00Z"),
  executedAt: null
}
```

**Sharding Configuration**:
```javascript
// Enable sharding on database
sh.enableSharding("timerservice")

// Shard collection on shardId
sh.shardCollection("timerservice.timers", {shardId: 1})

// Compound index for queries
db.timers.createIndex({shardId: 1, timerId: 1}, {unique: true})
db.timers.createIndex({shardId: 1, executeAt: 1})
```

**Key Features**:
- **Native Sharding**: Built-in shard key support
- **Flexible Schema**: JSON-native storage for payload and retry policy
- **Rich Indexing**: Compound indexes for efficient queries
- **Horizontal Scaling**: Automatic balancing across shards

### TiDB (MySQL Compatible)

**Table Definition**:
```sql
CREATE TABLE timers (
    shard_id INT NOT NULL,
    timer_id VARCHAR(255) NOT NULL,
    group_id VARCHAR(255) NOT NULL,
    execute_at TIMESTAMP(3) NOT NULL,
    callback_url VARCHAR(2048) NOT NULL,
    payload JSON,
    retry_policy JSON,
    callback_timeout VARCHAR(32) DEFAULT '30s',
    created_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    executed_at TIMESTAMP(3) NULL,
    PRIMARY KEY (shard_id, timer_id),
    KEY idx_execute_at (shard_id, execute_at)
) PARTITION BY HASH(shard_id) PARTITIONS 1024;
```

**Key Features**:
- **MySQL Compatibility**: Standard SQL with MySQL extensions
- **Native Partitioning**: HASH partitioning on shard_id
- **JSON Support**: Native JSON data type for flexible fields
- **HTAP**: Hybrid transactional and analytical processing

### DynamoDB

**Table Structure**:
```json
{
  "TableName": "timers",
  "KeySchema": [
    {
      "AttributeName": "shardId",
      "KeyType": "HASH"
    },
    {
      "AttributeName": "timerId", 
      "KeyType": "RANGE"
    }
  ],
  "AttributeDefinitions": [
    {
      "AttributeName": "shardId",
      "AttributeType": "N"
    },
    {
      "AttributeName": "timerId",
      "AttributeType": "S"
    },
    {
      "AttributeName": "executeAt",
      "AttributeType": "S"
    }
  ],
  "GlobalSecondaryIndexes": [
    {
      "IndexName": "executeAt-index",
      "KeySchema": [
        {
          "AttributeName": "shardId",
          "KeyType": "HASH"
        },
        {
          "AttributeName": "executeAt",
          "KeyType": "RANGE"
        }
      ]
    }
  ]
}
```

**Item Structure**:
```json
{
  "shardId": {"N": "286"},
  "timerId": {"S": "user-reminder-123"},
  "groupId": {"S": "notifications"},
  "executeAt": {"S": "2024-12-20T15:30:00Z"},
  "callbackUrl": {"S": "https://api.example.com/webhook"},
  "payload": {"S": "{\"userId\":\"user123\"}"},
  "retryPolicy": {"S": "{\"maxRetries\":3}"},
  "callbackTimeout": {"S": "30s"},
  "createdAt": {"S": "2024-12-19T10:00:00Z"},
  "updatedAt": {"S": "2024-12-19T10:00:00Z"},
  "executedAt": {"NULL": true}
}
```

**Key Features**:
- **Managed Service**: Fully managed, serverless scaling
- **Partition Key**: Natural sharding via shardId hash key
- **GSI**: Global Secondary Index for time-based queries (effectively "local" since partitioned by shardId)
- **Consistent Hashing**: Built-in data distribution

**Note on GSI vs LSI**: We use GSI instead of LSI because:
- LSI has 10GB limit per partition (could be exceeded by large shards)
- GSI with shardId as partition key behaves like local index for our use case
- GSI provides unlimited storage per partition

## Query Patterns and Performance

### Primary Operations

#### 1. Timer Creation
```sql
-- All databases: Insert with computed shard_id
INSERT INTO timers (shard_id, timer_id, group_id, execute_at, ...)
VALUES (?, ?, ?, ?, ...)
```
- **Performance**: Single partition write, O(1) complexity
- **Scaling**: Distributes across shards automatically

#### 2. Timer Retrieval
```sql
-- Direct lookup by composite key
SELECT * FROM timers 
WHERE shard_id = ? AND timer_id = ?
```
- **Performance**: Single partition read, O(1) complexity
- **Efficiency**: No cross-partition queries needed

#### 3. Timer Update
```sql
-- Update within single partition
UPDATE timers 
SET execute_at = ?, payload = ?, updated_at = ?
WHERE shard_id = ? AND timer_id = ?
```
- **Performance**: Single partition write, O(1) complexity
- **Consistency**: Strong consistency within partition

#### 4. Timer Deletion
```sql
-- Delete from single partition
DELETE FROM timers 
WHERE shard_id = ? AND timer_id = ?
```
- **Performance**: Single partition write, O(1) complexity
- **Cleanup**: Immediate space reclamation (database dependent)

### Timer Execution Queries

#### 5. Find Due Timers
```sql
-- Query per shard for execution
SELECT timer_id, callback_url, payload, retry_policy
FROM timers 
WHERE shard_id = ? AND execute_at <= ?
ORDER BY execute_at ASC
LIMIT 1000
```
- **Performance**: Single partition scan with index
- **Parallelization**: Can query multiple shards concurrently
- **Efficiency**: Index on execute_at enables fast range scans

## Scaling and Performance Considerations

### Shard Distribution
- **Even Distribution**: Hash function ensures uniform shard assignment
- **Hot Spot Avoidance**: Timer IDs spread across all shards
- **Predictable Performance**: Consistent load per shard

### Execution Scheduling
```
For each active shard S in parallel:
  query timers WHERE shard_id = S AND execute_at <= NOW()
  process timers in executeAt order
```

**Benefits**:
- **Parallelism**: Independent shard processing
- **Scalability**: Adding shards increases throughput
- **Fault Tolerance**: Shard failures don't affect others

### Database-Specific Optimizations

#### Cassandra
- **Compaction Strategy**: Time Window Compaction for time-series data
- **Consistency Level**: QUORUM for read/write balance
- **Token Aware**: Use token-aware driver for direct shard access

#### MongoDB
- **Read Preference**: Primary preferred for consistency
- **Write Concern**: Majority for durability
- **Chunk Size**: Adjust for timer distribution patterns

#### TiDB
- **Placement Rules**: Pin frequently accessed shards to fast storage
- **Follower Read**: Use follower reads for query scalability
- **Auto Split**: Enable automatic region splitting

#### DynamoDB
- **Provisioned Capacity**: Set per-shard capacity limits
- **Adaptive Capacity**: Enable for handling hot partitions
- **DAX**: Use DynamoDB Accelerator for read-heavy workloads

## Operational Considerations

### Monitoring and Metrics
- **Shard Balance**: Monitor timer distribution across shards
- **Query Performance**: Track per-shard query latencies
- **Hot Spot Detection**: Identify overloaded shards

### Backup and Recovery
- **Per-Shard Backup**: Leverage partitioning for parallel backups
- **Point-in-Time Recovery**: Maintain consistent state across shards
- **Cross-Region Replication**: Distribute shards across regions

### Schema Migration
- **Backward Compatibility**: Maintain existing partitioning scheme
- **Rolling Updates**: Update schema without downtime
- **Data Migration**: Move data between shards if rebalancing needed

---

*This design provides a foundation for horizontal scaling while maintaining strong consistency and performance across all supported distributed databases.* 