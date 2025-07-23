# Timer Service Database Design

## Overview

This document describes the database design for the Distributed Durable Timer Service, focusing on distributed database implementations that support horizontal scaling to millions of concurrent timers. The design emphasizes partitioning strategies that enable efficient timer operations across distributed systems.


## Partitioning Strategy

### Shard ID Calculation

```
shardId = hash(timerId) % namespace.numShards
```

**Implementation Steps**:
1. Client provides `namespace` and `timerId`
2. System looks up `numShards` configuration for the namespace
3. Hash `timerId` using consistent hash function (e.g., CRC32, SHA1)
4. Apply modulo operation: `shardId = hash(timerId) % numShards`
5. Use `shardId` as partition key for database operations

**Example**:
```
namespace: "user-services"  (configured with numShards: 1024)
timerId: "user-reminder-123"
hash("user-reminder-123") = 0x7B2C4A1E = 2067390238
shardId = 2067390238 % 1024 = 286
```

### Namespace Configuration

Namespaces support different scale requirements:

| Namespace Type | Use Case | numShards | Expected Load |
|------------|----------|-----------|---------------|
| small | Development/Testing | 16 | < 10K timers |
| medium | Standard Production | 256 | < 1M timers |
| large | High-Volume | 1024 | < 10M timers |
| xlarge | Massive Scale | 4096 | < 100M timers |



#### Namespace-Based Partitioning Trade-offs

**Design Constraint: Fixed numShards per Namespace**
- **Immutable Configuration**: Once a namespace is created with a specific `numShards` value, it cannot be changed
- **Lookup Dependency**: Timer lookups depend on `hash(timerId) % numShards` - changing numShards would make existing timers unfindable
- **Comparison to Other Systems**: Unlike systems like Uber Cadence where `numShards` is globally fixed for the entire cluster, our approach allows different namespaces to have different shard counts

**Pros: Flexible Scaling Model**  
- **Namespace-Level Flexibility**: New namespaces can be created with appropriate shard counts based on expected load
- **Workload Isolation**: Different use cases can use namespaces with different scale characteristics (e.g., dev: 16 shards, production: 1024 shards)
- **No Global Resharding**: Avoids cluster-wide resharding operations that affect all workloads simultaneously
- **Predictable Performance**: Shard assignment is deterministic within each namespace
- **Simple Implementation**: No complex data migration or resharding algorithms needed

**Cons: Namespace Management Overhead**
- **Planning Required**: Must estimate shard requirements when creating namespaces
- **API Complexity**: Users must understand and provide `namespace` for all timer operations
- **Immutable Scaling**: Cannot increase shard count for existing namespaces with high load

**Impact Assessment**
- **Superior to Global Fixed Sharding**: More flexible than systems where shard count is fixed cluster-wide
- **Better Than Dynamic Resharding**: Avoids complexity of live data migration and consistency challenges
- **Operational Benefits**: Predictable performance characteristics and no resharding downtime
- **Trade-off Justification**: Namespace planning overhead is preferable to complex resharding infrastructure





## Shard Ownership Management

### Problem Statement

In distributed systems, membership frameworks (e.g., Consul, etcd, Raft) provide eventual consistency through gossip-based protocols. This creates a critical race condition where multiple service instances may simultaneously believe they own the same shard during membership changes or network partitions.

**Race Condition Example**:
1. Instance A owns shard 42 and loads timers for execution (T+1 minute window)
2. Network partition occurs, Instance B also claims shard 42
3. Instance B inserts new timer into T+1 minute window
4. Instance A executes loaded timers and performs range delete
5. Instance B's timer is deleted without execution ❌

### Solution: Versioned Shard Ownership

We implement optimistic concurrency control using a dedicated `shards` table with version-based ownership claims.

**Shard Ownership Protocol**:
1. **Claim Shard**: Instance loads current version, increments it, and stores new version
2. **Memory Cache**: Instance keeps version in memory for all subsequent operations
3. **Version Check**: During any write operation, instance verifies version hasn't changed within a transaction
4. **Graceful Exit**: If version mismatch detected, instance releases shard ownership


In most databases, transactions within a single table and same partition have the best performance.
We will merge the shard table into the timer table below using a unified schema design.


## Unified Timer+Shard Schema Design

### Design Rationale
To optimize transaction performance and data locality, we merge the timers and shards tables into a single unified table

### General Design Pattern

