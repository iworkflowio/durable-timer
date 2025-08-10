package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/VividCortex/mysqlerr"
	"github.com/go-sql-driver/mysql"
	"github.com/iworkflowio/durable-timer/config"
	"github.com/iworkflowio/durable-timer/databases"
)

// MySQLTimerStore implements TimerStore interface for MySQL
type MySQLTimerStore struct {
	db *sql.DB
}

// NewMySQLTimerStore creates a new MySQL timer store
func NewMySQLTimerStore(config *config.MySQLConnectConfig) (databases.TimerStore, error) {
	dsn := fmt.Sprintf("%s:%s@tcp(%s)/%s?parseTime=true&loc=UTC",
		config.Username, config.Password, config.Host, config.Database)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open MySQL connection: %w", err)
	}

	// Test connection
	err = db.Ping()
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping MySQL: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(config.MaxOpenConns)
	db.SetMaxIdleConns(config.MaxIdleConns)
	db.SetConnMaxLifetime(config.ConnMaxLifetime)

	store := &MySQLTimerStore{
		db: db,
	}

	return store, nil
}

// Close closes the MySQL connection
func (m *MySQLTimerStore) Close() error {
	if m.db != nil {
		return m.db.Close()
	}
	return nil
}

func (m *MySQLTimerStore) ClaimShardOwnership(
	ctx context.Context,
	shardId int,
	ownerAddr string,
) (prevShardInfo, currentShardInfo *databases.ShardInfo, err *databases.DbError) {
	// Convert ZeroUUID to high/low format for shard records
	zeroUuidHigh, zeroUuidLow := databases.UuidToHighLow(databases.ZeroUUID)

	now := time.Now().UTC()

	// First, try to read the current shard record from unified timers table
	var currentVersion int64
	var currentOwnerAddr string
	var currentClaimedAt time.Time
	var currentMetadata string
	query := `SELECT shard_version, shard_owner_addr, shard_claimed_at, shard_metadata FROM timers 
	          WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid_high = ? AND timer_uuid_low = ?`

	readErr := m.db.QueryRowContext(ctx, query, shardId, databases.RowTypeShard,
		databases.ZeroTimestamp, zeroUuidHigh, zeroUuidLow).Scan(&currentVersion, &currentOwnerAddr, &currentClaimedAt, &currentMetadata)

	if readErr != nil && !errors.Is(readErr, sql.ErrNoRows) {
		return nil, nil, databases.NewGenericDbError("failed to read shard record", readErr)
	}

	if errors.Is(readErr, sql.ErrNoRows) {
		// Shard doesn't exist, create it with version 1
		newVersion := int64(1)

		// Initialize with default metadata
		defaultMetadata := databases.ShardMetadata{}
		metadataJSON, marshalErr := json.Marshal(defaultMetadata)
		if marshalErr != nil {
			return nil, nil, databases.NewGenericDbError("failed to marshal default metadata", marshalErr)
		}

		insertQuery := `INSERT INTO timers (shard_id, row_type, timer_execute_at, timer_uuid_high, timer_uuid_low, 
		                                   shard_version, shard_owner_addr, shard_claimed_at, shard_metadata) 
		                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

		_, insertErr := m.db.ExecContext(ctx, insertQuery,
			shardId, databases.RowTypeShard, databases.ZeroTimestamp, zeroUuidHigh, zeroUuidLow,
			newVersion, ownerAddr, now, string(metadataJSON))

		if insertErr != nil {
			// Check if it's a duplicate key error (another instance created it concurrently)
			if isDuplicateKeyError(insertErr) {
				// Don't read conflict info to avoid expensive extra query
				return nil, nil, databases.NewDbErrorOnShardConditionFail("failed to insert shard record due to concurrent insert", insertErr)
			}
			return nil, nil, databases.NewGenericDbError("failed to insert new shard record", insertErr)
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

	// Extract current shard info for prevShardInfo
	prevShardInfo = &databases.ShardInfo{
		ShardId:      int64(shardId),
		OwnerAddr:    currentOwnerAddr,
		ShardVersion: currentVersion,
		ClaimedAt:    currentClaimedAt,
	}

	// Parse metadata from JSON string to ShardMetadata struct
	if currentMetadata != "" {
		var shardMetadata databases.ShardMetadata
		if metadataErr := json.Unmarshal([]byte(currentMetadata), &shardMetadata); metadataErr == nil {
			prevShardInfo.Metadata = shardMetadata
		}
		// If parsing fails, use default metadata (zero values)
	}

	// Update the shard with new version and ownership using optimistic concurrency control (preserve existing metadata)
	newVersion := currentVersion + 1
	updateQuery := `UPDATE timers SET shard_version = ?, shard_owner_addr = ?, shard_claimed_at = ? 
	                WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid_high = ? AND timer_uuid_low = ? AND shard_version = ?`

	result, updateErr := m.db.ExecContext(ctx, updateQuery,
		newVersion, ownerAddr, now,
		shardId, databases.RowTypeShard, databases.ZeroTimestamp, zeroUuidHigh, zeroUuidLow, currentVersion)

	if updateErr != nil {
		return nil, nil, databases.NewGenericDbError("failed to update shard record", updateErr)
	}

	rowsAffected, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return nil, nil, databases.NewGenericDbError("failed to get rows affected", rowsErr)
	}

	if rowsAffected == 0 {
		// Version changed concurrently, don't read conflict info to avoid expensive extra query
		updateErr := errors.New("no rows affected")
		return nil, nil, databases.NewDbErrorOnShardConditionFail("shard ownership claim failed due to concurrent modification", updateErr)
	}

	// Successfully updated, return both previous and current shard info
	currentShardInfo = &databases.ShardInfo{
		ShardId:      int64(shardId),
		OwnerAddr:    ownerAddr,
		ShardVersion: newVersion,
		Metadata:     prevShardInfo.Metadata, // Preserve existing metadata
		ClaimedAt:    now,
	}

	return prevShardInfo, currentShardInfo, nil
}

// isDuplicateKeyError checks if the error is a MySQL duplicate key error
func isDuplicateKeyError(err error) bool {
	var sqlErr *mysql.MySQLError
	ok := errors.As(err, &sqlErr)
	// ErrDupEntry MySQL Error 1062 indicates a duplicate primary key i.e. the row already exists,
	// so we don't do the insert and return a ConditionalUpdate error.
	return ok && sqlErr.Number == mysqlerr.ER_DUP_ENTRY
}

func (m *MySQLTimerStore) UpdateShardMetadata(
	ctx context.Context,
	shardId int, shardVersion int64,
	metadata databases.ShardMetadata,
) (err *databases.DbError) {
	// Marshal metadata to JSON
	metadataJSON, marshalErr := json.Marshal(metadata)
	if marshalErr != nil {
		return databases.NewGenericDbError("failed to marshal metadata", marshalErr)
	}

	// Convert ZeroUUID to high/low format for shard records
	zeroUuidHigh, zeroUuidLow := databases.UuidToHighLow(databases.ZeroUUID)

	// Use conditional update to update shard metadata only if the shard version matches
	updateQuery := `UPDATE timers SET shard_metadata = ?, shard_updated_at = ?
	                WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid_high = ? AND timer_uuid_low = ? AND shard_version = ?`

	result, updateErr := m.db.ExecContext(ctx, updateQuery, string(metadataJSON), time.Now().UTC(), shardId, databases.RowTypeShard, databases.ZeroTimestamp, zeroUuidHigh, zeroUuidLow, shardVersion)
	if updateErr != nil {
		return databases.NewGenericDbError("failed to update shard metadata", updateErr)
	}

	rowsAffected, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return databases.NewGenericDbError("failed to get rows affected", rowsErr)
	}

	if rowsAffected == 0 {
		// No rows updated means either shard doesn't exist or version mismatch
		// Create a minimal error to avoid nil pointer dereference in DbError.Error()
		err := errors.New("no rows affected")
		return databases.NewDbErrorOnShardConditionFail("shard version mismatch during metadata update", err)
	}

	return nil
}

func (m *MySQLTimerStore) CreateTimer(ctx context.Context, shardId int, shardVersion int64, namespace string, timer *databases.DbTimer) (err *databases.DbError) {
	// Convert the provided timer UUID to high/low format for predictable pagination
	timerUuidHigh, timerUuidLow := databases.UuidToHighLow(timer.TimerUuid)

	// Convert ZeroUUID to high/low format for shard records
	zeroUuidHigh, zeroUuidLow := databases.UuidToHighLow(databases.ZeroUUID)

	// Serialize payload and retry policy to JSON
	var payloadJSON, retryPolicyJSON interface{}

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

	// Use MySQL transaction to atomically check shard version and insert timer
	tx, txErr := m.db.BeginTx(ctx, nil)
	if txErr != nil {
		return databases.NewGenericDbError("failed to start MySQL transaction", txErr)
	}
	defer tx.Rollback() // Will be no-op if transaction is committed

	// Read the shard to get current version within the transaction
	var actualShardVersion int64
	shardQuery := `SELECT shard_version FROM timers 
	               WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid_high = ? AND timer_uuid_low = ?`

	shardErr := tx.QueryRowContext(ctx, shardQuery, shardId, databases.RowTypeShard,
		databases.ZeroTimestamp, zeroUuidHigh, zeroUuidLow).Scan(&actualShardVersion)

	if shardErr != nil {
		if errors.Is(shardErr, sql.ErrNoRows) {
			return databases.NewDbErrorOnShardConditionFail("shard not found during timer creation", nil)
		}
		return databases.NewGenericDbError("failed to read shard", shardErr)
	}

	// Compare shard versions
	if actualShardVersion != shardVersion {

		return databases.NewDbErrorOnShardConditionFail("shard version mismatch during timer creation", nil)
	}

	// Shard version matches, proceed to upsert timer within the transaction (overwrite if exists)
	upsertQuery := `INSERT INTO timers (shard_id, row_type, timer_execute_at, timer_uuid_high, timer_uuid_low, 
	                                   timer_id, timer_namespace, timer_callback_url, 
	                                   timer_payload, timer_retry_policy, timer_callback_timeout_seconds,
	                                   timer_created_at, timer_attempts) 
	                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	                ON DUPLICATE KEY UPDATE
	                  timer_execute_at = VALUES(timer_execute_at),
	                  timer_uuid_high = VALUES(timer_uuid_high),
	                  timer_uuid_low = VALUES(timer_uuid_low),
	                  timer_callback_url = VALUES(timer_callback_url),
	                  timer_payload = VALUES(timer_payload),
	                  timer_retry_policy = VALUES(timer_retry_policy),
	                  timer_callback_timeout_seconds = VALUES(timer_callback_timeout_seconds),
	                  timer_created_at = VALUES(timer_created_at),
	                  timer_attempts = VALUES(timer_attempts)`

	_, insertErr := tx.ExecContext(ctx, upsertQuery,
		shardId, databases.RowTypeTimer, timer.ExecuteAt, timerUuidHigh, timerUuidLow,
		timer.Id, timer.Namespace, timer.CallbackUrl,
		payloadJSON, retryPolicyJSON, timer.CallbackTimeoutSeconds,
		timer.CreatedAt, timer.Attempts)

	if insertErr != nil {
		return databases.NewGenericDbError("failed to upsert timer", insertErr)
	}

	// Commit the transaction
	if commitErr := tx.Commit(); commitErr != nil {
		return databases.NewGenericDbError("failed to commit transaction", commitErr)
	}

	return nil
}

func (m *MySQLTimerStore) CreateTimerNoLock(ctx context.Context, shardId int, namespace string, timer *databases.DbTimer) (err *databases.DbError) {
	// Convert the provided timer UUID to high/low format for predictable pagination
	timerUuidHigh, timerUuidLow := databases.UuidToHighLow(timer.TimerUuid)

	// Serialize payload and retry policy to JSON
	var payloadJSON, retryPolicyJSON interface{}

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

	// Upsert the timer directly without any locking or version checking (overwrite if exists)
	upsertQuery := `INSERT INTO timers (shard_id, row_type, timer_execute_at, timer_uuid_high, timer_uuid_low, 
	                                   timer_id, timer_namespace, timer_callback_url, 
	                                   timer_payload, timer_retry_policy, timer_callback_timeout_seconds,
	                                   timer_created_at, timer_attempts) 
	                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	                ON DUPLICATE KEY UPDATE
	                  timer_execute_at = VALUES(timer_execute_at),
	                  timer_uuid_high = VALUES(timer_uuid_high),
	                  timer_uuid_low = VALUES(timer_uuid_low),
	                  timer_callback_url = VALUES(timer_callback_url),
	                  timer_payload = VALUES(timer_payload),
	                  timer_retry_policy = VALUES(timer_retry_policy),
	                  timer_callback_timeout_seconds = VALUES(timer_callback_timeout_seconds),
	                  timer_created_at = VALUES(timer_created_at),
	                  timer_attempts = VALUES(timer_attempts)`

	_, insertErr := m.db.ExecContext(ctx, upsertQuery,
		shardId, databases.RowTypeTimer, timer.ExecuteAt, timerUuidHigh, timerUuidLow,
		timer.Id, timer.Namespace, timer.CallbackUrl,
		payloadJSON, retryPolicyJSON, timer.CallbackTimeoutSeconds,
		timer.CreatedAt, timer.Attempts)

	if insertErr != nil {
		return databases.NewGenericDbError("failed to upsert timer", insertErr)
	}

	return nil
}

func (c *MySQLTimerStore) RangeGetTimers(ctx context.Context, shardId int, request *databases.RangeGetTimersRequest) (*databases.RangeGetTimersResponse, *databases.DbError) {
	// Convert start and end UUIDs to high/low format for precise range selection
	startUuidHigh, startUuidLow := databases.UuidToHighLow(request.StartTimeUuid)
	endUuidHigh, endUuidLow := databases.UuidToHighLow(request.EndTimeUuid)

	// Query timers in the specified range, ordered by execution time and UUID
	query := `SELECT shard_id, row_type, timer_execute_at, timer_uuid_high, timer_uuid_low,
	                 timer_id, timer_namespace, timer_callback_url, timer_payload, 
	                 timer_retry_policy, timer_callback_timeout_seconds, timer_created_at, timer_attempts
	          FROM timers 
	          WHERE shard_id = ? AND row_type = ? 
	            AND (timer_execute_at, timer_uuid_high, timer_uuid_low) >= (?, ?, ?)
	            AND (timer_execute_at, timer_uuid_high, timer_uuid_low) <= (?, ?, ?)
	          ORDER BY timer_execute_at ASC, timer_uuid_high ASC, timer_uuid_low ASC
	          LIMIT ?`

	rows, err := c.db.QueryContext(ctx, query, shardId, databases.RowTypeTimer,
		request.StartTimestamp, startUuidHigh, startUuidLow,
		request.EndTimestamp, endUuidHigh, endUuidLow,
		request.Limit)
	if err != nil {
		return nil, databases.NewGenericDbError("failed to query timers", err)
	}
	defer rows.Close()

	var timers []*databases.DbTimer

	for rows.Next() {
		var (
			dbShardId                  int
			dbRowType                  int16
			dbTimerExecuteAt           time.Time
			dbTimerUuidHigh            int64
			dbTimerUuidLow             int64
			dbTimerId                  string
			dbTimerNamespace           string
			dbTimerCallbackUrl         string
			dbTimerPayload             sql.NullString
			dbTimerRetryPolicy         sql.NullString
			dbTimerCallbackTimeoutSecs int32
			dbTimerCreatedAt           time.Time
			dbTimerAttempts            int32
		)

		if err := rows.Scan(&dbShardId, &dbRowType, &dbTimerExecuteAt, &dbTimerUuidHigh, &dbTimerUuidLow,
			&dbTimerId, &dbTimerNamespace, &dbTimerCallbackUrl, &dbTimerPayload,
			&dbTimerRetryPolicy, &dbTimerCallbackTimeoutSecs, &dbTimerCreatedAt, &dbTimerAttempts); err != nil {
			return nil, databases.NewGenericDbError("failed to scan timer row", err)
		}

		// Convert UUID high/low back to UUID
		timerUuid := databases.HighLowToUuid(dbTimerUuidHigh, dbTimerUuidLow)

		// Parse JSON payload and retry policy
		var payload interface{}
		var retryPolicy interface{}

		if dbTimerPayload.Valid && dbTimerPayload.String != "" {
			if err := json.Unmarshal([]byte(dbTimerPayload.String), &payload); err != nil {
				return nil, databases.NewGenericDbError("failed to unmarshal timer payload", err)
			}
		}

		if dbTimerRetryPolicy.Valid && dbTimerRetryPolicy.String != "" {
			if err := json.Unmarshal([]byte(dbTimerRetryPolicy.String), &retryPolicy); err != nil {
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

	if err := rows.Err(); err != nil {
		return nil, databases.NewGenericDbError("failed to iterate over timer rows", err)
	}

	return &databases.RangeGetTimersResponse{
		Timers: timers,
	}, nil
}

func (c *MySQLTimerStore) RangeDeleteWithBatchInsertTxn(ctx context.Context, shardId int, shardVersion int64, request *databases.RangeDeleteTimersRequest, TimersToInsert []*databases.DbTimer) (*databases.RangeDeleteTimersResponse, *databases.DbError) {
	// Convert start and end UUIDs to high/low format for precise range selection
	startUuidHigh, startUuidLow := databases.UuidToHighLow(request.StartTimeUuid)
	endUuidHigh, endUuidLow := databases.UuidToHighLow(request.EndTimeUuid)

	// Convert ZeroUUID to high/low format for shard records
	zeroUuidHigh, zeroUuidLow := databases.UuidToHighLow(databases.ZeroUUID)

	// Use MySQL transaction to atomically check shard version, delete timers, and insert new ones
	tx, txErr := c.db.BeginTx(ctx, nil)
	if txErr != nil {
		return nil, databases.NewGenericDbError("failed to start MySQL transaction", txErr)
	}
	defer tx.Rollback() // Will be no-op if transaction is committed

	// Read the shard to verify version within the transaction
	var actualShardVersion int64
	shardQuery := `SELECT shard_version FROM timers 
	               WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid_high = ? AND timer_uuid_low = ?`

	shardErr := tx.QueryRowContext(ctx, shardQuery, shardId, databases.RowTypeShard,
		databases.ZeroTimestamp, zeroUuidHigh, zeroUuidLow).Scan(&actualShardVersion)

	if shardErr != nil {
		if errors.Is(shardErr, sql.ErrNoRows) {
			// Shard doesn't exist
			return nil, databases.NewGenericDbError("shard record does not exist", nil)
		}
		return nil, databases.NewGenericDbError("failed to read shard", shardErr)
	}

	// Compare shard versions
	if actualShardVersion != shardVersion {
		return nil, databases.NewDbErrorOnShardConditionFail("shard version mismatch during delete and insert operation", nil)
	}

	// Delete timers in the specified range
	deleteQuery := `DELETE FROM timers 
	                WHERE shard_id = ? AND row_type = ? 
	                AND (timer_execute_at, timer_uuid_high, timer_uuid_low) >= (?, ?, ?)
	                AND (timer_execute_at, timer_uuid_high, timer_uuid_low) <= (?, ?, ?)`

	deleteResult, deleteErr := tx.ExecContext(ctx, deleteQuery, shardId, databases.RowTypeTimer,
		request.StartTimestamp, startUuidHigh, startUuidLow,
		request.EndTimestamp, endUuidHigh, endUuidLow)

	if deleteErr != nil {
		return nil, databases.NewGenericDbError("failed to delete timers", deleteErr)
	}

	// Get actual deleted count from MySQL
	rowsAffected, rowsErr := deleteResult.RowsAffected()
	if rowsErr != nil {
		return nil, databases.NewGenericDbError("failed to get deleted rows count", rowsErr)
	}
	deletedCount := int(rowsAffected)

	// Insert new timers
	for _, timer := range TimersToInsert {
		timerUuidHigh, timerUuidLow := databases.UuidToHighLow(timer.TimerUuid)

		// Serialize payload and retry policy to JSON
		var payloadJSON, retryPolicyJSON interface{}

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

		insertQuery := `INSERT INTO timers (shard_id, row_type, timer_execute_at, timer_uuid_high, timer_uuid_low, 
		                                   timer_id, timer_namespace, timer_callback_url, timer_payload, 
		                                   timer_retry_policy, timer_callback_timeout_seconds, timer_created_at, timer_attempts)
		                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

		_, insertErr := tx.ExecContext(ctx, insertQuery,
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
			timer.Attempts, // Use timer's actual attempts value
		)

		if insertErr != nil {
			return nil, databases.NewGenericDbError("failed to insert timer", insertErr)
		}
	}

	// Commit the transaction
	commitErr := tx.Commit()
	if commitErr != nil {
		return nil, databases.NewGenericDbError("failed to commit transaction", commitErr)
	}

	return &databases.RangeDeleteTimersResponse{
		DeletedCount: deletedCount,
	}, nil
}

