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

### General Idea For All Databases
* Use a single table design.
* Use shardId as partition key (if supporting partitioning)
* Under a partition, use `execute_at + uuid` primary abd clustering key
* Under a partition, use `timer_id` as unique index. 

```sql
-- Universal schema design (consistent across all databases)
CREATE TABLE timers (
    shard_id        INT NOT NULL,           -- Partition key
    execute_at      TIMESTAMP NOT NULL,     -- Primary clustering key for execution queries
    timer_uuid      UUID NOT NULL,          -- UUID for uniqueness
    timer_id        VARCHAR(255) NOT NULL,  -- Business key (unique index)
    group_id        VARCHAR(255) NOT NULL,  -- Group identifier
    callback_url    VARCHAR(2048) NOT NULL, -- HTTP callback URL
    payload         JSON,                   -- Custom payload data
    retry_policy    JSON,                   -- Retry configuration
    callback_timeout VARCHAR(32),           -- Timeout duration
    created_at      TIMESTAMP NOT NULL,     -- Creation timestamp
    updated_at      TIMESTAMP NOT NULL,     -- Last update timestamp
    
    PRIMARY KEY   (shard_id, execute_at, timer_uuid) and CLUSTERING
) PARTITION BY HASH(shard_id);

-- Unique index for timer_id lookups and CRUD operations
CREATE UNIQUE INDEX idx_timer_id ON timers (shard_id, timer_id);
```
 
 This provides:

- **Optimal Performance**: `execute_at` first for fast timer execution queries
- **UUID for Uniqueness**: Eliminates complex uniqueness handling
- **timer_id Index**: Efficient CRUD operations via unique index
- **Consistent Design**: Same primary key strategy across all database types

### Field Details

| Field | Type | Purpose | Notes |
|-------|------|---------|--------|
| `shard_id` | INT | Partitioning key | Computed from groupId + timerId |
| `execute_at` | TIMESTAMP | Execution time | Primary + Clustering key. Indexed for range queries |
| `timer_uuid` | UUID | Uniqueness key | Primary key. Auto-generated UUID |
| `timer_id` | VARCHAR(255) | Timer identifier | Unique within group |
| `group_id` | VARCHAR(255) | Group identifier | For configuration lookup |
| `callback_url` | VARCHAR(2048) | HTTP endpoint | Target for timer execution |
| `payload` | JSON | Custom data | Flexible schema |
| `retry_policy` | JSON | Retry configuration | Per-timer retry settings |
| `callback_timeout` | VARCHAR(32) | Request timeout | Duration string (e.g., "30s") |
| `created_at` | TIMESTAMP | Creation time | Audit trail |
| `updated_at` | TIMESTAMP | Last modification | Optimistic locking |

## Design Trade-offs

### Group-Based Partitioning Trade-offs

**Cons: API Complexity**
- **Exposed Abstraction**: Users must understand and provide `groupId` for timer operations
- **API Surface**: Get, update, and delete operations require both `groupId` and `timerId` parameters
- **Learning Curve**: Additional concept for users to understand beyond simple timer IDs

**Pros: Operational Simplicity**  
- **No Complex Resharding**: Avoids building sophisticated data migration mechanisms when changing `numShards` configuration
- **Predictable Performance**: Shard assignment is deterministic and doesn't require cluster-wide coordination
- **Simpler Implementation**: No need for complex resharding algorithms, data movement, or consistency guarantees during resharding operations

**Impact Assessment**
- **Limited API Impact**: Only 3 of 4 API endpoints require `groupId` (create timer uses it implicitly for shard assignment)
- **Better Than Alternatives**: Exposing `groupId` is preferable to exposing raw `shardId` or requiring complex client-side shard computation
- **Operational Benefits**: The operational simplicity of avoiding resharding outweighs the minor API complexity increase
- **Future Flexibility**: Groups can be pre-allocated with appropriate shard counts based on expected load, reducing the need for runtime resharding

**Similarity to NoSQL Database Patterns**
This trade-off mirrors common patterns in distributed NoSQL databases:
- **Cassandra**: Exposes partition keys to users for predictable performance and data locality
- **DynamoDB**: Requires users to design partition keys and understand hot partitions for optimal scaling
- **MongoDB Sharding**: Users must choose shard keys and understand their impact on query routing
- **HBase**: Row key design directly affects data distribution and query performance