**Core Concepts**:
* **Single Table**: All data (timers and shard ownership) in one table
* **Row Type Discrimination**: `row_type` field distinguishes record types:
  - `row_type = 1`: Shard ownership record (one per shard)
  - `row_type = 2`: Timer record (many per shard)
* **Partition Key**: `shard_id` ensures data locality within same shard
* **Clustering Keys**: Optimized for most frequent query patterns
* **Conditional Fields**: Fields used based on row type
* Fields starting with "timer_" are specific for timer rows. Starting with "shards" are for shard rows(except shard_id which is the partition key)

**Unified Table Structure**:
```sql
CREATE TABLE timers (
    shard_id            INT NOT NULL,              -- Partition key
    row_type            SMALLINT NOT NULL,         -- 1=shard, 2=timer  
    timer_execute_at    TIMESTAMP,                 -- Clustering key (timer rows only)
    timer_uuid          UUID,                      -- Uniqueness (timer rows only)
    
    -- Timer-specific fields (row_type = 2)
    timer_id            VARCHAR(255),              -- Business identifier
    timer_namespace     VARCHAR(255),              -- Namespace identifier  
    timer_callback_url  VARCHAR(2048),             -- HTTP callback URL
    timer_payload       JSON,                      -- Custom payload data
    timer_retry_policy  JSON,                      -- Retry configuration
    timer_callback_timeout_seconds INT,            -- Timeout in seconds
    timer_created_at    TIMESTAMP NOT NULL,        -- Creation time
    timer_attempts      INT NOT NULL DEFAULT 0,    -- Retry attempt count
    
    -- Shard-specific fields (row_type = 1)  
    shard_version       BIGINT,                    -- Optimistic concurrency control
    shard_owner_id      VARCHAR(255),              -- Current owner instance
    shard_claimed_at    TIMESTAMP,                 -- When ownership was claimed
    shard_metadata      TEXT,                      -- Owner metadata (JSON)
    
    PRIMARY KEY (shard_id, row_type, timer_execute_at, timer_uuid)
) PARTITION BY HASH(shard_id);

-- Indexes for efficient lookups
CREATE INDEX idx_timer_lookup ON timers (shard_id, row_type, timer_namespace, timer_id) WHERE row_type = 2;
```

## TimerUuid and Upsert Behavior

### Stable UUID Requirement

The `timer_uuid` field must be provided by callers and should be **stable** for the same timer (namespace + timer_id combination). This enables consistent upsert behavior across all database implementations.

**UUID Generation Strategy**:
```go
// Example: Generate stable UUID from namespace and timer_id
func generateTimerUUID(namespace, timerId string) string {
    hash := md5.Sum([]byte(fmt.Sprintf("%s:%s", namespace, timerId)))
    uuid, _ := gocql.UUIDFromBytes(hash[:])
    return uuid.String()
}
```

### Database-Specific Upsert Behavior

| Database | Upsert Mechanism | Behavior |
|----------|------------------|----------|
| **MySQL** | `INSERT ... ON DUPLICATE KEY UPDATE` | True upsert - overwrites existing timer |
| **PostgreSQL** | `INSERT ... ON CONFLICT DO UPDATE` | True upsert - overwrites existing timer |
| **MongoDB** | `ReplaceOne` with `upsert: true` | True upsert - overwrites existing timer |
| **DynamoDB** | `PutItem` (native behavior) | True upsert - overwrites existing timer |
| **Cassandra** | `INSERT` (natural behavior) | **Partial upsert** - see limitation below |

### Cassandra Limitation: Execute Time Clustering

⚠️ **Important Limitation**: Cassandra allows multiple timer records with the same `timer_id` if they have different `timer_execute_at` values. This is due to the clustering key design that includes `timer_execute_at` for optimal batch read/delete performance.

**Scenario**:
```
1. Create timer: namespace="alerts", timer_id="daily-report", execute_at="2025-01-01 09:00"
2. Update timer: namespace="alerts", timer_id="daily-report", execute_at="2025-01-01 10:00"
   → Result: Two separate records in Cassandra (different clustering key values)
```

**Design Trade-off**: This limitation is acceptable because:
- Cassandra's clustering design provides significant performance benefits for batch timer operations
- Timer updates with different execution times are less common than creation/deletion operations
- The performance benefits for high-volume timer processing outweigh the upsert limitation
- Other databases (MySQL, PostgreSQL, MongoDB, DynamoDB) provide true upsert behavior
- User can still use DeleteTimer API to delete first, if duplicate is not desirable

