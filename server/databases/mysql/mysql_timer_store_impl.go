package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-sql-driver/mysql"
	"time"

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
	ctx context.Context, shardId int, ownerId string, metadata interface{},
) (shardVersion int64, retErr *databases.DbError) {
	// Serialize metadata to JSON
	var metadataJSON interface{}
	if metadata != nil {
		metadataBytes, err := json.Marshal(metadata)
		if err != nil {
			return 0, databases.NewGenericDbError("failed to marshal metadata", err)
		}
		metadataJSON = string(metadataBytes)
	}

	now := time.Now()

	// First, try to read the current shard record from unified timers table
	var currentVersion int64
	var currentOwnerId string
	query := `SELECT shard_version, shard_owner_id FROM timers 
	          WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid = ?`

	err := m.db.QueryRowContext(ctx, query, shardId, databases.RowTypeShard,
		databases.ZeroTimestamp, databases.ZeroUUIDString).Scan(&currentVersion, &currentOwnerId)

	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, databases.NewGenericDbError("failed to read shard record", err)
	}

	if errors.Is(err, sql.ErrNoRows) {
		// Shard doesn't exist, create it with version 1
		newVersion := int64(1)
		insertQuery := `INSERT INTO timers (shard_id, row_type, timer_execute_at, timer_uuid, 
		                                   shard_version, shard_owner_id, shard_claimed_at, shard_metadata,
		                                   timer_created_at, timer_attempts) 
		                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

		_, err := m.db.ExecContext(ctx, insertQuery,
			shardId, databases.RowTypeShard, databases.ZeroTimestamp, databases.ZeroUUIDString,
			newVersion, ownerId, now, metadataJSON, now, 0)

		if err != nil {
			// Check if it's a duplicate key error (another instance created it concurrently)
			if isDuplicateKeyError(err) {
				// Try to read the existing record to return conflict info
				var conflictVersion int64
				var conflictOwnerId string
				var conflictClaimedAt time.Time
				var conflictMetadata string

				conflictQuery := `SELECT shard_version, shard_owner_id, shard_claimed_at, shard_metadata 
				                  FROM timers WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid = ?`

				conflictErr := m.db.QueryRowContext(ctx, conflictQuery, shardId, databases.RowTypeShard,
					databases.ZeroTimestamp, databases.ZeroUUIDString).Scan(&conflictVersion, &conflictOwnerId, &conflictClaimedAt, &conflictMetadata)

				if conflictErr == nil {
					conflictInfo := &databases.ShardInfo{
						ShardId:      int64(shardId),
						OwnerId:      conflictOwnerId,
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
	updateQuery := `UPDATE timers SET shard_version = ?, shard_owner_id = ?, shard_claimed_at = ?, shard_metadata = ? 
	                WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid = ? AND shard_version = ?`

	result, err := m.db.ExecContext(ctx, updateQuery,
		newVersion, ownerId, now, metadataJSON,
		shardId, databases.RowTypeShard, databases.ZeroTimestamp, databases.ZeroUUIDString, currentVersion)

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

func (m *MySQLTimerStore) CreateTimer(ctx context.Context, shardId int, shardVersion int64, timer *databases.DbTimer) (err *databases.DbError) {
	//TODO implement me
	panic("implement me")
}

func (m *MySQLTimerStore) GetTimersUpToTimestamp(ctx context.Context, shardId int, request *databases.RangeGetTimersRequest) (*databases.RangeGetTimersResponse, *databases.DbError) {
	//TODO implement me
	panic("implement me")
}

func (m *MySQLTimerStore) DeleteTimersUpToTimestamp(ctx context.Context, shardId int, shardVersion int64, request *databases.RangeDeleteTimersRequest) (*databases.RangeDeleteTimersResponse, *databases.DbError) {
	//TODO implement me
	panic("implement me")
}

func (m *MySQLTimerStore) UpdateTimer(ctx context.Context, shardId int, shardVersion int64, timerId string, request *databases.UpdateDbTimerRequest) (err *databases.DbError) {
	//TODO implement me
	panic("implement me")
}

func (m *MySQLTimerStore) GetTimer(ctx context.Context, shardId int, timerId string) (timer *databases.DbTimer, err *databases.DbError) {
	//TODO implement me
	panic("implement me")
}

func (m *MySQLTimerStore) DeleteTimer(ctx context.Context, shardId int, shardVersion int64, timerId string) *databases.DbError {
	//TODO implement me
	panic("implement me")
}