Like these systems, we expose partitioning concepts (`groupId`) to users in exchange for:
- Predictable, scalable performance
- Elimination of complex auto-resharding mechanisms
- Simplified operational model
- Direct user control over data distribution

**Conclusion**: The design trades minor API complexity for significant operational simplicity. Users learn one additional concept (`groupId`) but gain predictable performance and avoid the complexity and risks associated with online resharding operations. This follows established patterns in distributed systems where exposing partitioning concepts to users is a proven approach for achieving scale.

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
- **DynamoDB**: LSI with shardId partition key provides cost-effective local indexes (10GB limit managed via shard allocation)

## Primary Key Size Optimization

### Database-Specific Primary Key Strategies

**Databases Supporting Non-Unique Primary Keys** (can optimize for smaller primary keys):
- **MongoDB**: Can use non-unique compound indexes with separate unique constraints
- **Some NoSQL databases**: May support similar patterns

**Databases Requiring Unique Primary Keys** (must include timer_id for uniqueness):
- **Cassandra**: PRIMARY KEY must be unique
- **TiDB/MySQL**: PRIMARY KEY must be unique  
- **DynamoDB**: Primary key combination must be unique

### Optimization Strategy

For databases supporting non-unique primary keys:
```
Primary Index:    (shard_id, execute_at)           -- Smaller, optimized for queries
Unique Index:     (shard_id, timer_id) UNIQUE      -- Separate uniqueness constraint
```

For databases requiring unique primary keys:
```
Primary Key:      (shard_id, execute_at, timer_id) -- Must include timer_id for uniqueness
Secondary Index:  (shard_id, timer_id)             -- For CRUD operations
```

### Benefits of Smaller Primary Keys

✅ **Reduced Index Size**: Smaller keys = smaller indexes = better memory usage  
✅ **Faster Queries**: Less data to scan and compare  
✅ **Lower Storage Overhead**: Primary key often included in secondary indexes  
✅ **Better Cache Performance**: More index entries fit in memory  
✅ **Reduced Network Transfer**: Smaller keys transferred during operations  

## Database-Specific Implementations

### Apache Cassandra

**Table Definition**:
```cql
CREATE TABLE timers (
    shard_id int,
    execute_at timestamp,
    timer_uuid uuid,
    timer_id text,
    group_id text,
    callback_url text,
    payload text,            -- JSON serialized
    retry_policy text,       -- JSON serialized
    callback_timeout text,
    created_at timestamp,
    updated_at timestamp,
    PRIMARY KEY (shard_id, execute_at, timer_uuid)
) WITH CLUSTERING ORDER BY (execute_at ASC, timer_uuid ASC);

-- Unique index for timer_id lookups and CRUD operations
CREATE INDEX idx_timer_id ON timers (shard_id, timer_id);
```



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



### MongoDB

**Collection Structure**:
```javascript
// Collection: timers
{
  _id: ObjectId(),  // MongoDB default
  shardId: NumberInt(286),
  executeAt: ISODate("2024-12-20T15:30:00Z"),
  timerUuid: UUID("550e8400-e29b-41d4-a716-446655440000"),
  timerId: "user-reminder-123", 
  groupId: "notifications",
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
  updatedAt: ISODate("2024-12-19T10:00:00Z")
}
```

**Sharding Configuration**:
```javascript
// Enable sharding on database
sh.enableSharding("timerservice")

// Shard collection on shardId
sh.shardCollection("timerservice.timers", {shardId: 1})

// Primary compound index for timer execution queries
db.timers.createIndex({shardId: 1, executeAt: 1, timerUuid: 1})

// Unique index for timer_id CRUD operations  
db.timers.createIndex({shardId: 1, timerId: 1}, {unique: true})
```



**Query Patterns**:
```javascript
// Timer execution query (optimized - uses primary index)
// HIGH FREQUENCY: Executed every few seconds per shard
db.timers.find({
  shardId: 286,
  executeAt: {$lte: new Date()}
}).sort({executeAt: 1})

// Direct timer lookup (uses secondary index)
// LOWER FREQUENCY: User-driven CRUD operations  
db.timers.findOne({
  shardId: 286,
  timerId: "user-reminder-123"
})
```

### TiDB (MySQL Compatible)