**Query Patterns**:
```sql
-- Shard ownership operations (row_type = 1)
SELECT shard_version, shard_owner_id, shard_claimed_at, shard_metadata 
FROM timers WHERE shard_id = ? AND row_type = 1;

-- Timer execution query (row_type = 2, high frequency)
SELECT timer_id, timer_namespace, timer_callback_url, timer_payload, timer_retry_policy, timer_callback_timeout_seconds
FROM timers WHERE shard_id = ? AND row_type = 2 AND timer_execute_at <= ?
ORDER BY timer_execute_at ASC;

-- Timer CRUD operations (row_type = 2, lower frequency)  
SELECT * FROM timers WHERE shard_id = ? AND row_type = 2 AND timer_namespace = ? AND timer_id = ?;
```

## Database-Specific Implementations

### Apache Cassandra

**Unified Table Definition**:
```cql
CREATE TABLE timers (
    shard_id int,
    row_type smallint,           -- 1=shard, 2=timer
    timer_execute_at timestamp,  -- Clustering key (only for timer rows)
    timer_uuid uuid,             -- Uniqueness (only for timer rows)
    
    -- Timer-specific fields (row_type = 2)
    timer_id text,
    timer_namespace text,
    timer_callback_url text,
    timer_payload text,          -- JSON serialized
    timer_retry_policy text,     -- JSON serialized
    timer_callback_timeout_seconds int,
    timer_created_at timestamp,
    timer_attempts int,
    
    -- Shard-specific fields (row_type = 1)
    shard_version bigint,
    shard_owner_id text,
    shard_claimed_at timestamp,
    shard_metadata text,         -- JSON serialized

    
    PRIMARY KEY (shard_id, row_type, timer_execute_at, timer_uuid)
) WITH CLUSTERING ORDER BY (row_type ASC, timer_execute_at ASC, timer_uuid ASC);

-- Index for timer lookups by namespace and timer_id
CREATE INDEX idx_timer_namespace_id ON timers (timer_namespace, timer_id);
```

**Query Patterns**:
```cql
-- Shard ownership claim (row_type = 1)
SELECT shard_version, shard_owner_id, shard_claimed_at, shard_metadata
FROM timers WHERE shard_id = ? AND row_type = 1;

-- Update shard ownership (CAS operation)
UPDATE timers SET shard_version = ?, shard_owner_id = ?, shard_claimed_at = ?, shard_metadata = ?
WHERE shard_id = ? AND row_type = 1 IF shard_version = ?;

-- Timer execution query (row_type = 2, leverages clustering)
-- HIGH FREQUENCY: Executed every few seconds per shard
SELECT timer_id, timer_namespace, timer_callback_url, timer_payload, timer_retry_policy, timer_callback_timeout_seconds
FROM timers WHERE shard_id = ? AND row_type = 2 AND timer_execute_at <= ?
ORDER BY timer_execute_at ASC;

-- Direct timer lookup (uses secondary index)
-- LOWER FREQUENCY: User-driven CRUD operations  
SELECT * FROM timers WHERE shard_id = ? AND row_type = 2 AND timer_namespace = ? AND timer_id = ?;
```



### MongoDB

**Unified Collection Structure**:
```javascript
// Collection: timers (unified for both shard and timer data)

// Timer record (rowType = 2)
{
  _id: ObjectId(),
  shard_id: NumberInt(286),
  row_type: NumberInt(2),           // 2 = timer record
  timer_execute_at: ISODate("2024-12-20T15:30:00Z"),
  timer_uuid: UUID("550e8400-e29b-41d4-a716-446655440000"),
  
  // Timer-specific fields
  timer_id: "user-reminder-123", 
  timer_namespace: "user-services",
  timer_callback_url: "https://api.example.com/webhook",
  timer_payload: {
    userId: "user123",
    action: "send_reminder"
  },
  timer_retry_policy: {
    maxRetries: 3,
    initialInterval: "30s"
  },
  timer_callback_timeout_seconds: 30,

  timer_created_at: ISODate("2024-12-19T10:00:00Z"),
  timer_attempts: NumberInt(0)
}

// Shard ownership record (rowType = 1)
{
  _id: ObjectId(),
  shard_id: NumberInt(286),
  row_type: NumberInt(1),           // 1 = shard ownership record
  
  // Shard-specific fields  
  shard_version: NumberLong(42),
  shard_owner_id: "instance-uuid-1234",
  shard_claimed_at: ISODate("2024-12-19T10:00:00Z"),
  shard_metadata: {
    processId: 12345,
    serviceVersion: "1.0.0",
    nodeId: "node-001"
  },
}
```