func (c *MySQLTimerStore) RangeDeleteWithLimit(ctx context.Context, shardId int, request *databases.RangeDeleteTimersRequest, limit int) (*databases.RangeDeleteTimersResponse, *databases.DbError) {
	startUuidHigh, startUuidLow := databases.UuidToHighLow(request.StartTimeUuid)
	endUuidHigh, endUuidLow := databases.UuidToHighLow(request.EndTimeUuid)

	deleteQuery := `DELETE FROM timers 
	                WHERE shard_id = ? AND row_type = ? 
	                AND (timer_execute_at, timer_uuid_high, timer_uuid_low) >= (?, ?, ?)
	                AND (timer_execute_at, timer_uuid_high, timer_uuid_low) <= (?, ?, ?)
	                LIMIT ?`

	deleteResult, deleteErr := c.db.ExecContext(ctx, deleteQuery,
		shardId, databases.RowTypeTimer,
		request.StartTimestamp, startUuidHigh, startUuidLow,
		request.EndTimestamp, endUuidHigh, endUuidLow,
		limit)

	if deleteErr != nil {
		return nil, databases.NewGenericDbError("failed to delete timers", deleteErr)
	}

	rowsAffected, rowsErr := deleteResult.RowsAffected()
	if rowsErr != nil {
		return nil, databases.NewGenericDbError("failed to get rows affected", rowsErr)
	}

	return &databases.RangeDeleteTimersResponse{
		DeletedCount: int(rowsAffected),
	}, nil
}

