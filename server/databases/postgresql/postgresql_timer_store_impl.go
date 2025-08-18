package postgresql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/iworkflowio/durable-timer/config"
	"github.com/iworkflowio/durable-timer/databases"
	"github.com/lib/pq"
)

// PostgreSQLTimerStore implements TimerStore interface for PostgreSQL
type PostgreSQLTimerStore struct {
	db *sql.DB
}

// NewPostgreSQLTimerStore creates a new PostgreSQL timer store
func NewPostgreSQLTimerStore(config *config.PostgreSQLConnectConfig) (databases.TimerStore, error) {
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		config.Host, config.Port, config.Username, config.Password, config.Database, config.SSLMode)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open PostgreSQL connection: %w", err)
	}

	// Test connection
	err = db.Ping()
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping PostgreSQL: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(config.MaxOpenConns)
	db.SetMaxIdleConns(config.MaxIdleConns)
	db.SetConnMaxLifetime(config.ConnMaxLifetime)

	store := &PostgreSQLTimerStore{
		db: db,
	}

	return store, nil
}

// Close closes the PostgreSQL connection
func (p *PostgreSQLTimerStore) Close() error {
	if p.db != nil {
		return p.db.Close()
	}
	return nil
}

func (p *PostgreSQLTimerStore) ClaimShardOwnership(
	ctx context.Context,
	shardId int,
	ownerAddr string,
) (prevShardInfo, currentShardInfo *databases.ShardInfo, err *databases.DbError) {
	// Use ZeroUUID for shard records
	zeroUuid := databases.ZeroUUID

	now := time.Now().UTC()

	// First, try to read the current shard record from unified timers table
	var currentVersion int64
	var currentOwnerAddr string
	var currentClaimedAt time.Time
	var currentMetadata string
	query := `SELECT shard_version, shard_owner_addr, shard_claimed_at, shard_metadata FROM timers 
	          WHERE shard_id = $1 AND row_type = $2 AND timer_execute_at = $3 AND timer_uuid = $4`

	readErr := p.db.QueryRowContext(ctx, query, shardId, databases.RowTypeShard,
		databases.ZeroTimestamp, zeroUuid).Scan(&currentVersion, &currentOwnerAddr, &currentClaimedAt, &currentMetadata)

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

		insertQuery := `INSERT INTO timers (shard_id, row_type, timer_execute_at, timer_uuid, 
		                                   shard_version, shard_owner_addr, shard_claimed_at, shard_metadata) 
		                VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

		_, insertErr := p.db.ExecContext(ctx, insertQuery,
			shardId, databases.RowTypeShard, databases.ZeroTimestamp, zeroUuid,
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
	updateQuery := `UPDATE timers SET shard_version = $1, shard_owner_addr = $2, shard_claimed_at = $3 
	                WHERE shard_id = $4 AND row_type = $5 AND timer_execute_at = $6 AND timer_uuid = $7 AND shard_version = $8`

	result, updateErr := p.db.ExecContext(ctx, updateQuery,
		newVersion, ownerAddr, now,
		shardId, databases.RowTypeShard, databases.ZeroTimestamp, zeroUuid, currentVersion)

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

func (p *PostgreSQLTimerStore) UpdateShardMetadata(
	ctx context.Context,
	shardId int,
	shardVersion int64,
	metadata databases.ShardMetadata,
) (err *databases.DbError) {
	// Use ZeroUUID for shard records
	zeroUuid := databases.ZeroUUID

	// Marshal metadata to JSON
	metadataJSON, marshalErr := json.Marshal(metadata)
	if marshalErr != nil {
		return databases.NewGenericDbError("failed to marshal shard metadata", marshalErr)
	}

	// Update shard metadata using optimistic concurrency control
	updateQuery := `UPDATE timers SET shard_metadata = $1, shard_updated_at = $2
	                WHERE shard_id = $3 AND row_type = $4 AND timer_execute_at = $5 AND timer_uuid = $6 AND shard_version = $7`

	result, updateErr := p.db.ExecContext(ctx, updateQuery,
		string(metadataJSON), time.Now().UTC(), shardId, databases.RowTypeShard, databases.ZeroTimestamp, zeroUuid, shardVersion)

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

// isDuplicateKeyError checks if the error is a PostgreSQL duplicate key error
func isDuplicateKeyError(err error) bool {
	var pqErr *pq.Error
	ok := errors.As(err, &pqErr)
	// PostgreSQL Error 23505 indicates a unique constraint violation
	return ok && pqErr.Code == "23505"
}

func (p *PostgreSQLTimerStore) CreateTimer(ctx context.Context, shardId int, shardVersion int64, namespace string, timer *databases.DbTimer) (err *databases.DbError) {
	// Use the provided timer UUID
	timerUuid := timer.TimerUuid

	// Use ZeroUUID for shard records
	zeroUuid := databases.ZeroUUID

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

	// Use PostgreSQL transaction to atomically check shard version and insert timer
	tx, txErr := p.db.BeginTx(ctx, nil)
	if txErr != nil {
		return databases.NewGenericDbError("failed to start PostgreSQL transaction", txErr)
	}
	defer tx.Rollback() // Will be no-op if transaction is committed

	// Read the shard to get current version within the transaction
	var actualShardVersion int64
	shardQuery := `SELECT shard_version FROM timers 
	               WHERE shard_id = $1 AND row_type = $2 AND timer_execute_at = $3 AND timer_uuid = $4`

	shardErr := tx.QueryRowContext(ctx, shardQuery, shardId, databases.RowTypeShard,
		databases.ZeroTimestamp, zeroUuid).Scan(&actualShardVersion)

	if shardErr != nil {
		if errors.Is(shardErr, sql.ErrNoRows) {
			// Shard doesn't exist
			return databases.NewDbErrorOnShardConditionFail("shard not found during timer creation", nil)
		}
		return databases.NewGenericDbError("failed to read shard", shardErr)
	}

	// Compare shard versions
	if actualShardVersion != shardVersion {
		// Version mismatch
		return databases.NewDbErrorOnShardConditionFail("shard version mismatch during timer creation", nil)
	}

	// Shard version matches, proceed to upsert timer within the transaction (overwrite if exists)
	upsertQuery := `INSERT INTO timers (shard_id, row_type, timer_execute_at, timer_uuid, 
	                                   timer_id, timer_namespace, timer_callback_url, 
	                                   timer_payload, timer_retry_policy, timer_callback_timeout_seconds,
	                                   timer_created_at, timer_attempts) 
	                VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	                ON CONFLICT (shard_id, row_type, timer_namespace, timer_id) DO UPDATE SET
	                  timer_execute_at = EXCLUDED.timer_execute_at,
	                  timer_uuid = EXCLUDED.timer_uuid,
	                  timer_callback_url = EXCLUDED.timer_callback_url,
	                  timer_payload = EXCLUDED.timer_payload,
	                  timer_retry_policy = EXCLUDED.timer_retry_policy,
	                  timer_callback_timeout_seconds = EXCLUDED.timer_callback_timeout_seconds,
	                  timer_created_at = EXCLUDED.timer_created_at,
	                  timer_attempts = EXCLUDED.timer_attempts`

	_, insertErr := tx.ExecContext(ctx, upsertQuery,
		shardId, databases.RowTypeTimer, timer.ExecuteAt, timerUuid,
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

func (p *PostgreSQLTimerStore) CreateTimerNoLock(ctx context.Context, shardId int, namespace string, timer *databases.DbTimer) (err *databases.DbError) {
	// Use the provided timer UUID
	timerUuid := timer.TimerUuid

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
	upsertQuery := `INSERT INTO timers (shard_id, row_type, timer_execute_at, timer_uuid, 
	                                   timer_id, timer_namespace, timer_callback_url, 
	                                   timer_payload, timer_retry_policy, timer_callback_timeout_seconds,
	                                   timer_created_at, timer_attempts) 
	                VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	                ON CONFLICT (shard_id, row_type, timer_namespace, timer_id) DO UPDATE SET
	                  timer_execute_at = EXCLUDED.timer_execute_at,
	                  timer_uuid = EXCLUDED.timer_uuid,
	                  timer_callback_url = EXCLUDED.timer_callback_url,
	                  timer_payload = EXCLUDED.timer_payload,
	                  timer_retry_policy = EXCLUDED.timer_retry_policy,
	                  timer_callback_timeout_seconds = EXCLUDED.timer_callback_timeout_seconds,
	                  timer_created_at = EXCLUDED.timer_created_at,
	                  timer_attempts = EXCLUDED.timer_attempts`

	_, insertErr := p.db.ExecContext(ctx, upsertQuery,
		shardId, databases.RowTypeTimer, timer.ExecuteAt, timerUuid,
		timer.Id, timer.Namespace, timer.CallbackUrl,
		payloadJSON, retryPolicyJSON, timer.CallbackTimeoutSeconds,
		timer.CreatedAt, timer.Attempts)

	if insertErr != nil {
		return databases.NewGenericDbError("failed to upsert timer", insertErr)
	}

	return nil
}

func (p *PostgreSQLTimerStore) RangeGetTimers(ctx context.Context, shardId int, request *databases.RangeGetTimersRequest) (*databases.RangeGetTimersResponse, *databases.DbError) {
	// Use start and end UUIDs directly for precise range selection
	startUuid := request.StartTimeUuid
	endUuid := request.EndTimeUuid

	// Query timers in the specified range, ordered by execution time and UUID
	query := `SELECT shard_id, row_type, timer_execute_at, timer_uuid,
	                 timer_id, timer_namespace, timer_callback_url, timer_payload, 
	                 timer_retry_policy, timer_callback_timeout_seconds, timer_created_at, timer_attempts
	          FROM timers 
	          WHERE shard_id = $1 AND row_type = $2 
	            AND (timer_execute_at, timer_uuid) >= ($3, $4)
	            AND (timer_execute_at, timer_uuid) <= ($5, $6)
	          ORDER BY timer_execute_at ASC, timer_uuid ASC
	          LIMIT $7`

	rows, err := p.db.QueryContext(ctx, query, shardId, databases.RowTypeTimer,
		request.StartTimestamp, startUuid,
		request.EndTimestamp, endUuid,
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
			dbTimerUuid                string
			dbTimerId                  string
			dbTimerNamespace           string
			dbTimerCallbackUrl         string
			dbTimerPayload             sql.NullString
			dbTimerRetryPolicy         sql.NullString
			dbTimerCallbackTimeoutSecs int32
			dbTimerCreatedAt           time.Time
			dbTimerAttempts            int32
		)

		if err := rows.Scan(&dbShardId, &dbRowType, &dbTimerExecuteAt, &dbTimerUuid,
			&dbTimerId, &dbTimerNamespace, &dbTimerCallbackUrl, &dbTimerPayload,
			&dbTimerRetryPolicy, &dbTimerCallbackTimeoutSecs, &dbTimerCreatedAt, &dbTimerAttempts); err != nil {
			return nil, databases.NewGenericDbError("failed to scan timer row", err)
		}

		// Parse UUID from string
		timerUuid, uuidErr := uuid.Parse(dbTimerUuid)
		if uuidErr != nil {
			return nil, databases.NewGenericDbError("failed to parse timer UUID", uuidErr)
		}

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

func (c *PostgreSQLTimerStore) RangeDeleteWithBatchInsertTxn(ctx context.Context, shardId int, shardVersion int64, request *databases.RangeDeleteTimersRequest, TimersToInsert []*databases.DbTimer) (*databases.RangeDeleteTimersResponse, *databases.DbError) {
	// Use start and end UUIDs directly for precise range selection
	startUuid := request.StartTimeUuid
	endUuid := request.EndTimeUuid

	// Use ZeroUUID for shard records
	zeroUuid := databases.ZeroUUID

	// Use PostgreSQL transaction to atomically check shard version, delete timers, and insert new ones
	tx, txErr := c.db.BeginTx(ctx, nil)
	if txErr != nil {
		return nil, databases.NewGenericDbError("failed to start PostgreSQL transaction", txErr)
	}
	defer tx.Rollback() // Will be no-op if transaction is committed

	// Read the shard to verify version within the transaction
	var actualShardVersion int64
	shardQuery := `SELECT shard_version FROM timers 
	               WHERE shard_id = $1 AND row_type = $2 AND timer_execute_at = $3 AND timer_uuid = $4`

	shardErr := tx.QueryRowContext(ctx, shardQuery, shardId, databases.RowTypeShard,
		databases.ZeroTimestamp, zeroUuid).Scan(&actualShardVersion)

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
		return nil, databases.NewDbErrorOnShardConditionFail("shard version mismatch during delete and insert operation", nil)
	}

	// Delete timers in the specified range
	deleteQuery := `DELETE FROM timers 
	                WHERE shard_id = $1 AND row_type = $2 
	                AND (timer_execute_at, timer_uuid) >= ($3, $4)
	                AND (timer_execute_at, timer_uuid) <= ($5, $6)`

	deleteResult, deleteErr := tx.ExecContext(ctx, deleteQuery, shardId, databases.RowTypeTimer,
		request.StartTimestamp, startUuid,
		request.EndTimestamp, endUuid)

	if deleteErr != nil {
		return nil, databases.NewGenericDbError("failed to delete timers", deleteErr)
	}

	// Get actual deleted count from PostgreSQL
	rowsAffected, rowsErr := deleteResult.RowsAffected()
	if rowsErr != nil {
		return nil, databases.NewGenericDbError("failed to get deleted rows count", rowsErr)
	}
	deletedCount := int(rowsAffected)

	// Insert new timers
	for _, timer := range TimersToInsert {
		timerUuid := timer.TimerUuid

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

		insertQuery := `INSERT INTO timers (shard_id, row_type, timer_execute_at, timer_uuid, 
		                                   timer_id, timer_namespace, timer_callback_url, timer_payload, 
		                                   timer_retry_policy, timer_callback_timeout_seconds, timer_created_at, timer_attempts)
		                VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`

		_, insertErr := tx.ExecContext(ctx, insertQuery,
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
			timer.Attempts,
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

func (c *PostgreSQLTimerStore) RangeDeleteWithLimit(ctx context.Context, shardId int, request *databases.RangeDeleteTimersRequest, limit int) (*databases.RangeDeleteTimersResponse, *databases.DbError) {
	startUuid := request.StartTimeUuid
	endUuid := request.EndTimeUuid

	deleteQuery := `DELETE FROM timers 
	                WHERE ctid IN (
	                    SELECT ctid FROM timers 
	                    WHERE shard_id = $1 AND row_type = $2 
	                    AND (timer_execute_at, timer_uuid) >= ($3, $4)
	                    AND (timer_execute_at, timer_uuid) <= ($5, $6)
	                    ORDER BY timer_execute_at, timer_uuid
	                    LIMIT $7
	                )`

	deleteResult, deleteErr := c.db.ExecContext(ctx, deleteQuery, shardId, databases.RowTypeTimer,
		request.StartTimestamp, startUuid,
		request.EndTimestamp, endUuid, limit)

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

func (c *PostgreSQLTimerStore) UpdateTimer(ctx context.Context, shardId int, shardVersion int64, namespace string, request *databases.UpdateDbTimerRequest) (err *databases.DbError) {
	// Use ZeroUUID for shard records
	zeroUuid := databases.ZeroUUID

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

	// Use PostgreSQL transaction for atomic operation
	tx, txErr := c.db.BeginTx(ctx, nil)
	if txErr != nil {
		return databases.NewGenericDbError("failed to start PostgreSQL transaction", txErr)
	}
	defer tx.Rollback() // Will be no-op if transaction is committed

	// Check shard version within the transaction
	var actualShardVersion int64
	shardQuery := `SELECT shard_version FROM timers 
	               WHERE shard_id = $1 AND row_type = $2 AND timer_execute_at = $3 AND timer_uuid = $4`

	shardErr := tx.QueryRowContext(ctx, shardQuery, shardId, databases.RowTypeShard,
		databases.ZeroTimestamp, zeroUuid).Scan(&actualShardVersion)

	if shardErr != nil {
		if errors.Is(shardErr, sql.ErrNoRows) {
			// Shard doesn't exist
			return databases.NewDbErrorOnShardConditionFail("shard not found during timer update", nil)
		}
		return databases.NewGenericDbError("failed to read shard", shardErr)
	}

	// Compare shard versions
	if actualShardVersion != shardVersion {
		// Version mismatch
		return databases.NewDbErrorOnShardConditionFail("shard version mismatch during timer update", nil)
	}

	// Get current timer to preserve certain fields and check if ExecuteAt changed
	var currentExecuteAt time.Time
	var currentTimerUuidStr string
	var currentCreatedAt time.Time
	var currentAttempts int32

	timerQuery := `SELECT timer_execute_at, timer_uuid, timer_created_at, timer_attempts
	               FROM timers 
	               WHERE shard_id = $1 AND row_type = $2 AND timer_namespace = $3 AND timer_id = $4`

	timerErr := tx.QueryRowContext(ctx, timerQuery, shardId, databases.RowTypeTimer, namespace, request.TimerId).
		Scan(&currentExecuteAt, &currentTimerUuidStr, &currentCreatedAt, &currentAttempts)

	if timerErr != nil {
		if errors.Is(timerErr, sql.ErrNoRows) {
			return databases.NewDbErrorNotExists("timer not found for update", timerErr)
		}
		return databases.NewGenericDbError("failed to read current timer", timerErr)
	}

	// Check if ExecuteAt changed - if so, we need to delete and insert with new primary key
	if !currentExecuteAt.Equal(request.ExecuteAt) {
		// Parse current UUID and use it for the new record
		currentTimerUuid, parseErr := uuid.Parse(currentTimerUuidStr)
		if parseErr != nil {
			return databases.NewGenericDbError("failed to parse current timer UUID", parseErr)
		}

		// Delete old record
		deleteQuery := `DELETE FROM timers 
		               WHERE shard_id = $1 AND row_type = $2 AND timer_execute_at = $3 AND timer_uuid = $4`

		_, deleteErr := tx.ExecContext(ctx, deleteQuery, shardId, databases.RowTypeTimer,
			currentExecuteAt, currentTimerUuid)

		if deleteErr != nil {
			return databases.NewGenericDbError("failed to delete timer for update", deleteErr)
		}

		// Insert new record with updated ExecuteAt and same UUID
		insertQuery := `INSERT INTO timers (shard_id, row_type, timer_execute_at, timer_uuid, 
		                                   timer_id, timer_namespace, timer_callback_url, timer_payload, 
		                                   timer_retry_policy, timer_callback_timeout_seconds, timer_created_at, 
		                                   timer_attempts) 
		                VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`

		_, insertErr := tx.ExecContext(ctx, insertQuery,
			shardId, databases.RowTypeTimer, request.ExecuteAt, currentTimerUuid,
			request.TimerId, namespace, request.CallbackUrl, payloadJSON, retryPolicyJSON,
			request.CallbackTimeoutSeconds, currentCreatedAt, currentAttempts)

		if insertErr != nil {
			return databases.NewGenericDbError("failed to insert updated timer", insertErr)
		}
	} else {
		// ExecuteAt didn't change, we can do a simple UPDATE
		updateQuery := `UPDATE timers 
		               SET timer_callback_url = $1, timer_payload = $2, timer_retry_policy = $3, 
		                   timer_callback_timeout_seconds = $4
		               WHERE shard_id = $5 AND row_type = $6 AND timer_namespace = $7 AND timer_id = $8`

		result, updateErr := tx.ExecContext(ctx, updateQuery,
			request.CallbackUrl, payloadJSON, retryPolicyJSON, request.CallbackTimeoutSeconds,
			shardId, databases.RowTypeTimer, namespace, request.TimerId)

		if updateErr != nil {
			return databases.NewGenericDbError("failed to update timer", updateErr)
		}

		rowsAffected, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return databases.NewGenericDbError("failed to get rows affected", rowsErr)
		}

		if rowsAffected == 0 {
			return databases.NewDbErrorNotExists("timer not found for update", nil)
		}
	}

	// Commit the transaction
	if commitErr := tx.Commit(); commitErr != nil {
		return databases.NewGenericDbError("failed to commit transaction", commitErr)
	}

	return nil
}

func (c *PostgreSQLTimerStore) GetTimer(ctx context.Context, shardId int, namespace string, timerId string) (timer *databases.DbTimer, err *databases.DbError) {
	// Query timer by shard_id, namespace, and timer_id using the unique index
	query := `SELECT shard_id, row_type, timer_execute_at, timer_uuid,
	                 timer_id, timer_namespace, timer_callback_url, timer_payload, 
	                 timer_retry_policy, timer_callback_timeout_seconds, timer_created_at, timer_attempts
	          FROM timers 
	          WHERE shard_id = $1 AND row_type = $2 AND timer_namespace = $3 AND timer_id = $4`

	var (
		dbShardId                  int
		dbRowType                  int16
		dbTimerExecuteAt           time.Time
		dbTimerUuidStr             string
		dbTimerId                  string
		dbTimerNamespace           string
		dbTimerCallbackUrl         string
		dbTimerPayload             sql.NullString
		dbTimerRetryPolicy         sql.NullString
		dbTimerCallbackTimeoutSecs int32
		dbTimerCreatedAt           time.Time
		dbTimerAttempts            int32
	)

	queryErr := c.db.QueryRowContext(ctx, query, shardId, databases.RowTypeTimer, namespace, timerId).
		Scan(&dbShardId, &dbRowType, &dbTimerExecuteAt, &dbTimerUuidStr,
			&dbTimerId, &dbTimerNamespace, &dbTimerCallbackUrl, &dbTimerPayload,
			&dbTimerRetryPolicy, &dbTimerCallbackTimeoutSecs, &dbTimerCreatedAt, &dbTimerAttempts)

	if queryErr != nil {
		if errors.Is(queryErr, sql.ErrNoRows) {
			return nil, databases.NewDbErrorNotExists("timer not found", queryErr)
		}
		return nil, databases.NewGenericDbError("failed to query timer", queryErr)
	}

	// Parse UUID from string
	timerUuid, parseErr := uuid.Parse(dbTimerUuidStr)
	if parseErr != nil {
		return nil, databases.NewGenericDbError("failed to parse timer UUID", parseErr)
	}

	// Parse JSON payload and retry policy
	var payload interface{}
	var retryPolicy interface{}

	if dbTimerPayload.Valid && dbTimerPayload.String != "" {
		if unmarshalErr := json.Unmarshal([]byte(dbTimerPayload.String), &payload); unmarshalErr != nil {
			return nil, databases.NewGenericDbError("failed to unmarshal timer payload", unmarshalErr)
		}
	}

	if dbTimerRetryPolicy.Valid && dbTimerRetryPolicy.String != "" {
		if unmarshalErr := json.Unmarshal([]byte(dbTimerRetryPolicy.String), &retryPolicy); unmarshalErr != nil {
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

func (c *PostgreSQLTimerStore) DeleteTimer(ctx context.Context, shardId int, shardVersion int64, namespace string, timerId string) *databases.DbError {
	// Use ZeroUUID for shard records
	zeroUuid := databases.ZeroUUID

	// Use PostgreSQL transaction for atomic operation
	tx, txErr := c.db.BeginTx(ctx, nil)
	if txErr != nil {
		return databases.NewGenericDbError("failed to start PostgreSQL transaction", txErr)
	}
	defer tx.Rollback() // Will be no-op if transaction is committed

	// Check shard version within the transaction
	var actualShardVersion int64
	shardQuery := `SELECT shard_version FROM timers 
	               WHERE shard_id = $1 AND row_type = $2 AND timer_execute_at = $3 AND timer_uuid = $4`

	shardErr := tx.QueryRowContext(ctx, shardQuery, shardId, databases.RowTypeShard,
		databases.ZeroTimestamp, zeroUuid).Scan(&actualShardVersion)

	if shardErr != nil {
		if errors.Is(shardErr, sql.ErrNoRows) {
			// Shard doesn't exist
			return databases.NewDbErrorOnShardConditionFail("shard not found during timer delete", nil)
		}
		return databases.NewGenericDbError("failed to read shard", shardErr)
	}

	// Compare shard versions
	if actualShardVersion != shardVersion {
		// Version mismatch
		return databases.NewDbErrorOnShardConditionFail("shard version mismatch during timer delete", nil)
	}

	// Delete the timer using the unique index (optimized - no lookup needed)
	deleteQuery := `DELETE FROM timers 
	               WHERE shard_id = $1 AND row_type = $2 AND timer_namespace = $3 AND timer_id = $4`

	result, deleteErr := tx.ExecContext(ctx, deleteQuery, shardId, databases.RowTypeTimer, namespace, timerId)

	if deleteErr != nil {
		return databases.NewGenericDbError("failed to delete timer", deleteErr)
	}

	// Check how many rows were affected to determine if timer existed
	rowsAffected, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return databases.NewGenericDbError("failed to get rows affected", rowsErr)
	}

	// Check if timer actually existed and was deleted
	// rowsAffected == 0 means timer didn't exist, rowsAffected == 1 means timer was deleted
	if rowsAffected == 0 {
		return databases.NewDbErrorNotExists("timer not found", nil)
	}

	// Commit the transaction
	if commitErr := tx.Commit(); commitErr != nil {
		return databases.NewGenericDbError("failed to commit transaction", commitErr)
	}

	return nil
}