**Sharding Configuration**:
```javascript
// Enable sharding on database
sh.enableSharding("timerservice")

// Shard collection on shard_id  
sh.shardCollection("timerservice.timers", {shard_id: 1})

// Compound index optimized for timer execution queries
db.timers.createIndex({shard_id: 1, row_type: 1, timer_execute_at: 1, timer_uuid: 1})

// Index for timer CRUD operations
db.timers.createIndex({shard_id: 1, row_type: 1, timer_namespace: 1, timer_id: 1}, {
  unique: true
})

```

**Query Patterns**:
```javascript
// Shard ownership claim (row_type = 1)
// MEDIUM FREQUENCY: Ownership changes during failover
db.timers.findOne({
  shard_id: 286,
  row_type: 1
}, {
  shard_version: 1,
  shard_owner_id: 1, 
  shard_claimed_at: 1,
  shard_metadata: 1
})

// Update shard ownership (atomic operation with version check)
db.timers.updateOne({
  shard_id: 286,
  row_type: 1,
  shard_version: 42  // Current version for optimistic concurrency
}, {
  $set: {
    shard_version: 43,
    shard_owner_id: "new-instance-uuid",
    shard_claimed_at: new Date(),
    shard_metadata: {...},
    updated_at: new Date()
  }
})

// Timer execution query (row_type = 2, optimized - uses compound index)
// HIGH FREQUENCY: Executed every few seconds per shard  
db.timers.find({
  shard_id: 286,
  row_type: 2,
  timer_execute_at: {$lte: new Date()}
}).sort({timer_execute_at: 1})

// Direct timer lookup (uses partial index)
// LOWER FREQUENCY: User-driven CRUD operations
db.timers.findOne({
  shard_id: 286,
  row_type: 2,
  timer_namespace: "user-services",
  timer_id: "user-reminder-123"  
})
```

### TiDB (MySQL Compatible)

**Unified Table Definition**:
```sql
CREATE TABLE timers (
    shard_id INT NOT NULL,
    row_type SMALLINT NOT NULL,        -- 1=shard, 2=timer
    timer_execute_at TIMESTAMP(3),     -- Clustering key (timer rows only)
    timer_uuid CHAR(36),               -- UUID as string (timer rows only)
    
    -- Timer-specific fields (row_type = 2)
    timer_id VARCHAR(255),
    timer_namespace VARCHAR(255),
    timer_callback_url VARCHAR(2048),
    timer_payload JSON,
    timer_retry_policy JSON,
    timer_callback_timeout_seconds INT DEFAULT 30,
    timer_created_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    timer_attempts INT NOT NULL DEFAULT 0,
    
    -- Shard-specific fields (row_type = 1)
    shard_version BIGINT,
    shard_owner_id VARCHAR(255),
    shard_claimed_at TIMESTAMP(3),
    shard_metadata JSON,
    
    PRIMARY KEY (shard_id, row_type, timer_execute_at, timer_uuid) CLUSTERED,
    UNIQUE KEY idx_timer_lookup (shard_id, row_type, timer_namespace, timer_id)
) PARTITION BY HASH(shard_id) PARTITIONS 1024;
```


**Query Patterns**:
```sql
-- Shard ownership claim (row_type = 1)
-- MEDIUM FREQUENCY: Ownership changes during failover
SELECT shard_version, shard_owner_id, shard_claimed_at, shard_metadata
FROM timers WHERE shard_id = ? AND row_type = 1;

-- Update shard ownership (with optimistic concurrency)
UPDATE timers 
SET shard_version = ?, shard_owner_id = ?, shard_claimed_at = ?, shard_metadata = ?, updated_at = NOW()
WHERE shard_id = ? AND row_type = 1 AND shard_version = ?;

-- Timer execution query (row_type = 2, optimized - uses clustered primary key)
-- HIGH FREQUENCY: Executed every few seconds per shard
SELECT timer_id, timer_namespace, timer_callback_url, timer_payload, timer_retry_policy, timer_callback_timeout_seconds
FROM timers 
WHERE shard_id = ? AND row_type = 2 AND timer_execute_at <= NOW()
ORDER BY timer_execute_at ASC, timer_uuid ASC
LIMIT 1000;

-- Direct timer lookup (uses unique secondary index)
-- LOWER FREQUENCY: User-driven CRUD operations
SELECT * FROM timers 
WHERE shard_id = ? AND row_type = 2 AND timer_namespace = ? AND timer_id = ?;
```

