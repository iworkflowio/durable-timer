package cassandra

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
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
	ctx context.Context,
	shardId int,
	ownerAddr string,
) (prevShardInfo, currentShardInfo *databases.ShardInfo, err *databases.DbError) {
	// Use ZeroUUID for shard records - convert to gocql.UUID for Cassandra
	zeroUuid, _ := gocql.UUIDFromBytes(databases.ZeroUUID[:])

	now := time.Now().UTC()
	// When CAS fails, Cassandra returns the existing row values
	previous := make(map[string]interface{})

	// First, try to read the current shard record from unified timers table
	var currentVersion int64
	var currentOwnerAddr string
	var currentClaimedAt time.Time
	var currentMetadataJSON string

	query := `SELECT shard_version, shard_owner_addr, shard_claimed_at, shard_metadata FROM timers WHERE shard_id = ? AND row_type = ?`
	err2 := c.session.Query(query, shardId, databases.RowTypeShard).WithContext(ctx).Scan(&currentVersion, &currentOwnerAddr, &currentClaimedAt, &currentMetadataJSON)

	if err2 != nil && !errors.Is(err2, gocql.ErrNotFound) {
		return nil, nil, databases.NewGenericDbError("failed to read shard record", err2)
	}

	if errors.Is(err2, gocql.ErrNotFound) {
		// Shard doesn't exist, create it with version 1
		newVersion := int64(1)

		// Initialize empty metadata
		defaultMetadata := databases.ShardMetadata{}
		metadataJSON, marshalErr := json.Marshal(defaultMetadata)
		if marshalErr != nil {
			return nil, nil, databases.NewGenericDbError("failed to marshal default metadata", marshalErr)
		}

		insertQuery := `INSERT INTO timers (shard_id, row_type, timer_execute_at, timer_uuid, shard_version, shard_owner_addr, shard_claimed_at, shard_metadata) 
		                VALUES (?, ?, ?, ?, ?, ?, ?, ?) IF NOT EXISTS`

		applied, insertErr := c.session.Query(insertQuery, shardId, databases.RowTypeShard, databases.ZeroTimestamp, zeroUuid, newVersion, ownerAddr, now, string(metadataJSON)).
			WithContext(ctx).MapScanCAS(previous)

		if insertErr != nil {
			return nil, nil, databases.NewGenericDbError("failed to insert new shard record", insertErr)
		}

		if !applied {
			// Another instance created the record concurrently
			return nil, nil, databases.NewDbErrorOnShardConditionFail("failed to insert shard record due to concurrent insert", nil)
		}

		// Successfully created new record, return nil for prevShardInfo and current info
		currentShardInfo = &databases.ShardInfo{
			ShardId:      int64(shardId),
			OwnerAddr:    ownerAddr,
			ShardVersion: newVersion,
			Metadata:     defaultMetadata,
			ClaimedAt:    now,
		}
		return nil, currentShardInfo, nil
	}

	// Parse current metadata
	var currentMetadata databases.ShardMetadata
	if currentMetadataJSON != "" {
		if parseErr := json.Unmarshal([]byte(currentMetadataJSON), &currentMetadata); parseErr != nil {
			return nil, nil, databases.NewGenericDbError("failed to unmarshal shard metadata", parseErr)
		}
	}

	// Store previous shard info
	prevShardInfo = &databases.ShardInfo{
		ShardId:      int64(shardId),
		OwnerAddr:    currentOwnerAddr,
		ShardVersion: currentVersion,
		Metadata:     currentMetadata,
		ClaimedAt:    currentClaimedAt,
	}

	// Update the shard with new version and ownership using optimistic concurrency control
	newVersion := currentVersion + 1

	updateQuery := `UPDATE timers SET shard_version = ?, shard_owner_addr = ?, shard_claimed_at = ?
	                WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid = ? IF shard_version = ?`

	applied, updateErr := c.session.Query(updateQuery, newVersion, ownerAddr, now,
		shardId, databases.RowTypeShard, databases.ZeroTimestamp, zeroUuid, currentVersion).
		WithContext(ctx).MapScanCAS(previous)

	if updateErr != nil {
		return nil, nil, databases.NewGenericDbError("failed to update shard record", updateErr)
	}

	if !applied {
		// Version changed concurrently
		return nil, nil, databases.NewDbErrorOnShardConditionFail("shard ownership claim failed due to concurrent modification", nil)
	}

	// Successfully updated, return both previous and current shard info
	currentShardInfo = &databases.ShardInfo{
		ShardId:      int64(shardId),
		OwnerAddr:    ownerAddr,
		ShardVersion: newVersion,
		Metadata:     currentMetadata,
		ClaimedAt:    now,
	}

	return prevShardInfo, currentShardInfo, nil
}

