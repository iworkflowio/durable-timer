package cassandra

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gocql/gocql"
	"github.com/iworkflowio/durable-timer/config"
	"github.com/iworkflowio/durable-timer/databases"
)

// CassandraTimerStore implements TimerStore interface for Cassandra
type CassandraTimerStore struct {
	session *gocql.Session
}

// NewCassandraTimerStore creates a new Cassandra timer store
func NewCassandraTimerStore(config *config.CassandraConnectConfig) (databases.TimerStore, error) {
	cluster := gocql.NewCluster(config.Hosts...)
	cluster.Keyspace = config.Keyspace
	cluster.Consistency = config.Consistency
	cluster.Timeout = config.Timeout

	session, err := cluster.CreateSession()
	if err != nil {
		return nil, fmt.Errorf("failed to create Cassandra session: %w", err)
	}

	store := &CassandraTimerStore{
		session: session,
	}

	return store, nil
}

// Close closes the Cassandra session
func (c *CassandraTimerStore) Close() error {
	if c.session != nil {
		c.session.Close()
	}
	return nil
}
func (c *CassandraTimerStore) ClaimShardOwnership(
	ctx context.Context, shardId int, ownerAddr string, metadata interface{},
) (shardVersion int64, retErr *databases.DbError) {
	// Convert ZeroUUID to high/low format for shard records
	zeroUuidHigh, zeroUuidLow := databases.UuidToHighLow(databases.ZeroUUID)

	// Serialize metadata to JSON
	var metadataJSON string
	if metadata != nil {
		metadataBytes, err := json.Marshal(metadata)
		if err != nil {
			return 0, databases.NewGenericDbError("failed to marshal metadata", err)
		}
		metadataJSON = string(metadataBytes)
	}

	now := time.Now().UTC()
	// When CAS fails, Cassandra returns the existing row values
	previous := make(map[string]interface{})

	// First, try to read the current shard record from unified timers table
	var currentVersion int64
	var currentOwnerAddr string
	query := `SELECT shard_version, shard_owner_addr FROM timers WHERE shard_id = ? AND row_type = ?`
	err := c.session.Query(query, shardId, databases.RowTypeShard).WithContext(ctx).Scan(&currentVersion, &currentOwnerAddr)

	if err != nil && !errors.Is(err, gocql.ErrNotFound) {
		return 0, databases.NewGenericDbError("failed to read shard record: %w", err)
	}

	if errors.Is(err, gocql.ErrNotFound) {
		// Shard doesn't exist, create it with version 1
		newVersion := int64(1)

		insertQuery := `INSERT INTO timers (shard_id, row_type, timer_execute_at, timer_uuid_high, timer_uuid_low, shard_version, shard_owner_addr, shard_claimed_at, shard_metadata) 
		                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) IF NOT EXISTS`

		applied, err := c.session.Query(insertQuery, shardId, databases.RowTypeShard, databases.ZeroTimestamp, zeroUuidHigh, zeroUuidLow, newVersion, ownerAddr, now, metadataJSON).
			WithContext(ctx).MapScanCAS(previous)

		if err != nil {
			return 0, databases.NewGenericDbError("failed to insert new shard record", err)
		}

		if !applied {
			// Another instance created the record concurrently, return conflict info
			conflictInfo := &databases.ShardInfo{
				ShardId:      int64(previous["shard_id"].(int)),
				OwnerAddr:    previous["shard_owner_addr"].(string),
				ClaimedAt:    previous["shard_claimed_at"].(time.Time),
				Metadata:     previous["shard_metadata"],
				ShardVersion: previous["shard_version"].(int64),
			}
			return 0, databases.NewDbErrorOnShardConditionFail("failed to insert shard record due to concurrent insert", nil, conflictInfo)
		} else {
			// Successfully created new record
			return newVersion, nil
		}
	}

	// Update the shard with new version and ownership using optimistic concurrency control
	newVersion := currentVersion + 1
	updateQuery := `UPDATE timers SET shard_version = ?, shard_owner_addr = ?, shard_claimed_at = ?, shard_metadata = ? 
	                WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid_high = ? AND timer_uuid_low = ? IF shard_version = ?`

	applied, err := c.session.Query(updateQuery, newVersion, ownerAddr, now, metadataJSON,
		shardId, databases.RowTypeShard, databases.ZeroTimestamp, zeroUuidHigh, zeroUuidLow, currentVersion).
		WithContext(ctx).MapScanCAS(previous)

	if err != nil {
		return 0, databases.NewGenericDbError("failed to update shard record", err)
	}

	if !applied {
		// Version changed concurrently, return conflict info
		// only version is available
		conflictInfo := &databases.ShardInfo{
			ShardVersion: previous["shard_version"].(int64),
		}
		return 0, databases.NewDbErrorOnShardConditionFail("shard ownership claim failed due to concurrent modification", nil, conflictInfo)
	}

	return newVersion, nil
}

