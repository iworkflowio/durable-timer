CREATE TABLE timers (
    shard_id INTEGER NOT NULL,
    row_type SMALLINT NOT NULL,
    timer_execute_at TIMESTAMP(3) NOT NULL,
    timer_uuid_high BIGINT NOT NULL,
    timer_uuid_low BIGINT NOT NULL,
    timer_id VARCHAR(255),
    timer_namespace VARCHAR(255),
    timer_callback_url VARCHAR(2048),
    timer_payload JSONB,
    timer_retry_policy JSONB,
    timer_callback_timeout_seconds INTEGER DEFAULT 30,
    shard_version BIGINT,
    shard_owner_addr VARCHAR(255),
    shard_claimed_at TIMESTAMP(3),
    shard_metadata JSONB,
    timer_created_at TIMESTAMP(3) NOT NULL DEFAULT NOW(),
    timer_attempts INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (shard_id, row_type, timer_execute_at, timer_uuid_high, timer_uuid_low)
);

-- Unique index for timer lookups
CREATE UNIQUE INDEX idx_timer_lookup ON timers (shard_id, row_type, timer_namespace, timer_id); 