func (c *CassandraTimerStore) UpdateShardMetadata(
	ctx context.Context,
	shardId int, shardVersion int64,
	metadata databases.ShardMetadata,
) (err *databases.DbError) {
	// Use ZeroUUID for shard records - convert to gocql.UUID for Cassandra
	zeroUuid, _ := gocql.UUIDFromBytes(databases.ZeroUUID[:])

	// Marshal metadata to JSON
	metadataJSON, marshalErr := json.Marshal(metadata)
	if marshalErr != nil {
		return databases.NewGenericDbError("failed to marshal metadata", marshalErr)
	}

	// Use LWT to update shard metadata only if the shard version matches
	updateQuery := `UPDATE timers SET shard_metadata = ? WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid = ? IF shard_version = ?`

	// Create a logged batch for the LWT operation
	batch := c.session.NewBatch(gocql.LoggedBatch).WithContext(ctx)
	batch.Query(updateQuery, string(metadataJSON), shardId, databases.RowTypeShard, databases.ZeroTimestamp, zeroUuid, shardVersion)

	// When CAS fails, Cassandra returns the existing row values
	previous := make(map[string]interface{})
	applied, iter, lwt_err := c.session.MapExecuteBatchCAS(batch, previous)
	if iter != nil {
		iter.Close()
	}

	if lwt_err != nil {
		return databases.NewGenericDbError("failed to update shard metadata", lwt_err)
	}

	if !applied {
		// Version mismatch during metadata update
		return databases.NewDbErrorOnShardConditionFail("shard version mismatch during metadata update", nil)
	}

	return nil
}

func (c *CassandraTimerStore) CreateTimer(ctx context.Context, shardId int, shardVersion int64, namespace string, timer *databases.DbTimer) (err *databases.DbError) {
	// Use the provided timer UUID - convert to gocql.UUID for Cassandra
	timerUuid, _ := gocql.UUIDFromBytes(timer.TimerUuid[:])

	// Use ZeroUUID for shard records - convert to gocql.UUID for Cassandra
	zeroUuid, _ := gocql.UUIDFromBytes(databases.ZeroUUID[:])

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
	checkVersionQuery := `UPDATE timers SET shard_version = ? WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid = ? IF shard_version = ?`
	batch.Query(checkVersionQuery, shardVersion, shardId, databases.RowTypeShard, databases.ZeroTimestamp, zeroUuid, shardVersion)

	// Add timer insertion to batch
	insertQuery := `INSERT INTO timers (shard_id, row_type, timer_execute_at, timer_uuid, timer_id, timer_namespace, timer_callback_url, timer_payload, timer_retry_policy, timer_callback_timeout_seconds, timer_created_at, timer_attempts) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	batch.Query(insertQuery,
		shardId,
		databases.RowTypeTimer,
		timer.ExecuteAt,
		timerUuid,
		timer.Id,
		timer.Namespace,
		timer.CallbackUrl,
		payloadJSON,
		retryPolicyJSON,
		timer.CallbackTimeoutSeconds,
		timer.CreatedAt,
		timer.Attempts, // Use the attempts value from the timer
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
		if shardVersionValue, exists := previous["shard_version"]; exists && shardVersionValue != nil {
			return databases.NewDbErrorOnShardConditionFail("shard version mismatch during timer creation", nil)
		} else {
			// Shard doesn't exist
			return databases.NewGenericDbError("shard record does not exist", nil)
		}
	}

	return nil
}

func (c *CassandraTimerStore) CreateTimerNoLock(ctx context.Context, shardId int, namespace string, timer *databases.DbTimer) (err *databases.DbError) {
	// Use the provided timer UUID - convert to gocql.UUID for Cassandra
	timerUuid, _ := gocql.UUIDFromBytes(timer.TimerUuid[:])

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
	insertQuery := `INSERT INTO timers (shard_id, row_type, timer_execute_at, timer_uuid, timer_id, timer_namespace, timer_callback_url, timer_payload, timer_retry_policy, timer_callback_timeout_seconds, timer_created_at, timer_attempts) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	insertErr := c.session.Query(insertQuery,
		shardId,
		databases.RowTypeTimer,
		timer.ExecuteAt,
		timerUuid,
		timer.Id,
		timer.Namespace,
		timer.CallbackUrl,
		payloadJSON,
		retryPolicyJSON,
		timer.CallbackTimeoutSeconds,
		timer.CreatedAt,
		timer.Attempts, // Use the attempts value from the timer
	).WithContext(ctx).Exec()

	if insertErr != nil {
		return databases.NewGenericDbError("failed to insert timer", insertErr)
	}

	return nil
}

