# Timer Service Database Design

## Overview

This document describes the database design for the Distributed Durable Timer Service, focusing on distributed database implementations that support horizontal scaling to millions of concurrent timers. The design emphasizes partitioning strategies that enable efficient timer operations across distributed systems.


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



#### Group-Based Partitioning Trade-offs

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


### Shards Table Schema

```sql
-- Universal shards table design
CREATE TABLE shards (
    shard_id            INT NOT NULL,           -- Shard identifier (partition key for databases that support it)
    version             BIGINT NOT NULL,        -- Version number for optimistic concurrency
    owner_id   VARCHAR(255),                    -- Service instance identifier and/or address
    claimed_at          TIMESTAMP NOT NULL,     -- Ownership claim timestamp
    metadata            JSON,                   -- Additional instance metadata
    
    PRIMARY KEY (shard_id)
);
```


## Timer Schema Design

### General Idea For Most Databases
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
    
    PRIMARY KEY (shard_id, execute_at, timer_uuid)  -- Must provide physical clustering
) PARTITION BY HASH(shard_id);

-- Unique index for timer_id lookups and CRUD operations
CREATE UNIQUE INDEX idx_timer_id ON timers (shard_id, timer_id);
```
 
 This provides:

- **Optimal Performance**: `execute_at` first for fast timer execution queries
- **UUID for Uniqueness**: Eliminates complex uniqueness handling
- **timer_id Index**: Efficient CRUD operations via unique index
- **Consistent Design**: Same primary key strategy across all database types


However, this may not work for every database. We will need to adjust the design for each database based on 
the availabilities of features to support this. For example, DynamoDB doesn't support using multile columns(attributes) for sort key, and doesn't support UUID natively. And the optimization for clustering is not neccesary because this is not part of the pricing model, and there is no self-hosting option.



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

-- Shards table for ownership management
CREATE TABLE shards (
    shard_id int,
    version bigint,
    owner_id text,
    claimed_at timestamp,
    metadata text,  -- JSON serialized
    PRIMARY KEY (shard_id)
);
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

// Primary compound index for timer execution queries (provides physical ordering)
db.timers.createIndex({shardId: 1, executeAt: 1, timerUuid: 1})

// Unique index for timer_id CRUD operations  
db.timers.createIndex({shardId: 1, timerId: 1}, {unique: true})
```

**Shards Collection**:
```javascript
// Collection: shards
{
  _id: ObjectId(),
  shardId: NumberInt(286),  // Primary identifier
  version: NumberLong(42),  // Version for optimistic concurrency
  ownerId: "instance-uuid-1234",
  claimedAt: ISODate("2024-12-19T10:00:00Z"),
  metadata: {
    processId: 12345,
    serviceVersion: "1.0.0",
    nodeId: "node-001"
  }
}

// Indexes
db.shards.createIndex({shardId: 1}, {unique: true})
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
    PRIMARY KEY (shard_id, execute_at, timer_uuid) CLUSTERED,  -- Explicit clustering
    UNIQUE KEY idx_timer_id (shard_id, timer_id)  -- Unique index for CRUD operations
) PARTITION BY HASH(shard_id) PARTITIONS 1024;

-- Shards table for ownership management
CREATE TABLE shards (
    shard_id INT NOT NULL,
    version BIGINT NOT NULL,
    owner_id VARCHAR(255),
    claimed_at TIMESTAMP(3) NOT NULL,
    metadata JSON,
    PRIMARY KEY (shard_id) CLUSTERED
) PARTITION BY HASH(shard_id) PARTITIONS 32;  -- Fewer partitions as there are fewer shards
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
  "LocalSecondaryIndexes": [
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
  "executeAt": {"S": "2024-12-20T15:30:00Z"},
  "timerUuid": {"S": "550e8400-e29b-41d4-a716-446655440000"},
  "groupId": {"S": "notifications"},
  "callbackUrl": {"S": "https://api.example.com/webhook"},
  "payload": {"S": "{\"userId\":\"user123\"}"},
  "retryPolicy": {"S": "{\"maxRetries\":3}"},
  "callbackTimeout": {"S": "30s"},
  "createdAt": {"S": "2024-12-19T10:00:00Z"},
  "updatedAt": {"S": "2024-12-19T10:00:00Z"}
}
```