func (c *MySQLTimerStore) UpdateTimer(ctx context.Context, shardId int, shardVersion int64, namespace string, request *databases.UpdateDbTimerRequest) (err *databases.DbError) {
	// Convert ZeroUUID to high/low format for shard records
	zeroUuidHigh, zeroUuidLow := databases.UuidToHighLow(databases.ZeroUUID)

	// Serialize payload and retry policy to JSON
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

	// Use MySQL transaction to atomically check shard version and update timer
	tx, txErr := c.db.BeginTx(ctx, nil)
	if txErr != nil {
		return databases.NewGenericDbError("failed to start MySQL transaction", txErr)
	}
	defer tx.Rollback() // Will be no-op if transaction is committed

	// Read the shard to verify version within the transaction
	var actualShardVersion int64
	shardQuery := `SELECT shard_version FROM timers 
	               WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid_high = ? AND timer_uuid_low = ?`

	shardErr := tx.QueryRowContext(ctx, shardQuery, shardId, databases.RowTypeShard,
		databases.ZeroTimestamp, zeroUuidHigh, zeroUuidLow).Scan(&actualShardVersion)

	if shardErr != nil {
		if errors.Is(shardErr, sql.ErrNoRows) {

			return databases.NewDbErrorOnShardConditionFail("shard not found during timer update", nil)
		}
		return databases.NewGenericDbError("failed to read shard", shardErr)
	}

	// Compare shard versions
	if actualShardVersion != shardVersion {

		return databases.NewDbErrorOnShardConditionFail("shard version mismatch during timer update", nil)
	}

	// Optimized update using REPLACE INTO which handles both update and potential primary key changes
	// This approach doesn't require a lookup since we use the unique constraint (shard_id, row_type, timer_namespace, timer_id)
	// Generate the new UUID from the request
	newTimerUuid := databases.GenerateTimerUUID(namespace, request.TimerId)
	newTimerUuidHigh, newTimerUuidLow := databases.UuidToHighLow(newTimerUuid)

	// Use REPLACE INTO with a derived table to preserve creation time
	// This query will either UPDATE existing timer or fail if timer doesn't exist
	updateQuery := `UPDATE timers 
	                SET timer_execute_at = ?, timer_uuid_high = ?, timer_uuid_low = ?, 
	                    timer_callback_url = ?, timer_payload = ?, timer_retry_policy = ?, 
	                    timer_callback_timeout_seconds = ?, timer_attempts = 0
	                WHERE shard_id = ? AND row_type = ? AND timer_namespace = ? AND timer_id = ?`

	result, updateErr := tx.ExecContext(ctx, updateQuery,
		request.ExecuteAt, newTimerUuidHigh, newTimerUuidLow,
		request.CallbackUrl, payloadJSON, retryPolicyJSON,
		request.CallbackTimeoutSeconds,
		shardId, databases.RowTypeTimer, namespace, request.TimerId)

	if updateErr != nil {
		return databases.NewGenericDbError("failed to update timer", updateErr)
	}

	// Check if any rows were affected
	rowsAffected, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return databases.NewGenericDbError("failed to get rows affected", rowsErr)
	}

	if rowsAffected == 0 {
		// Timer doesn't exist
		return databases.NewDbErrorNotExists("timer not found for update", nil)
	}

	// Commit the transaction
	if commitErr := tx.Commit(); commitErr != nil {
		return databases.NewGenericDbError("failed to commit transaction", commitErr)
	}

	return nil
}