func (c *CassandraTimerStore) CreateTimer(ctx context.Context, shardId int, shardVersion int64, namespace string, timer *databases.DbTimer) (err *databases.DbError) {
	// Convert the provided timer UUID to high/low format for predictable pagination
	timerUuidHigh, timerUuidLow := databases.UuidToHighLow(timer.TimerUuid)

	// Convert ZeroUUID to high/low format for shard records
	zeroUuidHigh, zeroUuidLow := databases.UuidToHighLow(databases.ZeroUUID)

	// Serialize payload and retry policy to JSON
	var payloadJSON, retryPolicyJSON string

	if timer.Payload != nil {
		payloadBytes, marshalErr := json.Marshal(timer.Payload)
		if marshalErr != nil {
			return databases.NewGenericDbError("failed to marshal timer payload", marshalErr)
		}
		payloadJSON = string(payloadBytes)
	}

	if timer.RetryPolicy != nil {
		retryPolicyBytes, marshalErr := json.Marshal(timer.RetryPolicy)
		if marshalErr != nil {
			return databases.NewGenericDbError("failed to marshal timer retry policy", marshalErr)
		}
		retryPolicyJSON = string(retryPolicyBytes)
	}

	// Create a batch with both shard version check and timer insertion
	batch := c.session.NewBatch(gocql.LoggedBatch).WithContext(ctx)

	// Add shard version check to batch - update shard version to same value to verify it matches
	checkVersionQuery := `UPDATE timers SET shard_version = ? WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid_high = ? AND timer_uuid_low = ? IF shard_version = ?`
	batch.Query(checkVersionQuery, shardVersion, shardId, databases.RowTypeShard, databases.ZeroTimestamp, zeroUuidHigh, zeroUuidLow, shardVersion)

	// Add timer insertion to batch
	insertQuery := `INSERT INTO timers (shard_id, row_type, timer_execute_at, timer_uuid_high, timer_uuid_low, timer_id, timer_namespace, timer_callback_url, timer_payload, timer_retry_policy, timer_callback_timeout_seconds, timer_created_at, timer_attempts) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	batch.Query(insertQuery,
		shardId,
		databases.RowTypeTimer,
		timer.ExecuteAt,
		timerUuidHigh,
		timerUuidLow,
		timer.Id,
		timer.Namespace,
		timer.CallbackUrl,
		payloadJSON,
		retryPolicyJSON,
		timer.CallbackTimeoutSeconds,
		timer.CreatedAt,
		0, // timer_attempts starts at 0
	)

	// Execute the batch atomically
	previous := make(map[string]interface{})
	applied, iter, batchErr := c.session.MapExecuteBatchCAS(batch, previous)
	if iter != nil {
		iter.Close()
	}

	if batchErr != nil {
		return databases.NewGenericDbError("failed to execute atomic timer creation batch", batchErr)
	}

	if !applied {
		// Batch failed - check if it was due to shard version mismatch or shard not existing
		var conflictShardVersion int64
		if shardVersionValue, exists := previous["shard_version"]; exists && shardVersionValue != nil {
			conflictShardVersion = shardVersionValue.(int64)
			conflictInfo := &databases.ShardInfo{
				ShardVersion: conflictShardVersion,
			}
			return databases.NewDbErrorOnShardConditionFail("shard version mismatch during timer creation", nil, conflictInfo)
		} else {
			// Shard doesn't exist
			return databases.NewGenericDbError("shard record does not exist", nil)
		}
	}

	return nil
}

func (c *CassandraTimerStore) CreateTimerNoLock(ctx context.Context, shardId int, namespace string, timer *databases.DbTimer) (err *databases.DbError) {
	// Convert the provided timer UUID to high/low format for predictable pagination
	timerUuidHigh, timerUuidLow := databases.UuidToHighLow(timer.TimerUuid)

	// Serialize payload and retry policy to JSON
	var payloadJSON, retryPolicyJSON string

	if timer.Payload != nil {
		payloadBytes, marshalErr := json.Marshal(timer.Payload)
		if marshalErr != nil {
			return databases.NewGenericDbError("failed to marshal timer payload", marshalErr)
		}
		payloadJSON = string(payloadBytes)
	}

	if timer.RetryPolicy != nil {
		retryPolicyBytes, marshalErr := json.Marshal(timer.RetryPolicy)
		if marshalErr != nil {
			return databases.NewGenericDbError("failed to marshal timer retry policy", marshalErr)
		}
		retryPolicyJSON = string(retryPolicyBytes)
	}

	// Insert the timer directly without any locking or version checking
	insertQuery := `INSERT INTO timers (shard_id, row_type, timer_execute_at, timer_uuid_high, timer_uuid_low, timer_id, timer_namespace, timer_callback_url, timer_payload, timer_retry_policy, timer_callback_timeout_seconds, timer_created_at, timer_attempts) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	insertErr := c.session.Query(insertQuery,
		shardId,
		databases.RowTypeTimer,
		timer.ExecuteAt,
		timerUuidHigh,
		timerUuidLow,
		timer.Id,
		timer.Namespace,
		timer.CallbackUrl,
		payloadJSON,
		retryPolicyJSON,
		timer.CallbackTimeoutSeconds,
		timer.CreatedAt,
		0, // timer_attempts starts at 0
	).WithContext(ctx).Exec()

	if insertErr != nil {
		return databases.NewGenericDbError("failed to insert timer", insertErr)
	}

	return nil
}