**Query Patterns**:
```javascript
// Timer execution query (uses LSI on executeAt)
// HIGH FREQUENCY: Executed every few seconds per shard
{
  TableName: "timers",
  IndexName: "executeAt-index",
  KeyConditionExpression: "shardId = :shardId AND executeAt <= :now",
  ExpressionAttributeValues: {
    ":shardId": {"N": "286"},
    ":now": {"S": "2024-12-20T15:30:00Z"}
  }
}

// Direct timer lookup (uses primary key)
// LOWER FREQUENCY: User-driven CRUD operations
{
  TableName: "timers",
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
- **Design Choice**: We use LSI for timer execution queries (by executeAt) to minimize costs while using the primary key for CRUD operations (by timerId). The 10GB partition limit is managed through administrative controls - when a partition approaches the limit, system administrators can create new groups with higher shard counts to redistribute the load. For customers requiring unlimited partition storage, GSI support can be offered as a premium feature with higher DynamoDB costs.

**Shards Table**:
```json
{
  "TableName": "shards",
  "KeySchema": [
    {
      "AttributeName": "shardId",
      "KeyType": "HASH"
    }
  ],
  "AttributeDefinitions": [
    {
      "AttributeName": "shardId",
      "AttributeType": "N"
    }
  ]
}
```

**Shards Item Structure**:
```json
{
  "shardId": {"N": "286"},
  "version": {"N": "42"},
  "ownerId": {"S": "instance-uuid-1234"},
  "claimedAt": {"S": "2024-12-19T10:00:00Z"},
  "metadata": {"S": "{\"processId\":12345,\"serviceVersion\":\"1.0.0\"}"}
}
```

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
    PRIMARY KEY (shard_id, execute_at, timer_uuid),  -- Clustered by default in MySQL
    UNIQUE INDEX idx_timer_lookup (shard_id, timer_id)
) PARTITION BY HASH(shard_id) PARTITIONS 32;

-- Shards table for ownership management
CREATE TABLE shards (
    shard_id INT NOT NULL,
    version BIGINT NOT NULL,
    owner_id VARCHAR(255),
    claimed_at TIMESTAMP(3) NOT NULL,
    metadata JSON,
    PRIMARY KEY (shard_id)
) PARTITION BY HASH(shard_id) PARTITIONS 8;  -- Fewer partitions as there are fewer shards
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

-- Cluster table by primary key for better physical ordering
CLUSTER timers USING timers_pkey;

-- Shards table for ownership management
CREATE TABLE shards (
    shard_id INTEGER NOT NULL,
    version BIGINT NOT NULL,
    owner_id VARCHAR(255),
    claimed_at TIMESTAMP(3) NOT NULL DEFAULT NOW(),
    metadata JSONB,
    PRIMARY KEY (shard_id)
) PARTITION BY HASH (shard_id);


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
ORGANIZATION INDEX                    -- Index Organized Table for clustering
PARTITION BY HASH (shard_id) PARTITIONS 32;

-- Unique index for timer lookups
CREATE UNIQUE INDEX idx_timer_lookup ON timers (shard_id, timer_id);

-- Shards table for ownership management
CREATE TABLE shards (
    shard_id NUMBER(10) NOT NULL,
    version NUMBER(19) NOT NULL,
    owner_id VARCHAR2(255),
    claimed_at TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP,
    metadata CLOB CHECK (metadata IS JSON),
    PRIMARY KEY (shard_id)
)
ORGANIZATION INDEX                    -- Index Organized Table for clustering
PARTITION BY HASH (shard_id) PARTITIONS 8;
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
    PRIMARY KEY CLUSTERED (shard_id, execute_at, timer_uuid)  -- Explicit clustering
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

-- Shards table for ownership management
CREATE TABLE shards (
    shard_id INT NOT NULL,
    version BIGINT NOT NULL,
    owner_id NVARCHAR(255),
    claimed_at DATETIME2(3) NOT NULL DEFAULT GETUTCDATE(),
    metadata NVARCHAR(MAX) CHECK (ISJSON(metadata) = 1),
    PRIMARY KEY CLUSTERED (shard_id)
);

-- Partition shards table using same scheme (fewer partitions for fewer shards)
CREATE PARTITION FUNCTION pf_shards_id (INT)
AS RANGE LEFT FOR VALUES (0, 1, 2, 3, 4, 5, 6, 7);

CREATE PARTITION SCHEME ps_shards_id
AS PARTITION pf_shards_id TO ([PRIMARY], [PRIMARY], [PRIMARY], [PRIMARY], 
                             [PRIMARY], [PRIMARY], [PRIMARY], [PRIMARY], [PRIMARY]);
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


---

*This design provides a foundation for horizontal scaling while maintaining strong consistency and performance across all supported distributed databases.* 