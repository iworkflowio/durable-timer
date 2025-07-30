CREATE TABLE timers (
    shard_id int,
    row_type smallint,           -- 1=shard, 2=timer
    timer_execute_at timestamp,  -- Clustering key (only for timer rows)
    timer_uuid_high bigint,      -- UUID high 64 bits (only for timer rows)
    timer_uuid_low bigint,       -- UUID low 64 bits (only for timer rows)
    
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
    shard_owner_addr text,
    shard_claimed_at timestamp,
    shard_metadata text,         -- JSON serialized

    
    PRIMARY KEY (shard_id, row_type, timer_execute_at, timer_uuid_high, timer_uuid_low)
) WITH CLUSTERING ORDER BY (row_type ASC, timer_execute_at ASC, timer_uuid_high ASC, timer_uuid_low ASC);

-- Index for timer lookups by namespace and timer_id
CREATE INDEX idx_timer_namespace_id ON timers (timer_namespace, timer_id);