**Table Definition**:
```sql
CREATE TABLE timers (
    shard_id INT NOT NULL,
    execute_at TIMESTAMP(3) NOT NULL,
    timer_uuid CHAR(36) NOT NULL,      -- UUID as string
    timer_id VARCHAR(255) NOT NULL,
    group_id VARCHAR(255) NOT NULL,
    callback_url VARCHAR(2048) NOT NULL,
    payload JSON,
    retry_policy JSON,
    callback_timeout VARCHAR(32) DEFAULT '30s',
    created_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (shard_id, execute_at, timer_uuid),
    UNIQUE KEY idx_timer_id (shard_id, timer_id)  -- Unique index for CRUD operations
) PARTITION BY HASH(shard_id) PARTITIONS 1024;
```


**Query Patterns**:
```sql
-- Timer execution query (optimized - uses primary key order)
-- HIGH FREQUENCY: Executed every few seconds per shard
SELECT timer_id, callback_url, payload, retry_policy
FROM timers 
WHERE shard_id = ? AND execute_at <= NOW()
ORDER BY execute_at ASC, timer_id ASC
LIMIT 1000;

-- Direct timer lookup (uses secondary index)
-- LOWER FREQUENCY: User-driven CRUD operations
SELECT * FROM timers 
WHERE shard_id = ? AND timer_id = ?;
```

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
      "AttributeName": "executeAt_timerUuid", 
      "KeyType": "RANGE"
    }
  ],
  "AttributeDefinitions": [
    {
      "AttributeName": "shardId",
      "AttributeType": "N"
    },
    {
      "AttributeName": "executeAt_timerUuid",
      "AttributeType": "S"
    },
    {
      "AttributeName": "timerId",
      "AttributeType": "S"
    }
  ],
  "LocalSecondaryIndexes": [
    {
      "IndexName": "timerId-index",
      "KeySchema": [
        {
          "AttributeName": "shardId",
          "KeyType": "HASH"
        },
        {
          "AttributeName": "timerId",
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
  "executeAt_timerUuid": {"S": "2024-12-20T15:30:00Z#550e8400-e29b-41d4-a716-446655440000"},
  "executeAt": {"S": "2024-12-20T15:30:00Z"},
  "timerUuid": {"S": "550e8400-e29b-41d4-a716-446655440000"},
  "timerId": {"S": "user-reminder-123"},
  "groupId": {"S": "notifications"},
  "callbackUrl": {"S": "https://api.example.com/webhook"},
  "payload": {"S": "{\"userId\":\"user123\"}"},
  "retryPolicy": {"S": "{\"maxRetries\":3}"},
  "callbackTimeout": {"S": "30s"},
  "createdAt": {"S": "2024-12-19T10:00:00Z"},
  "updatedAt": {"S": "2024-12-19T10:00:00Z"}
}
```

**Key Features**:
- **Consistent Design**: Same logical primary key pattern as all databases
- **Managed Service**: Fully managed, serverless scaling
- **Composite Range Key**: `executeAt#timerUuid` for uniqueness and ordering
- **LSI for CRUD**: Local Secondary Index on timerId for timer CRUD operations
- **Consistent Hashing**: Built-in data distribution

**Note on Uniqueness**: Since multiple timers can have the same executeAt, we include timerId in the item structure to maintain uniqueness. DynamoDB will handle items with identical primary keys by overwriting, so application logic must ensure executeAt + timerId combinations are unique.

**Query Patterns**:
```javascript
// Timer execution query (optimized - uses primary key range)
// HIGH FREQUENCY: Executed every few seconds per shard
{
  TableName: "timers",
  KeyConditionExpression: "shardId = :shardId AND executeAt <= :now",
  ExpressionAttributeValues: {
    ":shardId": {"N": "286"},
    ":now": {"S": "2024-12-20T15:30:00Z"}
  }
}

// Direct timer lookup (uses LSI)
// LOWER FREQUENCY: User-driven CRUD operations
{
  TableName: "timers", 
  IndexName: "timerId-index",
  KeyConditionExpression: "shardId = :shardId AND timerId = :timerId",
  ExpressionAttributeValues: {
    ":shardId": {"N": "286"},
    ":timerId": {"S": "user-reminder-123"}
  }
}
```

**Note on LSI vs GSI Cost in DynamoDB**:

- **LSI (Local Secondary Index)**: Storage for LSI is billed at the same rate as the base table, and write costs are included in the base table's write capacity. LSI has a strict 10GB storage limit per partition, but this is manageable through proper shard management. LSI queries are always strongly consistent and operate within the same partition as the base table.
- **GSI (Global Secondary Index)**: GSI storage is billed separately from the base table, and you pay for both read and write capacity (or on-demand) on the GSI in addition to the base table. GSI does **not** have the 10GB per-partition limit—storage is effectively unlimited and scales independently.
- **Cost Comparison**: LSI is significantly more cost-effective for write-heavy workloads since writes don't consume additional capacity. GSI incurs additional costs for every write operation that affects the index.
- **Design Choice**: We use LSI with `shardId` as partition key to minimize costs. The 10GB partition limit is managed through administrative controls - when a partition approaches the limit, system administrators can create new groups with higher shard counts to redistribute the load. For customers requiring unlimited partition storage, GSI support can be offered as a premium feature with higher DynamoDB costs.

## Traditional SQL Databases

While the distributed databases above provide native sharding and horizontal scaling, traditional SQL databases can also support the timer service with appropriate configuration and potentially external sharding mechanisms.

### MySQL

**Table Definition**:
```sql
CREATE TABLE timers (
    shard_id INT NOT NULL,
    execute_at TIMESTAMP(3) NOT NULL,
    timer_uuid CHAR(36) NOT NULL,      -- UUID as string
    timer_id VARCHAR(255) NOT NULL,
    group_id VARCHAR(255) NOT NULL,
    callback_url VARCHAR(2048) NOT NULL,
    payload JSON,
    retry_policy JSON,
    callback_timeout VARCHAR(32) DEFAULT '30s',
    created_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (shard_id, execute_at, timer_uuid),
    UNIQUE INDEX idx_timer_lookup (shard_id, timer_id)
) PARTITION BY HASH(shard_id) PARTITIONS 32;
```


**Query Patterns**:
```sql
-- Timer execution query (optimized - uses primary key clustering)
-- HIGH FREQUENCY: Executed every few seconds per shard
SELECT timer_id, callback_url, payload, retry_policy
FROM timers 
WHERE shard_id = 286 AND execute_at <= NOW(3)
ORDER BY execute_at ASC
LIMIT 1000;

-- Direct timer lookup (uses secondary index)
-- LOWER FREQUENCY: User-driven CRUD operations
SELECT * FROM timers 
WHERE shard_id = 286 AND timer_id = 'user-reminder-123';
```

### PostgreSQL

**Table Definition**:
```sql
CREATE TABLE timers (
    shard_id INTEGER NOT NULL,
    execute_at TIMESTAMP(3) NOT NULL,
    timer_uuid UUID NOT NULL,
    timer_id VARCHAR(255) NOT NULL,
    group_id VARCHAR(255) NOT NULL,
    callback_url VARCHAR(2048) NOT NULL,
    payload JSONB,
    retry_policy JSONB,
    callback_timeout VARCHAR(32) DEFAULT '30s',
    created_at TIMESTAMP(3) NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP(3) NOT NULL DEFAULT NOW(),
    PRIMARY KEY (shard_id, execute_at, timer_uuid)
) PARTITION BY HASH (shard_id);

-- Create partitions (example for 32 partitions)
-- CREATE TABLE timers_p0 PARTITION OF timers FOR VALUES WITH (modulus 32, remainder 0);
-- ... repeat for p1 through p31

-- Unique index for timer lookups
CREATE UNIQUE INDEX idx_timer_lookup ON timers (shard_id, timer_id);


```


**Query Patterns**:
```sql
-- Timer execution query (partition-aware)
-- HIGH FREQUENCY: Executed every few seconds per shard
SELECT timer_id, callback_url, payload, retry_policy
FROM timers 
WHERE shard_id = 286 AND execute_at <= NOW()
ORDER BY execute_at ASC
LIMIT 1000;

-- Direct timer lookup (uses index)
-- LOWER FREQUENCY: User-driven CRUD operations
SELECT * FROM timers 
WHERE shard_id = 286 AND timer_id = 'user-reminder-123';
```

### Oracle Database

**Table Definition**:
```sql
CREATE TABLE timers (
    shard_id NUMBER(10) NOT NULL,
    execute_at TIMESTAMP(3) NOT NULL,
    timer_uuid RAW(16) NOT NULL,       -- UUID as binary
    timer_id VARCHAR2(255) NOT NULL,
    group_id VARCHAR2(255) NOT NULL,
    callback_url VARCHAR2(2048) NOT NULL,
    payload CLOB CHECK (payload IS JSON),
    retry_policy CLOB CHECK (retry_policy IS JSON),
    callback_timeout VARCHAR2(32) DEFAULT '30s',
    created_at TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (shard_id, execute_at, timer_uuid)
) 
PARTITION BY HASH (shard_id) PARTITIONS 32;

-- Unique index for timer lookups
CREATE UNIQUE INDEX idx_timer_lookup ON timers (shard_id, timer_id);


```


**Query Patterns**:
```sql
-- Timer execution query (partition pruning)
-- HIGH FREQUENCY: Executed every few seconds per shard
SELECT timer_id, callback_url, payload, retry_policy
FROM timers 
WHERE shard_id = 286 AND execute_at <= CURRENT_TIMESTAMP
ORDER BY execute_at ASC
FETCH FIRST 1000 ROWS ONLY;

-- Direct timer lookup (partition + index access)
-- LOWER FREQUENCY: User-driven CRUD operations
SELECT * FROM timers 
WHERE shard_id = 286 AND timer_id = 'user-reminder-123';
```

### Microsoft SQL Server

**Table Definition**:
```sql
CREATE TABLE timers (
    shard_id INT NOT NULL,
    execute_at DATETIME2(3) NOT NULL,
    timer_uuid UNIQUEIDENTIFIER NOT NULL,
    timer_id NVARCHAR(255) NOT NULL,
    group_id NVARCHAR(255) NOT NULL,
    callback_url NVARCHAR(2048) NOT NULL,
    payload NVARCHAR(MAX) CHECK (ISJSON(payload) = 1),
    retry_policy NVARCHAR(MAX) CHECK (ISJSON(retry_policy) = 1),
    callback_timeout NVARCHAR(32) DEFAULT '30s',
    created_at DATETIME2(3) NOT NULL DEFAULT GETUTCDATE(),
    updated_at DATETIME2(3) NOT NULL DEFAULT GETUTCDATE(),
    PRIMARY KEY (shard_id, execute_at, timer_uuid)
);

-- Partition function and scheme (requires SQL Server Enterprise)
CREATE PARTITION FUNCTION pf_shard_id (INT)
AS RANGE LEFT FOR VALUES (0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
                         16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30);

CREATE PARTITION SCHEME ps_shard_id
AS PARTITION pf_shard_id TO ([PRIMARY], [PRIMARY], [PRIMARY], [PRIMARY], 
                           [PRIMARY], [PRIMARY], [PRIMARY], [PRIMARY],
                           [PRIMARY], [PRIMARY], [PRIMARY], [PRIMARY],
                           [PRIMARY], [PRIMARY], [PRIMARY], [PRIMARY],
                           [PRIMARY], [PRIMARY], [PRIMARY], [PRIMARY],
                           [PRIMARY], [PRIMARY], [PRIMARY], [PRIMARY],
                           [PRIMARY], [PRIMARY], [PRIMARY], [PRIMARY],
                           [PRIMARY], [PRIMARY], [PRIMARY], [PRIMARY]);

-- Recreate table with partitioning (Enterprise Edition)
-- DROP TABLE timers;
-- CREATE TABLE timers (...) ON ps_shard_id(shard_id);

-- Unique index for timer lookups  
CREATE UNIQUE INDEX idx_timer_lookup ON timers (shard_id, timer_id);


```


**Query Patterns**:
```sql
-- Timer execution query (partition elimination)
-- HIGH FREQUENCY: Executed every few seconds per shard
SELECT TOP 1000 timer_id, callback_url, payload, retry_policy
FROM timers 
WHERE shard_id = 286 AND execute_at <= GETUTCDATE()
ORDER BY execute_at ASC;

-- Direct timer lookup (partition + index seek)
-- LOWER FREQUENCY: User-driven CRUD operations
SELECT * FROM timers 
WHERE shard_id = 286 AND timer_id = 'user-reminder-123';
```





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