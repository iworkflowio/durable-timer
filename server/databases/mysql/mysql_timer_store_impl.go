package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/VividCortex/mysqlerr"
	_ "github.com/go-sql-driver/mysql"
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
	ctx context.Context, shardId int, ownerAddr string, metadata interface{},
) (shardVersion int64, retErr *databases.DbError) {
	// Convert ZeroUUID to high/low format for shard records
	zeroUuidHigh, zeroUuidLow := databases.UuidToHighLow(databases.ZeroUUID)

	// Serialize metadata to JSON
	var metadataJSON interface{}
	if metadata != nil {
		metadataBytes, err := json.Marshal(metadata)
		if err != nil {
			return 0, databases.NewGenericDbError("failed to marshal metadata", err)
		}
		metadataJSON = string(metadataBytes)
	}

	now := time.Now().UTC()

	// First, try to read the current shard record from unified timers table
	var currentVersion int64
	var currentOwnerAddr string
	query := `SELECT shard_version, shard_owner_addr FROM timers 
	          WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid_high = ? AND timer_uuid_low = ?`

	err := m.db.QueryRowContext(ctx, query, shardId, databases.RowTypeShard,
		databases.ZeroTimestamp, zeroUuidHigh, zeroUuidLow).Scan(&currentVersion, &currentOwnerAddr)

	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, databases.NewGenericDbError("failed to read shard record", err)
	}

	if errors.Is(err, sql.ErrNoRows) {
		// Shard doesn't exist, create it with version 1
		newVersion := int64(1)
		insertQuery := `INSERT INTO timers (shard_id, row_type, timer_execute_at, timer_uuid_high, timer_uuid_low, 
		                                   shard_version, shard_owner_addr, shard_claimed_at, shard_metadata) 
		                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

		_, err := m.db.ExecContext(ctx, insertQuery,
			shardId, databases.RowTypeShard, databases.ZeroTimestamp, zeroUuidHigh, zeroUuidLow,
			newVersion, ownerAddr, now, metadataJSON)

		if err != nil {
			// Check if it's a duplicate key error (another instance created it concurrently)
			if isDuplicateKeyError(err) {
				// Try to read the existing record to return conflict info
				var conflictVersion int64
				var conflictOwnerAddr string
				var conflictClaimedAt time.Time
				var conflictMetadata string

				conflictQuery := `SELECT shard_version, shard_owner_addr, shard_claimed_at, shard_metadata 
				                  FROM timers WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid_high = ? AND timer_uuid_low = ?`

				conflictErr := m.db.QueryRowContext(ctx, conflictQuery, shardId, databases.RowTypeShard,
					databases.ZeroTimestamp, zeroUuidHigh, zeroUuidLow).Scan(&conflictVersion, &conflictOwnerAddr, &conflictClaimedAt, &conflictMetadata)

				if conflictErr == nil {
					conflictInfo := &databases.ShardInfo{
						ShardId:      int64(shardId),
						OwnerAddr:    conflictOwnerAddr,
						ClaimedAt:    conflictClaimedAt,
						Metadata:     conflictMetadata,
						ShardVersion: conflictVersion,
					}
					return 0, databases.NewDbErrorOnShardConditionFail("failed to insert shard record due to concurrent insert", nil, conflictInfo)
				}
			}
			return 0, databases.NewGenericDbError("failed to insert new shard record", err)
		}

		// Successfully created new record
		return newVersion, nil
	}

	// Update the shard with new version and ownership using optimistic concurrency control
	newVersion := currentVersion + 1
	updateQuery := `UPDATE timers SET shard_version = ?, shard_owner_addr = ?, shard_claimed_at = ?, shard_metadata = ? 
	                WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid_high = ? AND timer_uuid_low = ? AND shard_version = ?`

	result, err := m.db.ExecContext(ctx, updateQuery,
		newVersion, ownerAddr, now, metadataJSON,
		shardId, databases.RowTypeShard, databases.ZeroTimestamp, zeroUuidHigh, zeroUuidLow, currentVersion)

	if err != nil {
		return 0, databases.NewGenericDbError("failed to update shard record", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, databases.NewGenericDbError("failed to get rows affected", err)
	}

	if rowsAffected == 0 {
		// Version changed concurrently, return conflict info
		conflictInfo := &databases.ShardInfo{
			ShardVersion: currentVersion, // We know it was at least this version
		}
		return 0, databases.NewDbErrorOnShardConditionFail("shard ownership claim failed due to concurrent modification", nil, conflictInfo)
	}

	return newVersion, nil
}

// isDuplicateKeyError checks if the error is a MySQL duplicate key error
func isDuplicateKeyError(err error) bool {
	var sqlErr *mysql.MySQLError
	ok := errors.As(err, &sqlErr)
	// ErrDupEntry MySQL Error 1062 indicates a duplicate primary key i.e. the row already exists,
	// so we don't do the insert and return a ConditionalUpdate error.
	return ok && sqlErr.Number == mysqlerr.ER_DUP_ENTRY
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
			// Shard doesn't exist
			conflictInfo := &databases.ShardInfo{
				ShardVersion: 0,
			}
			return databases.NewDbErrorOnShardConditionFail("shard not found during timer creation", nil, conflictInfo)
		}
		return databases.NewGenericDbError("failed to read shard", shardErr)
	}

	// Compare shard versions
	if actualShardVersion != shardVersion {
		// Version mismatch
		conflictInfo := &databases.ShardInfo{
			ShardVersion: actualShardVersion,
		}
		return databases.NewDbErrorOnShardConditionFail("shard version mismatch during timer creation", nil, conflictInfo)
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
		timer.CreatedAt, 0)

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
		timer.CreatedAt, 0)

	if insertErr != nil {
		return databases.NewGenericDbError("failed to upsert timer", insertErr)
	}

	return nil
}

func (c *MySQLTimerStore) GetTimersUpToTimestamp(ctx context.Context, shardId int, request *databases.RangeGetTimersRequest) (*databases.RangeGetTimersResponse, *databases.DbError) {
	// Query timers up to the specified timestamp, ordered by execution time
	query := `SELECT shard_id, row_type, timer_execute_at, timer_uuid_high, timer_uuid_low,
	                 timer_id, timer_namespace, timer_callback_url, timer_payload, 
	                 timer_retry_policy, timer_callback_timeout_seconds, timer_created_at, timer_attempts
	          FROM timers 
	          WHERE shard_id = ? AND row_type = ? AND timer_execute_at <= ?
	          ORDER BY timer_execute_at ASC, timer_uuid_high ASC, timer_uuid_low ASC
	          LIMIT ?`

	rows, err := c.db.QueryContext(ctx, query, shardId, databases.RowTypeTimer, request.UpToTimestamp, request.Limit)
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

func (c *MySQLTimerStore) DeleteTimersUpToTimestampWithBatchInsert(ctx context.Context, shardId int, shardVersion int64, request *databases.RangeDeleteTimersRequest, TimersToInsert []*databases.DbTimer) (*databases.RangeDeleteTimersResponse, *databases.DbError) {
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
		// Version mismatch
		conflictInfo := &databases.ShardInfo{
			ShardVersion: actualShardVersion,
		}
		return nil, databases.NewDbErrorOnShardConditionFail("shard version mismatch during delete and insert operation", nil, conflictInfo)
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
			0, // timer_attempts starts at 0
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

func (c *MySQLTimerStore) BatchInsertTimers(ctx context.Context, shardId int, shardVersion int64, TimersToInsert []*databases.DbTimer) *databases.DbError {
	//TODO implement me
	panic("implement me")
}

func (c *MySQLTimerStore) UpdateTimer(ctx context.Context, shardId int, shardVersion int64, namespace string, request *databases.UpdateDbTimerRequest) (err *databases.DbError) {
	//TODO implement me
	panic("implement me")
}

func (c *MySQLTimerStore) GetTimer(ctx context.Context, shardId int, namespace string, timerId string) (timer *databases.DbTimer, err *databases.DbError) {
	//TODO implement me
	panic("implement me")
}

func (c *MySQLTimerStore) DeleteTimer(ctx context.Context, shardId int, shardVersion int64, namespace string, timerId string) *databases.DbError {
	//TODO implement me
	panic("implement me")
}