### DynamoDB

**Unified Table Structure**:
```json
{
  "TableName": "timers",
  "KeySchema": [
    {
      "AttributeName": "shard_id",
      "KeyType": "HASH"
    },
    {
      "AttributeName": "sortKey", 
      "KeyType": "RANGE"
    }
  ],
  "AttributeDefinitions": [
    {
      "AttributeName": "shard_id",
      "AttributeType": "N"
    },
    {
      "AttributeName": "sortKey",
      "AttributeType": "S"
    },
    {
      "AttributeName": "timer_execute_at",
      "AttributeType": "S"
    }
  ],
  "LocalSecondaryIndexes": [
    {
      "IndexName": "timer_execute_at-index",
      "KeySchema": [
        {
          "AttributeName": "shard_id",
          "KeyType": "HASH"
        },
        {
          "AttributeName": "timer_execute_at",
          "KeyType": "RANGE"
        }
      ]
    }
  ]
}
```

**Item Structure**:
```json
// Timer record (row_type = TIMER)
{
  "shard_id": {"N": "286"},
  "sortKey": {"S": "TIMER#user-services#user-reminder-123"},  // Composite sort key for timers
  "row_type": {"S": "TIMER"},
  "timer_execute_at": {"S": "2024-12-20T15:30:00Z"},
  "timer_uuid": {"S": "550e8400-e29b-41d4-a716-446655440000"},
  
  // Timer-specific fields
  "timer_id": {"S": "user-reminder-123"},
  "timer_namespace": {"S": "user-services"},
  "timer_callback_url": {"S": "https://api.example.com/webhook"},
  "timer_payload": {"S": "{\"userId\":\"user123\"}"},
  "timer_retry_policy": {"S": "{\"maxRetries\":3}"},
  "timer_callback_timeout_seconds": {"N": "30"},
  "timer_created_at": {"S": "2024-12-19T10:00:00Z"},
  "timer_attempts": {"N": "0"}
}

// Shard ownership record (row_type = SHARD)
{
  "shard_id": {"N": "286"},
  "sortKey": {"S": "SHARD#OWNERSHIP"},  // Fixed sort key for shard records
  "row_type": {"S": "SHARD"},
  
  // Shard-specific fields
  "shard_version": {"N": "42"},
  "shard_owner_id": {"S": "instance-uuid-1234"},
  "shard_claimed_at": {"S": "2024-12-19T10:00:00Z"},
  "shard_metadata": {"S": "{\"processId\":12345,\"serviceVersion\":\"1.0.0\"}"},

}
```

**Query Patterns**:
```javascript
// Shard ownership claim (row_type = SHARD)
// MEDIUM FREQUENCY: Ownership changes during failover
{
  TableName: "timers",
  KeyConditionExpression: "shard_id = :shard_id AND sortKey = :sortKey",
  ExpressionAttributeValues: {
    ":shard_id": {"N": "286"},
    ":sortKey": {"S": "SHARD#OWNERSHIP"}
  }
}

// Update shard ownership (conditional update with version check)
{
  TableName: "timers",
  Key: {
    "shard_id": {"N": "286"},
    "sortKey": {"S": "SHARD#OWNERSHIP"}
  },
  UpdateExpression: "SET shard_version = :newVersion, shard_owner_id = :ownerId, shard_claimed_at = :claimedAt, shard_metadata = :metadata, updated_at = :now",
  ConditionExpression: "shard_version = :currentVersion",
  ExpressionAttributeValues: {
    ":newVersion": {"N": "43"},
    ":currentVersion": {"N": "42"},
    ":ownerId": {"S": "new-instance-uuid"},
    ":claimedAt": {"S": "2024-12-20T10:00:00Z"},
    ":metadata": {"S": "{...}"},
    ":now": {"S": "2024-12-20T10:00:00Z"}
  }
}

// Timer execution query (uses LSI on timer_execute_at, filters by row_type)
// HIGH FREQUENCY: Executed every few seconds per shard
{
  TableName: "timers",
  IndexName: "timer_execute_at-index",
  KeyConditionExpression: "shard_id = :shard_id AND timer_execute_at <= :now",
  FilterExpression: "row_type = :row_type",
  ExpressionAttributeValues: {
    ":shard_id": {"N": "286"},
    ":now": {"S": "2024-12-20T15:30:00Z"},
    ":row_type": {"S": "TIMER"}
  }
}

// Direct timer lookup (uses primary key with composite sort key)
// LOWER FREQUENCY: User-driven CRUD operations
{
  TableName: "timers",
  KeyConditionExpression: "shard_id = :shard_id AND sortKey = :sortKey",
  ExpressionAttributeValues: {
    ":shard_id": {"N": "286"},
    ":sortKey": {"S": "TIMER#user-services#user-reminder-123"}
  }
}
```