func (c *MySQLTimerStore) GetTimer(ctx context.Context, shardId int, namespace string, timerId string) (timer *databases.DbTimer, err *databases.DbError) {
	// Query timer by shard_id, namespace, and timer_id
	query := `SELECT shard_id, row_type, timer_execute_at, timer_uuid_high, timer_uuid_low,
	                 timer_id, timer_namespace, timer_callback_url, timer_payload, 
	                 timer_retry_policy, timer_callback_timeout_seconds, timer_created_at, timer_attempts
	          FROM timers 
	          WHERE shard_id = ? AND row_type = ? AND timer_namespace = ? AND timer_id = ?`

	var (
		dbShardId                  int
		dbRowType                  int16
		dbTimerExecuteAt           time.Time
		dbTimerUuidHigh            int64
		dbTimerUuidLow             int64
		dbTimerId                  string
		dbTimerNamespace           string
		dbTimerCallbackUrl         string
		dbTimerPayload             sql.NullString
		dbTimerRetryPolicy         sql.NullString
		dbTimerCallbackTimeoutSecs int32
		dbTimerCreatedAt           time.Time
		dbTimerAttempts            int32
	)

	scanErr := c.db.QueryRowContext(ctx, query, shardId, databases.RowTypeTimer, namespace, timerId).
		Scan(&dbShardId, &dbRowType, &dbTimerExecuteAt, &dbTimerUuidHigh, &dbTimerUuidLow,
			&dbTimerId, &dbTimerNamespace, &dbTimerCallbackUrl, &dbTimerPayload,
			&dbTimerRetryPolicy, &dbTimerCallbackTimeoutSecs, &dbTimerCreatedAt, &dbTimerAttempts)

	if scanErr != nil {
		if errors.Is(scanErr, sql.ErrNoRows) {
			return nil, databases.NewDbErrorNotExists("timer not found", scanErr)
		}
		return nil, databases.NewGenericDbError("failed to query timer", scanErr)
	}

	// Convert UUID high/low back to UUID
	timerUuid := databases.HighLowToUuid(dbTimerUuidHigh, dbTimerUuidLow)

	// Parse JSON payload and retry policy
	var payload interface{}
	var retryPolicy interface{}

	if dbTimerPayload.Valid && dbTimerPayload.String != "" {
		if err := json.Unmarshal([]byte(dbTimerPayload.String), &payload); err != nil {
			return nil, databases.NewGenericDbError("failed to unmarshal timer payload", err)
		}
	}

	if dbTimerRetryPolicy.Valid && dbTimerRetryPolicy.String != "" {
		if err := json.Unmarshal([]byte(dbTimerRetryPolicy.String), &retryPolicy); err != nil {
			return nil, databases.NewGenericDbError("failed to unmarshal timer retry policy", err)
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

func (c *MySQLTimerStore) DeleteTimer(ctx context.Context, shardId int, shardVersion int64, namespace string, timerId string) *databases.DbError {
	// Convert ZeroUUID to high/low format for shard records
	zeroUuidHigh, zeroUuidLow := databases.UuidToHighLow(databases.ZeroUUID)

	// Use MySQL transaction to atomically check shard version and delete timer
	tx, txErr := c.db.BeginTx(ctx, nil)
	if txErr != nil {
		return databases.NewGenericDbError("failed to start MySQL transaction", txErr)
	}
	defer tx.Rollback() // Will be no-op if transaction is committed

	// Read the shard to verify version within the transaction
	var actualShardVersion int64
	shardQuery := `SELECT shard_version FROM timers 
	               WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid_high = ? AND timer_uuid_low = ?`

	shardErr := tx.QueryRowContext(ctx, shardQuery, shardId, databases.RowTypeShard,
		databases.ZeroTimestamp, zeroUuidHigh, zeroUuidLow).Scan(&actualShardVersion)

	if shardErr != nil {
		if errors.Is(shardErr, sql.ErrNoRows) {
			// Shard doesn't exist
			return databases.NewDbErrorOnShardConditionFail("shard not found during timer delete", nil)
		}
		return databases.NewGenericDbError("failed to read shard", shardErr)
	}

	// Compare shard versions
	if actualShardVersion != shardVersion {

		return databases.NewDbErrorOnShardConditionFail("shard version mismatch during timer delete", nil)
	}

	// Optimized delete without lookup - use the unique constraint (shard_id, row_type, timer_namespace, timer_id)
	// This approach is optimized because it doesn't require a lookup first
	deleteQuery := `DELETE FROM timers 
	                WHERE shard_id = ? AND row_type = ? AND timer_namespace = ? AND timer_id = ?`

	_, deleteErr := tx.ExecContext(ctx, deleteQuery, shardId, databases.RowTypeTimer, namespace, timerId)

	if deleteErr != nil {
		return databases.NewGenericDbError("failed to delete timer", deleteErr)
	}

	// For delete operations, we treat it as idempotent - even if no rows were affected (timer doesn't exist),
	// we consider it successful since the goal (timer being deleted) is achieved
	// This is consistent with other database implementations

	// Commit the transaction
	if commitErr := tx.Commit(); commitErr != nil {
		return databases.NewGenericDbError("failed to commit transaction", commitErr)
	}

	return nil
}