func (c *CassandraTimerStore) GetTimersUpToTimestamp(ctx context.Context, shardId int, request *databases.RangeGetTimersRequest) (*databases.RangeGetTimersResponse, *databases.DbError) {
	// Query timers up to the specified timestamp, ordered by execution time
	query := `SELECT shard_id, row_type, timer_execute_at, timer_uuid_high, timer_uuid_low,
	                 timer_id, timer_namespace, timer_callback_url, timer_payload, 
	                 timer_retry_policy, timer_callback_timeout_seconds, timer_created_at, timer_attempts
	          FROM timers 
	          WHERE shard_id = ? AND row_type = ? AND timer_execute_at <= ?
	          ORDER BY timer_execute_at ASC, timer_uuid_high ASC, timer_uuid_low ASC
	          LIMIT ?`

	iter := c.session.Query(query,
		shardId,
		databases.RowTypeTimer,
		request.UpToTimestamp,
		request.Limit).Iter()

	var timers []*databases.DbTimer
	var (
		dbShardId                  int
		dbRowType                  int16
		dbTimerExecuteAt           time.Time
		dbTimerUuidHigh            int64
		dbTimerUuidLow             int64
		dbTimerId                  string
		dbTimerNamespace           string
		dbTimerCallbackUrl         string
		dbTimerPayload             string
		dbTimerRetryPolicy         string
		dbTimerCallbackTimeoutSecs int32
		dbTimerCreatedAt           time.Time
		dbTimerAttempts            int32
	)

	for iter.Scan(&dbShardId, &dbRowType, &dbTimerExecuteAt, &dbTimerUuidHigh, &dbTimerUuidLow,
		&dbTimerId, &dbTimerNamespace, &dbTimerCallbackUrl, &dbTimerPayload,
		&dbTimerRetryPolicy, &dbTimerCallbackTimeoutSecs, &dbTimerCreatedAt, &dbTimerAttempts) {

		// Convert UUID high/low back to UUID
		timerUuid := databases.HighLowToUuid(dbTimerUuidHigh, dbTimerUuidLow)

		// Parse JSON payload and retry policy
		var payload interface{}
		var retryPolicy interface{}

		if dbTimerPayload != "" {
			if err := json.Unmarshal([]byte(dbTimerPayload), &payload); err != nil {
				return nil, databases.NewGenericDbError("failed to unmarshal timer payload", err)
			}
		}

		if dbTimerRetryPolicy != "" {
			if err := json.Unmarshal([]byte(dbTimerRetryPolicy), &retryPolicy); err != nil {
				return nil, databases.NewGenericDbError("failed to unmarshal timer retry policy", err)
			}
		}

		timer := &databases.DbTimer{
			Id:                     dbTimerId,
			TimerUuid:              timerUuid,
			Namespace:              dbTimerNamespace,
			ExecuteAt:              dbTimerExecuteAt,
			CallbackUrl:            dbTimerCallbackUrl,
			Payload:                payload,
			RetryPolicy:            retryPolicy,
			CallbackTimeoutSeconds: dbTimerCallbackTimeoutSecs,
			CreatedAt:              dbTimerCreatedAt,
		}

		timers = append(timers, timer)
	}

	if err := iter.Close(); err != nil {
		return nil, databases.NewGenericDbError("failed to query timers", err)
	}

	return &databases.RangeGetTimersResponse{
		Timers: timers,
	}, nil
}