**Note on LSI vs GSI Cost in DynamoDB**:

- **LSI (Local Secondary Index)**: Storage for LSI is billed at the same rate as the base table, and write costs are included in the base table's write capacity. LSI has a strict 10GB storage limit per partition, but this is manageable through proper shard management. LSI queries are always strongly consistent and operate within the same partition as the base table.
- **GSI (Global Secondary Index)**: GSI storage is billed separately from the base table, and you pay for both read and write capacity (or on-demand) on the GSI in addition to the base table. GSI does **not** have the 10GB per-partition limit—storage is effectively unlimited and scales independently.
- **Cost Comparison**: LSI is significantly more cost-effective for write-heavy workloads since writes don't consume additional capacity. GSI incurs additional costs for every write operation that affects the index.
- **Design Choice**: We use LSI for timer execution queries (by executeAt) to minimize costs while using the composite sort key for direct CRUD operations (by namespace and timerId). The unified table design keeps all shard and timer data in the same partition, improving data locality and transaction performance. The 10GB partition limit is managed through administrative controls - when a partition approaches the limit, system administrators can create new namespaces with higher shard counts to redistribute the load.

## Traditional SQL Databases

While the distributed databases above provide native sharding and horizontal scaling, traditional SQL databases can also support the timer service with appropriate configuration and potentially external sharding mechanisms.

### MySQL

**Unified Table Definition**:
```sql
CREATE TABLE timers (
    shard_id INT NOT NULL,
    row_type SMALLINT NOT NULL,        -- 1=shard, 2=timer
    timer_execute_at TIMESTAMP(3),     -- Clustering key (timer rows only)
    timer_uuid CHAR(36),               -- UUID as string (timer rows only)
    
    -- Timer-specific fields (row_type = 2)
    timer_id VARCHAR(255),
    timer_namespace VARCHAR(255),
    timer_callback_url VARCHAR(2048),
    timer_payload JSON,
    timer_retry_policy JSON,
    timer_callback_timeout_seconds INT DEFAULT 30,
    timer_created_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    timer_attempts INT NOT NULL DEFAULT 0,

    -- Shard-specific fields (row_type = 1)
    shard_version BIGINT,
    shard_owner_id VARCHAR(255),
    shard_claimed_at TIMESTAMP(3),
    shard_metadata JSON,
    
    PRIMARY KEY (shard_id, row_type, timer_execute_at, timer_uuid),  -- Clustered by default in MySQL
    UNIQUE INDEX idx_timer_lookup (shard_id, row_type, timer_namespace, timer_id)
) PARTITION BY HASH(shard_id) PARTITIONS 32;
```

**Query Patterns**:
```sql
-- Shard ownership claim (row_type = 1)
-- MEDIUM FREQUENCY: Ownership changes during failover
SELECT shard_version, shard_owner_id, shard_claimed_at, shard_metadata
FROM timers WHERE shard_id = ? AND row_type = 1;

-- Update shard ownership (with optimistic concurrency)
UPDATE timers 
SET shard_version = ?, shard_owner_id = ?, shard_claimed_at = ?, shard_metadata = ?, updated_at = NOW(3)
WHERE shard_id = ? AND row_type = 1 AND shard_version = ?;

-- Timer execution query (row_type = 2, optimized - uses clustered primary key)
-- HIGH FREQUENCY: Executed every few seconds per shard
SELECT timer_id, timer_namespace, timer_callback_url, timer_payload, timer_retry_policy, timer_callback_timeout_seconds
FROM timers 
WHERE shard_id = ? AND row_type = 2 AND timer_execute_at <= NOW(3)
ORDER BY timer_execute_at ASC, timer_uuid ASC
LIMIT 1000;

-- Direct timer lookup (uses unique secondary index)
-- LOWER FREQUENCY: User-driven CRUD operations
SELECT * FROM timers 
WHERE shard_id = ? AND row_type = 2 AND timer_namespace = ? AND timer_id = ?;
```