func (c *CassandraTimerStore) RangeGetTimers(ctx context.Context, shardId int, request *databases.RangeGetTimersRequest) (*databases.RangeGetTimersResponse, *databases.DbError) {
	// Use start and end UUIDs - convert to gocql.UUID for Cassandra
	startUuid, _ := gocql.UUIDFromBytes(request.StartTimeUuid[:])
	endUuid, _ := gocql.UUIDFromBytes(request.EndTimeUuid[:])

	// Query timers in the specified range, ordered by execution time and UUID
	query := `SELECT shard_id, row_type, timer_execute_at, timer_uuid,
	                 timer_id, timer_namespace, timer_callback_url, timer_payload, 
	                 timer_retry_policy, timer_callback_timeout_seconds, timer_created_at, timer_attempts
	          FROM timers 
	          WHERE shard_id = ? AND row_type = ? 
	            AND (timer_execute_at, timer_uuid) >= (?, ?)
	            AND (timer_execute_at, timer_uuid) <= (?, ?)
	          ORDER BY timer_execute_at ASC, timer_uuid ASC
	          LIMIT ?`

	iter := c.session.Query(query,
		shardId,
		databases.RowTypeTimer,
		request.StartTimestamp, startUuid,
		request.EndTimestamp, endUuid,
		request.Limit).Iter()

	var timers []*databases.DbTimer
	var (
		dbShardId                  int
		dbRowType                  int16
		dbTimerExecuteAt           time.Time
		dbTimerUuid                gocql.UUID
		dbTimerId                  string
		dbTimerNamespace           string
		dbTimerCallbackUrl         string
		dbTimerPayload             string
		dbTimerRetryPolicy         string
		dbTimerCallbackTimeoutSecs int32
		dbTimerCreatedAt           time.Time
		dbTimerAttempts            int32
	)

	for iter.Scan(&dbShardId, &dbRowType, &dbTimerExecuteAt, &dbTimerUuid,
		&dbTimerId, &dbTimerNamespace, &dbTimerCallbackUrl, &dbTimerPayload,
		&dbTimerRetryPolicy, &dbTimerCallbackTimeoutSecs, &dbTimerCreatedAt, &dbTimerAttempts) {

		// Convert gocql.UUID to uuid.UUID for consistency
		timerUuid, _ := uuid.FromBytes(dbTimerUuid.Bytes())

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
			Attempts:               dbTimerAttempts,
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

func (c *CassandraTimerStore) RangeDeleteWithBatchInsertTxn(ctx context.Context, shardId int, shardVersion int64, request *databases.RangeDeleteTimersRequest, TimersToInsert []*databases.DbTimer) (*databases.RangeDeleteTimersResponse, *databases.DbError) {
	// Use ZeroUUID for shard records - convert to gocql.UUID for Cassandra
	zeroUuid, _ := gocql.UUIDFromBytes(databases.ZeroUUID[:])

	// Use start and end UUIDs - convert to gocql.UUID for Cassandra
	startUuid, _ := gocql.UUIDFromBytes(request.StartTimeUuid[:])
	endUuid, _ := gocql.UUIDFromBytes(request.EndTimeUuid[:])

	// Create a batch with LWT for atomic operation
	batch := c.session.NewBatch(gocql.LoggedBatch).WithContext(ctx)

	// Add shard version check to batch - update shard version to same value to verify it matches
	checkVersionQuery := `UPDATE timers SET shard_version = ? WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid = ? IF shard_version = ?`
	batch.Query(checkVersionQuery, shardVersion, shardId, databases.RowTypeShard, databases.ZeroTimestamp, zeroUuid, shardVersion)

	// Add range DELETE statement for timers in the specified range
	deleteQuery := `DELETE FROM timers WHERE shard_id = ? AND row_type = ? 
	                AND (timer_execute_at, timer_uuid) >= (?, ?)
	                AND (timer_execute_at, timer_uuid) <= (?, ?)`
	batch.Query(deleteQuery, shardId, databases.RowTypeTimer,
		request.StartTimestamp, startUuid,
		request.EndTimestamp, endUuid)

	// Add INSERT statements for new timers
	insertQuery := `INSERT INTO timers (shard_id, row_type, timer_execute_at, timer_uuid, timer_id, timer_namespace, timer_callback_url, timer_payload, timer_retry_policy, timer_callback_timeout_seconds, timer_created_at, timer_attempts) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	for _, timer := range TimersToInsert {
		// Use the timer UUID - convert to gocql.UUID for Cassandra
		timerUuid, _ := gocql.UUIDFromBytes(timer.TimerUuid[:])

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
			timerUuid,
			timer.Id,
			timer.Namespace,
			timer.CallbackUrl,
			payloadJSON,
			retryPolicyJSON,
			timer.CallbackTimeoutSeconds,
			timer.CreatedAt,
			timer.Attempts, // Use the actual attempts from the timer
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
		if shardVersionValue, exists := previous["shard_version"]; exists && shardVersionValue != nil {
			return nil, databases.NewDbErrorOnShardConditionFail("shard version mismatch during delete and insert operation", nil)
		} else {
			// Shard doesn't exist
			return nil, databases.NewGenericDbError("shard record does not exist", nil)
		}
	}

	return &databases.RangeDeleteTimersResponse{
		DeletedCount: 0, // Count not available for range deletes in batch operations
	}, nil
}

func (c *CassandraTimerStore) RangeDeleteWithLimit(ctx context.Context, shardId int, request *databases.RangeDeleteTimersRequest, limit int) (*databases.RangeDeleteTimersResponse, *databases.DbError) {
	// Note: Cassandra doesn't support LIMIT in DELETE statements, so limit parameter is ignored
	startUuid, _ := gocql.UUIDFromBytes(request.StartTimeUuid[:])
	endUuid, _ := gocql.UUIDFromBytes(request.EndTimeUuid[:])

	deleteQuery := `DELETE FROM timers WHERE shard_id = ? AND row_type = ? 
	                AND (timer_execute_at, timer_uuid) >= (?, ?)
	                AND (timer_execute_at, timer_uuid) <= (?, ?)`

	err := c.session.Query(deleteQuery,
		shardId, databases.RowTypeTimer,
		request.StartTimestamp, startUuid,
		request.EndTimestamp, endUuid,
	).WithContext(ctx).Exec()

	if err != nil {
		return nil, databases.NewGenericDbError("failed to execute range delete", err)
	}

	return &databases.RangeDeleteTimersResponse{DeletedCount: 0}, nil
}

func (c *CassandraTimerStore) UpdateTimer(ctx context.Context, shardId int, shardVersion int64, namespace string, request *databases.UpdateDbTimerRequest) (err *databases.DbError) {
	// Use ZeroUUID for shard records - convert to gocql.UUID for Cassandra
	zeroUuid, _ := gocql.UUIDFromBytes(databases.ZeroUUID[:])

	// Serialize new payload and retry policy to JSON
	var payloadJSON, retryPolicyJSON string

	if request.Payload != nil {
		payloadBytes, marshalErr := json.Marshal(request.Payload)
		if marshalErr != nil {
			return databases.NewGenericDbError("failed to marshal timer payload", marshalErr)
		}
		payloadJSON = string(payloadBytes)
	}

	if request.RetryPolicy != nil {
		retryPolicyBytes, marshalErr := json.Marshal(request.RetryPolicy)
		if marshalErr != nil {
			return databases.NewGenericDbError("failed to marshal timer retry policy", marshalErr)
		}
		retryPolicyJSON = string(retryPolicyBytes)
	}

	// For Cassandra, we need to first lookup the timer's primary key components outside the LWT batch
	// Use the secondary index on timer_id and filter results in application code
	// This lookup combines partition key (shard_id) with secondary index (timer_id)
	// Most efficient query pattern - partition key + secondary index
	lookupQuery := `SELECT shard_id, row_type, timer_execute_at, timer_uuid, timer_created_at, timer_namespace FROM timers 
	                WHERE shard_id = ? AND timer_id = ?`

	// Perform lookup outside the LWT batch due to Cassandra limitations with secondary indexes in LWT
	iter := c.session.Query(lookupQuery, shardId, request.TimerId).
		WithContext(ctx).
		Iter()

	var found bool

	// Scan through results and find the one matching our shard, row type, and namespace
	var resultShardId int
	var resultRowType int16
	var resultExecuteAt, resultCreatedAt time.Time
	var resultUuid gocql.UUID
	var resultNamespace string

	for iter.Scan(&resultShardId, &resultRowType, &resultExecuteAt, &resultUuid, &resultCreatedAt, &resultNamespace) {
		if resultShardId == shardId && resultRowType == databases.RowTypeTimer && resultNamespace == namespace {
			found = true
			break
		}
	}

	if err2 := iter.Close(); err2 != nil {
		return databases.NewGenericDbError("failed to close lookup iterator", err2)
	}

	if !found {
		return databases.NewDbErrorNotExists("timer not found for update", nil)
	}

	// Create a batch with LWT for atomic operation
	batch := c.session.NewBatch(gocql.LoggedBatch).WithContext(ctx)

	// Add shard version check to batch - update shard version to same value to verify it matches
	checkVersionQuery := `UPDATE timers SET shard_version = ? WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid = ? IF shard_version = ?`
	batch.Query(checkVersionQuery, shardVersion, shardId, databases.RowTypeShard, databases.ZeroTimestamp, zeroUuid, shardVersion)

	if request.ExecuteAt.Equal(resultExecuteAt) {
		// Same execution time - can do direct UPDATE using existing primary key
		// Add IF EXISTS to detect if timer was deleted between lookup and LWT
		updateQuery := `UPDATE timers SET timer_callback_url = ?, timer_payload = ?, timer_retry_policy = ?, timer_callback_timeout_seconds = ?, timer_attempts = ? 
		                WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid = ? IF EXISTS`
		batch.Query(updateQuery,
			request.CallbackUrl,
			payloadJSON,
			retryPolicyJSON,
			request.CallbackTimeoutSeconds,
			0, // Reset attempts on update
			shardId,
			databases.RowTypeTimer,
			resultExecuteAt,
			resultUuid,
		)
	} else {
		// Different execution time - need delete+insert because timer_execute_at is part of primary key
		// Add IF EXISTS to detect if timer was deleted between lookup and LWT
		deleteQuery := `DELETE FROM timers WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid = ? IF EXISTS`
		batch.Query(deleteQuery, shardId, databases.RowTypeTimer, resultExecuteAt, resultUuid)

		// timerUUID is the same as the current timerUUID because the timerID is the same

		insertQuery := `INSERT INTO timers (shard_id, row_type, timer_execute_at, timer_uuid, timer_id, timer_namespace, timer_callback_url, timer_payload, timer_retry_policy, timer_callback_timeout_seconds, timer_created_at, timer_attempts) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
		batch.Query(insertQuery,
			shardId,
			databases.RowTypeTimer,
			request.ExecuteAt,
			resultUuid,
			request.TimerId,
			namespace,
			request.CallbackUrl,
			payloadJSON,
			retryPolicyJSON,
			request.CallbackTimeoutSeconds,
			resultCreatedAt, // Keep original creation time
			0,               // Reset attempts on update
		)
	}

	// Execute the batch atomically
	previous := make(map[string]interface{})
	applied, iter, batchErr := c.session.MapExecuteBatchCAS(batch, previous)
	if iter != nil {
		iter.Close()
	}

	if batchErr != nil {
		return databases.NewGenericDbError("failed to execute atomic update batch", batchErr)
	}

	if !applied {
		// Batch failed - check the specific reason by examining the previous map
		// The batch contains: 1) shard version check, 2) timer update/delete with IF EXISTS

		// Check if shard row exists in the response (indicates shard version check worked)
		if shardVersionValue, exists := previous["shard_version"]; exists {
			if shardVersionValue != nil {
				// Shard exists - check if version matches
				actualShardVersion := shardVersionValue.(int64)
				if actualShardVersion == shardVersion {
					// Shard version matches - the only possible failure is timer doesn't exist
					return databases.NewDbErrorNotExists("timer not found", nil)
				} else {
					// Shard version mismatch
					return databases.NewDbErrorOnShardConditionFail("shard version mismatch during update operation", nil)
				}
			} else {
				// Shard version field exists but is null - unexpected case
				return databases.NewGenericDbError("unexpected shard version state during update", nil)
			}
		} else {
			// No shard version in response - shard doesn't exist
			return databases.NewGenericDbError("shard record does not exist", nil)
		}
	}

	return nil
}

func (c *CassandraTimerStore) GetTimer(ctx context.Context, shardId int, namespace string, timerId string) (timer *databases.DbTimer, err *databases.DbError) {
	// Query to get the timer by namespace and timer ID
	query := `SELECT shard_id, row_type, timer_execute_at, timer_uuid,
	                 timer_id, timer_namespace, timer_callback_url, timer_payload, 
	                 timer_retry_policy, timer_callback_timeout_seconds, timer_created_at, timer_attempts
	          FROM timers 
	          WHERE shard_id = ? AND row_type = ? AND timer_namespace = ? AND timer_id = ?
	          ALLOW FILTERING`

	var (
		dbShardId                  int
		dbRowType                  int16
		dbTimerExecuteAt           time.Time
		dbTimerUuid                gocql.UUID
		dbTimerId                  string
		dbTimerNamespace           string
		dbTimerCallbackUrl         string
		dbTimerPayload             string
		dbTimerRetryPolicy         string
		dbTimerCallbackTimeoutSecs int32
		dbTimerCreatedAt           time.Time
		dbTimerAttempts            int32
	)

	err2 := c.session.Query(query, shardId, databases.RowTypeTimer, namespace, timerId).
		WithContext(ctx).
		Scan(&dbShardId, &dbRowType, &dbTimerExecuteAt, &dbTimerUuid,
			&dbTimerId, &dbTimerNamespace, &dbTimerCallbackUrl, &dbTimerPayload,
			&dbTimerRetryPolicy, &dbTimerCallbackTimeoutSecs, &dbTimerCreatedAt, &dbTimerAttempts)

	if err2 != nil {
		if err2 == gocql.ErrNotFound {
			return nil, databases.NewDbErrorNotExists("timer not found", err2)
		}
		return nil, databases.NewGenericDbError("failed to query timer", err2)
	}

	// Convert gocql.UUID to uuid.UUID for consistency
	timerUuid, _ := uuid.FromBytes(dbTimerUuid.Bytes())

	// Parse JSON payload and retry policy
	var payload interface{}
	var retryPolicy interface{}

	if dbTimerPayload != "" {
		if unmarshalErr := json.Unmarshal([]byte(dbTimerPayload), &payload); unmarshalErr != nil {
			return nil, databases.NewGenericDbError("failed to unmarshal timer payload", unmarshalErr)
		}
	}

	if dbTimerRetryPolicy != "" {
		if unmarshalErr := json.Unmarshal([]byte(dbTimerRetryPolicy), &retryPolicy); unmarshalErr != nil {
			return nil, databases.NewGenericDbError("failed to unmarshal timer retry policy", unmarshalErr)
		}
	}

	timer = &databases.DbTimer{
		Id:                     dbTimerId,
		TimerUuid:              timerUuid,
		Namespace:              dbTimerNamespace,
		ExecuteAt:              dbTimerExecuteAt,
		CallbackUrl:            dbTimerCallbackUrl,
		Payload:                payload,
		RetryPolicy:            retryPolicy,
		CallbackTimeoutSeconds: dbTimerCallbackTimeoutSecs,
		CreatedAt:              dbTimerCreatedAt,
		Attempts:               dbTimerAttempts,
	}

	return timer, nil
}

func (c *CassandraTimerStore) DeleteTimer(ctx context.Context, shardId int, shardVersion int64, namespace string, timerId string) *databases.DbError {
	// Use ZeroUUID for shard records - convert to gocql.UUID for Cassandra
	zeroUuid, _ := gocql.UUIDFromBytes(databases.ZeroUUID[:])

	// First, find the timer's primary key components (execute_at, uuid)
	// This lookup is done outside the LWT batch due to Cassandra limitations with secondary indexes in LWT
	lookupQuery := `SELECT shard_id, row_type, timer_execute_at, timer_uuid, timer_namespace FROM timers 
	                WHERE shard_id = ? AND timer_id = ?`

	iter := c.session.Query(lookupQuery, shardId, timerId).
		WithContext(ctx).
		Iter()

	// Scan through results and find the one matching our shard, row type, and namespace
	var resultShardId int
	var resultRowType int16
	var resultExecuteAt time.Time
	var resultUuid gocql.UUID
	var resultNamespace string
	var found bool

	for iter.Scan(&resultShardId, &resultRowType, &resultExecuteAt, &resultUuid, &resultNamespace) {
		if resultShardId == shardId && resultRowType == databases.RowTypeTimer && resultNamespace == namespace {
			found = true
			break
		}
	}

	if err := iter.Close(); err != nil {
		return databases.NewGenericDbError("failed to close lookup iterator", err)
	}

	if !found {
		// Timer doesn't exist - return NotExists error
		return databases.NewDbErrorNotExists("timer not found", nil)
	}

	// Create a batch with LWT for atomic operation
	batch := c.session.NewBatch(gocql.LoggedBatch).WithContext(ctx)

	// Add shard version check to batch - update shard version to same value to verify it matches
	checkVersionQuery := `UPDATE timers SET shard_version = ? WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid = ? IF shard_version = ?`
	batch.Query(checkVersionQuery, shardVersion, shardId, databases.RowTypeShard, databases.ZeroTimestamp, zeroUuid, shardVersion)

	// Add DELETE statement for the specific timer using primary key with IF EXISTS to detect race conditions
	deleteQuery := `DELETE FROM timers WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid = ? IF EXISTS`
	batch.Query(deleteQuery, shardId, databases.RowTypeTimer, resultExecuteAt, resultUuid)

	// Execute the batch atomically
	previous := make(map[string]interface{})
	applied, iter, batchErr := c.session.MapExecuteBatchCAS(batch, previous)
	if iter != nil {
		iter.Close()
	}

	if batchErr != nil {
		return databases.NewGenericDbError("failed to execute atomic delete batch", batchErr)
	}

	if !applied {
		// Batch failed - check the specific reason by examining the previous map
		// The batch contains: 1) shard version check, 2) timer delete with IF EXISTS

		// Check if shard row exists in the response (indicates shard version check worked)
		if shardVersionValue, exists := previous["shard_version"]; exists {
			if shardVersionValue != nil {
				// Shard exists - check if version matches
				actualShardVersion := shardVersionValue.(int64)
				if actualShardVersion == shardVersion {
					// Shard version matches - the only possible failure is timer doesn't exist
					return databases.NewDbErrorNotExists("timer not found", nil)
				} else {
					// Shard version mismatch
					return databases.NewDbErrorOnShardConditionFail("shard version mismatch during delete operation", nil)
				}
			} else {
				// Shard version field exists but is null - unexpected case
				return databases.NewGenericDbError("unexpected shard version state during delete", nil)
			}
		} else {
			// No shard version in response - shard doesn't exist
			return databases.NewGenericDbError("shard record does not exist", nil)
		}
	}

	return nil
}

func (c *CassandraTimerStore) UpdateTimerNoLock(ctx context.Context, shardId int, namespace string, request *databases.UpdateDbTimerRequest) (err *databases.DbError) {
	// Serialize new payload and retry policy to JSON
	var payloadJSON, retryPolicyJSON interface{}

	if request.Payload != nil {
		payloadBytes, marshalErr := json.Marshal(request.Payload)
		if marshalErr != nil {
			return databases.NewGenericDbError("failed to marshal timer payload", marshalErr)
		}
		payloadJSON = string(payloadBytes)
	}

	if request.RetryPolicy != nil {
		retryPolicyBytes, marshalErr := json.Marshal(request.RetryPolicy)
		if marshalErr != nil {
			return databases.NewGenericDbError("failed to marshal timer retry policy", marshalErr)
		}
		retryPolicyJSON = string(retryPolicyBytes)
	}

	// Lookup the timer's primary key components (without shard version check)
	// Use the secondary index on timer_id and filter results in application code
	lookupQuery := `SELECT shard_id, row_type, timer_execute_at, timer_uuid, timer_created_at, timer_namespace FROM timers 
	                WHERE shard_id = ? AND timer_id = ?`

	iter := c.session.Query(lookupQuery, shardId, request.TimerId).
		WithContext(ctx).
		Iter()

	var found bool

	// Scan through results and find the one matching our shard, row type, and namespace
	var resultShardId int
	var resultRowType int16
	var resultExecuteAt, resultCreatedAt time.Time
	var resultUuid gocql.UUID
	var resultNamespace string

	for iter.Scan(&resultShardId, &resultRowType, &resultExecuteAt, &resultUuid, &resultCreatedAt, &resultNamespace) {
		if resultShardId == shardId && resultRowType == databases.RowTypeTimer && resultNamespace == namespace {
			found = true
			break
		}
	}

	if err2 := iter.Close(); err2 != nil {
		return databases.NewGenericDbError("failed to close lookup iterator", err2)
	}

	if !found {
		return databases.NewDbErrorNotExists("timer not found for update", nil)
	}

	// Compare times with tolerance for Cassandra precision loss (nanoseconds -> microseconds)
	timeDiff := request.ExecuteAt.Sub(resultExecuteAt)
	if timeDiff >= -time.Millisecond && timeDiff <= time.Millisecond {
		// Same execution time - can do direct UPDATE using existing primary key
		// No locking, so no shard version check, just update if timer exists
		updateQuery := `UPDATE timers SET timer_callback_url = ?, timer_payload = ?, timer_retry_policy = ?, timer_callback_timeout_seconds = ?, timer_attempts = ? 
		                WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid = ? IF EXISTS`

		applied, err := c.session.Query(updateQuery,
			request.CallbackUrl,
			payloadJSON,
			retryPolicyJSON,
			request.CallbackTimeoutSeconds,
			0, // Reset attempts on update
			shardId,
			databases.RowTypeTimer,
			resultExecuteAt,
			resultUuid,
		).WithContext(ctx).ScanCAS(nil)

		if err != nil {
			return databases.NewGenericDbError("failed to execute update query", err)
		}

		if !applied {
			// Timer was deleted between lookup and update
			return databases.NewDbErrorNotExists("timer not found", nil)
		}
	} else {
		// Different execution time - need delete+insert because timer_execute_at is part of primary key
		// No locking, so we'll use a batch but without shard version check
		batch := c.session.NewBatch(gocql.LoggedBatch).WithContext(ctx)

		// Delete the old timer entry
		deleteQuery := `DELETE FROM timers WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid = ? IF EXISTS`
		batch.Query(deleteQuery, shardId, databases.RowTypeTimer, resultExecuteAt, resultUuid)

		// Insert the timer with new execution time
		insertQuery := `INSERT INTO timers (shard_id, row_type, timer_execute_at, timer_uuid, timer_id, timer_namespace, timer_callback_url, timer_payload, timer_retry_policy, timer_callback_timeout_seconds, timer_created_at, timer_attempts) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
		batch.Query(insertQuery,
			shardId,
			databases.RowTypeTimer,
			request.ExecuteAt,
			resultUuid,
			request.TimerId,
			namespace,
			request.CallbackUrl,
			payloadJSON,
			retryPolicyJSON,
			request.CallbackTimeoutSeconds,
			resultCreatedAt, // Keep original creation time
			0,               // Reset attempts on update
		)

		// Execute the batch atomically
		previous := make(map[string]interface{})
		applied, iter, batchErr := c.session.MapExecuteBatchCAS(batch, previous)
		if iter != nil {
			iter.Close()
		}

		if batchErr != nil {
			return databases.NewGenericDbError("failed to execute atomic update batch", batchErr)
		}

		if !applied {
			// Timer was deleted between lookup and batch execution
			return databases.NewDbErrorNotExists("timer not found", nil)
		}
	}

	return nil
}

func (c *CassandraTimerStore) DeleteTimerNoLock(ctx context.Context, shardId int, namespace string, timerId string) *databases.DbError {
	// Find the timer's primary key components (execute_at, uuid)
	// This lookup is done without any shard version checking
	lookupQuery := `SELECT shard_id, row_type, timer_execute_at, timer_uuid, timer_namespace FROM timers 
	                WHERE shard_id = ? AND timer_id = ?`

	iter := c.session.Query(lookupQuery, shardId, timerId).
		WithContext(ctx).
		Iter()

	// Scan through results and find the one matching our shard, row type, and namespace
	var resultShardId int
	var resultRowType int16
	var resultExecuteAt time.Time
	var resultUuid gocql.UUID
	var resultNamespace string
	var found bool

	for iter.Scan(&resultShardId, &resultRowType, &resultExecuteAt, &resultUuid, &resultNamespace) {
		if resultShardId == shardId && resultRowType == databases.RowTypeTimer && resultNamespace == namespace {
			found = true
			break
		}
	}

	if err := iter.Close(); err != nil {
		return databases.NewGenericDbError("failed to close lookup iterator", err)
	}

	if !found {
		// Timer doesn't exist - return NotExists error
		return databases.NewDbErrorNotExists("timer not found", nil)
	}

	// Delete the timer using primary key with IF EXISTS (no shard version check)
	deleteQuery := `DELETE FROM timers WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid = ? IF EXISTS`

	applied, err := c.session.Query(deleteQuery, shardId, databases.RowTypeTimer, resultExecuteAt, resultUuid).
		WithContext(ctx).
		ScanCAS(nil)

	if err != nil {
		return databases.NewGenericDbError("failed to execute delete query", err)
	}

	if !applied {
		// Timer was deleted between lookup and delete (race condition)
		return databases.NewDbErrorNotExists("timer not found", nil)
	}

	return nil
}