func (c *CassandraTimerStore) DeleteTimersUpToTimestampWithBatchInsert(ctx context.Context, shardId int, shardVersion int64, request *databases.RangeDeleteTimersRequest, TimersToInsert []*databases.DbTimer) (*databases.RangeDeleteTimersResponse, *databases.DbError) {
	// Convert ZeroUUID to high/low format for shard records
	zeroUuidHigh, zeroUuidLow := databases.UuidToHighLow(databases.ZeroUUID)

	// Convert start and end UUIDs to high/low format for precise range selection
	startUuidHigh, startUuidLow := databases.UuidToHighLow(request.StartTimeUuid)
	endUuidHigh, endUuidLow := databases.UuidToHighLow(request.EndTimeUuid)

	// Create a batch with LWT for atomic operation
	batch := c.session.NewBatch(gocql.LoggedBatch).WithContext(ctx)

	// Add shard version check to batch - update shard version to same value to verify it matches
	checkVersionQuery := `UPDATE timers SET shard_version = ? WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid_high = ? AND timer_uuid_low = ? IF shard_version = ?`
	batch.Query(checkVersionQuery, shardVersion, shardId, databases.RowTypeShard, databases.ZeroTimestamp, zeroUuidHigh, zeroUuidLow, shardVersion)

	// Add range DELETE statement for timers in the specified range
	deleteQuery := `DELETE FROM timers WHERE shard_id = ? AND row_type = ? 
	                AND (timer_execute_at, timer_uuid_high, timer_uuid_low) >= (?, ?, ?)
	                AND (timer_execute_at, timer_uuid_high, timer_uuid_low) <= (?, ?, ?)`
	batch.Query(deleteQuery, shardId, databases.RowTypeTimer,
		request.StartTimestamp, startUuidHigh, startUuidLow,
		request.EndTimestamp, endUuidHigh, endUuidLow)

	// Add INSERT statements for new timers
	insertQuery := `INSERT INTO timers (shard_id, row_type, timer_execute_at, timer_uuid_high, timer_uuid_low, timer_id, timer_namespace, timer_callback_url, timer_payload, timer_retry_policy, timer_callback_timeout_seconds, timer_created_at, timer_attempts) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	for _, timer := range TimersToInsert {
		// Convert the timer UUID to high/low format
		timerUuidHigh, timerUuidLow := databases.UuidToHighLow(timer.TimerUuid)

		// Serialize payload and retry policy to JSON
		var payloadJSON, retryPolicyJSON string

		if timer.Payload != nil {
			payloadBytes, marshalErr := json.Marshal(timer.Payload)
			if marshalErr != nil {
				return nil, databases.NewGenericDbError("failed to marshal timer payload", marshalErr)
			}
			payloadJSON = string(payloadBytes)
		}

		if timer.RetryPolicy != nil {
			retryPolicyBytes, marshalErr := json.Marshal(timer.RetryPolicy)
			if marshalErr != nil {
				return nil, databases.NewGenericDbError("failed to marshal timer retry policy", marshalErr)
			}
			retryPolicyJSON = string(retryPolicyBytes)
		}

		batch.Query(insertQuery,
			shardId,
			databases.RowTypeTimer,
			timer.ExecuteAt,
			timerUuidHigh,
			timerUuidLow,
			timer.Id,
			timer.Namespace,
			timer.CallbackUrl,
			payloadJSON,
			retryPolicyJSON,
			timer.CallbackTimeoutSeconds,
			timer.CreatedAt,
			0, // timer_attempts starts at 0
		)
	}

	// Execute the batch atomically
	previous := make(map[string]interface{})
	applied, iter, batchErr := c.session.MapExecuteBatchCAS(batch, previous)
	if iter != nil {
		iter.Close()
	}

	if batchErr != nil {
		return nil, databases.NewGenericDbError("failed to execute atomic delete and insert batch", batchErr)
	}

	if !applied {
		// Batch failed - check if it was due to shard version mismatch or shard not existing
		var conflictShardVersion int64
		if shardVersionValue, exists := previous["shard_version"]; exists && shardVersionValue != nil {
			conflictShardVersion = shardVersionValue.(int64)
			conflictInfo := &databases.ShardInfo{
				ShardVersion: conflictShardVersion,
			}
			return nil, databases.NewDbErrorOnShardConditionFail("shard version mismatch during delete and insert operation", nil, conflictInfo)
		} else {
			// Shard doesn't exist
			return nil, databases.NewGenericDbError("shard record does not exist", nil)
		}
	}

	return &databases.RangeDeleteTimersResponse{
		DeletedCount: 0, // Count not available for range deletes in batch operations
	}, nil
}

func (c *CassandraTimerStore) BatchInsertTimers(ctx context.Context, shardId int, shardVersion int64, TimersToInsert []*databases.DbTimer) *databases.DbError {
	//TODO implement me
	panic("implement me")
}

func (c *CassandraTimerStore) UpdateTimer(ctx context.Context, shardId int, shardVersion int64, namespace string, request *databases.UpdateDbTimerRequest) (err *databases.DbError) {
	//TODO implement me
	panic("implement me")
}

func (c *CassandraTimerStore) GetTimer(ctx context.Context, shardId int, namespace string, timerId string) (timer *databases.DbTimer, err *databases.DbError) {
	//TODO implement me
	panic("implement me")
}

func (c *CassandraTimerStore) DeleteTimer(ctx context.Context, shardId int, shardVersion int64, namespace string, timerId string) *databases.DbError {
	//TODO implement me
	panic("implement me")
}