### PostgreSQL

**Unified Table Definition**:
```sql
CREATE TABLE timers (
    shard_id INTEGER NOT NULL,
    row_type SMALLINT NOT NULL,        -- 1=shard, 2=timer
    timer_execute_at TIMESTAMP(3),     -- Clustering key (timer rows only)
    timer_uuid UUID,                   -- Uniqueness (timer rows only)
    
    -- Timer-specific fields (row_type = 2)
    timer_id VARCHAR(255),
    timer_namespace VARCHAR(255),
    timer_callback_url VARCHAR(2048),
    timer_payload JSONB,
    timer_retry_policy JSONB,
    timer_callback_timeout_seconds INTEGER DEFAULT 30,
    timer_created_at TIMESTAMP(3) NOT NULL DEFAULT NOW(),
    timer_attempts INTEGER NOT NULL DEFAULT 0,

    -- Shard-specific fields (row_type = 1)
    shard_version BIGINT,
    shard_owner_id VARCHAR(255),
    shard_claimed_at TIMESTAMP(3),
    shard_metadata JSONB,
    
    PRIMARY KEY (shard_id, row_type, timer_execute_at, timer_uuid)
) PARTITION BY HASH (shard_id);

-- Create partitions (example for 32 partitions)
-- CREATE TABLE timers_p0 PARTITION OF timers FOR VALUES WITH (modulus 32, remainder 0);
-- ... repeat for p1 through p31

-- Unique index for timer lookups
CREATE UNIQUE INDEX idx_timer_lookup ON timers (shard_id, row_type, timer_namespace, timer_id);

-- Cluster table by primary key for better physical ordering
CLUSTER timers USING timers_pkey;
```


**Query Patterns**:
```sql
-- Shard ownership claim (row_type = 1)
-- MEDIUM FREQUENCY: Ownership changes during failover
SELECT shard_version, shard_owner_id, shard_claimed_at, shard_metadata
FROM timers WHERE shard_id = ? AND row_type = 1;

-- Update shard ownership (with optimistic concurrency)
UPDATE timers 
SET shard_version = ?, shard_owner_id = ?, shard_claimed_at = ?, shard_metadata = ?, updated_at = NOW()
WHERE shard_id = ? AND row_type = 1 AND shard_version = ?;

-- Timer execution query (row_type = 2, partition-aware)
-- HIGH FREQUENCY: Executed every few seconds per shard
SELECT timer_id, timer_namespace, timer_callback_url, timer_payload, timer_retry_policy, timer_callback_timeout_seconds
FROM timers 
WHERE shard_id = ? AND row_type = 2 AND timer_execute_at <= NOW()
ORDER BY timer_execute_at ASC
LIMIT 1000;

-- Direct timer lookup (uses unique index)
-- LOWER FREQUENCY: User-driven CRUD operations
SELECT * FROM timers 
WHERE shard_id = ? AND row_type = 2 AND timer_namespace = ? AND timer_id = ?;
```

### Oracle Database

**Unified Table Definition**:
```sql
CREATE TABLE timers (
    shard_id NUMBER(10) NOT NULL,
    row_type NUMBER(5) NOT NULL,       -- 1=shard, 2=timer
    timer_execute_at TIMESTAMP(3),     -- Clustering key (timer rows only)
    timer_uuid RAW(16),                -- UUID as binary (timer rows only)
    
    -- Timer-specific fields (row_type = 2)
    timer_id VARCHAR2(255),
    timer_namespace VARCHAR2(255),
    timer_callback_url VARCHAR2(2048),
    timer_payload CLOB CHECK (timer_payload IS JSON),
    timer_retry_policy CLOB CHECK (timer_retry_policy IS JSON),
    timer_callback_timeout_seconds NUMBER(10) DEFAULT 30,
    timer_created_at TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP,
    timer_attempts NUMBER(10) DEFAULT 0,

    -- Shard-specific fields (row_type = 1)
    shard_version NUMBER(19),
    shard_owner_id VARCHAR2(255),
    shard_claimed_at TIMESTAMP(3),
    shard_metadata CLOB CHECK (shard_metadata IS JSON),
    
    PRIMARY KEY (shard_id, row_type, timer_execute_at, timer_uuid)
) 
ORGANIZATION INDEX                    -- Index Organized Table for clustering
PARTITION BY HASH (shard_id) PARTITIONS 32;

-- Unique index for timer lookups
CREATE UNIQUE INDEX idx_timer_lookup ON timers (shard_id, row_type, timer_namespace, timer_id);
```


