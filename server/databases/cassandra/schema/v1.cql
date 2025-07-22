-- Timer Service Cassandra Schema v1
-- Initial schema for the distributed durable timer service
-- Here assumes the keyspace has been created

-- Shards table for ownership management
CREATE Type shard (
    version bigint,
    owner_id text,
    claimed_at timestamp,
    metadata text,  -- JSON serialized
);

-- Create timers table with optimized primary key design
CREATE TABLE IF NOT EXISTS timers (
    shard_id int,
    type     int,            -- enum RowType { Shard, Timer}
    shard    frozen<shard>,
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
    PRIMARY KEY ( (shard_id), execute_at, timer_uuid)
) WITH CLUSTERING ORDER BY (execute_at ASC);

-- Unique index for timer_id lookups and CRUD operations
-- Note that Cassandra doesn't support unique constraints, it's has to be done via implementation. 
CREATE INDEX IF NOT EXISTS idx_timer_id ON timers (timer_id);