**Query Patterns**:
```sql
-- Shard ownership claim (row_type = 1)
-- MEDIUM FREQUENCY: Ownership changes during failover
SELECT shard_version, shard_owner_id, shard_claimed_at, shard_metadata
FROM timers WHERE shard_id = ? AND row_type = 1;

-- Update shard ownership (with optimistic concurrency)
UPDATE timers 
SET shard_version = ?, shard_owner_id = ?, shard_claimed_at = ?, shard_metadata = ?, updated_at = CURRENT_TIMESTAMP
WHERE shard_id = ? AND row_type = 1 AND shard_version = ?;

-- Timer execution query (row_type = 2, partition pruning)
-- HIGH FREQUENCY: Executed every few seconds per shard
SELECT timer_id, timer_namespace, timer_callback_url, timer_payload, timer_retry_policy, timer_callback_timeout_seconds
FROM timers 
WHERE shard_id = ? AND row_type = 2 AND timer_execute_at <= CURRENT_TIMESTAMP
ORDER BY timer_execute_at ASC
FETCH FIRST 1000 ROWS ONLY;

-- Direct timer lookup (partition + index access)
-- LOWER FREQUENCY: User-driven CRUD operations
SELECT * FROM timers 
WHERE shard_id = ? AND row_type = 2 AND timer_namespace = ? AND timer_id = ?;
```

### Microsoft SQL Server

**Unified Table Definition**:
```sql
CREATE TABLE timers (
    shard_id INT NOT NULL,
    row_type SMALLINT NOT NULL,        -- 1=shard, 2=timer
    timer_execute_at DATETIME2(3),     -- Clustering key (timer rows only)
    timer_uuid UNIQUEIDENTIFIER,       -- Uniqueness (timer rows only)
    
    -- Timer-specific fields (row_type = 2)
    timer_id NVARCHAR(255),
    timer_namespace NVARCHAR(255),
    timer_callback_url NVARCHAR(2048),
    timer_payload NVARCHAR(MAX) CHECK (ISJSON(timer_payload) = 1),
    timer_retry_policy NVARCHAR(MAX) CHECK (ISJSON(timer_retry_policy) = 1),
    timer_callback_timeout_seconds INT DEFAULT 30,
    timer_created_at DATETIME2(3) NOT NULL DEFAULT GETUTCDATE(),
    timer_attempts INT NOT NULL DEFAULT 0,

    -- Shard-specific fields (row_type = 1)
    shard_version BIGINT,
    shard_owner_id NVARCHAR(255),
    shard_claimed_at DATETIME2(3),
    shard_metadata NVARCHAR(MAX) CHECK (ISJSON(shard_metadata) = 1),
    
    PRIMARY KEY CLUSTERED (shard_id, row_type, timer_execute_at, timer_uuid)  -- Explicit clustering
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
CREATE UNIQUE INDEX idx_timer_lookup ON timers (shard_id, row_type, timer_namespace, timer_id);
AS RANGE LEFT FOR VALUES (0, 1, 2, 3, 4, 5, 6, 7);

CREATE PARTITION SCHEME ps_shards_id
AS PARTITION pf_shards_id TO ([PRIMARY], [PRIMARY], [PRIMARY], [PRIMARY], 
                             [PRIMARY], [PRIMARY], [PRIMARY], [PRIMARY], [PRIMARY]);
```


**Query Patterns**:
```sql
-- Timer execution query (partition elimination)
-- HIGH FREQUENCY: Executed every few seconds per shard
SELECT TOP 1000 timer_id, timer_namespace, callback_url, payload, retry_policy
FROM timers 
WHERE shard_id = 286 AND execute_at <= GETUTCDATE()
ORDER BY execute_at ASC;

-- Direct timer lookup (partition + index seek)
-- LOWER FREQUENCY: User-driven CRUD operations
SELECT * FROM timers 
WHERE shard_id = 286 AND timer_namespace = 'user-services' AND timer_id = 'user-reminder-123';
```


---

*This design provides a foundation for horizontal scaling while maintaining strong consistency and performance across all supported distributed databases.* 