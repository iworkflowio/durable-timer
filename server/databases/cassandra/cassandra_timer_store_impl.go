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
	ctx context.Context, shardId int, ownerId string, metadata interface{},
) (shardVersion int64, retErr *databases.DbError) {
	// Serialize metadata to JSON
	var metadataJSON string
	if metadata != nil {
		metadataBytes, err := json.Marshal(metadata)
		if err != nil {
			return 0, databases.NewGenericDbError("failed to marshal metadata", err)
		}
		metadataJSON = string(metadataBytes)
	}

	now := time.Now()

	// First, try to read the current shard record
	var currentVersion int64
	var currentOwnerId string
	query := `SELECT version, owner_id FROM shards WHERE shard_id = ?`
	err := c.session.Query(query, shardId).WithContext(ctx).Scan(&currentVersion, &currentOwnerId)

	if err != nil && !errors.Is(err, gocql.ErrNotFound) {
		return 0, databases.NewGenericDbError("failed to read shard record: %w", err)
	}

	if errors.Is(err, gocql.ErrNotFound) {
		// Shard doesn't exist, create it with version 1
		newVersion := int64(1)
		insertQuery := `INSERT INTO shards (shard_id, version, owner_id, claimed_at, metadata) 
		                VALUES (?, ?, ?, ?, ?) IF NOT EXISTS`

		// When CAS fails, Cassandra returns the existing row values
		var conflictShardId int64
		var conflictVersion int64
		var conflictOwnerId string
		var conflictClaimedAt time.Time
		var conflictMetadata string

		applied, err := c.session.Query(insertQuery, shardId, newVersion, ownerId, now, metadataJSON).
			WithContext(ctx).ScanCAS(&conflictShardId, &conflictVersion, &conflictOwnerId, &conflictClaimedAt, &conflictMetadata)

		if err != nil {
			return 0, databases.NewGenericDbError("failed to insert new shard record", err)
		}

		if !applied {
			// Another instance created the record concurrently, return conflict info
			conflictInfo := &databases.ShardInfo{
				ShardId:   conflictShardId,
				OwnerId:   conflictOwnerId,
				ClaimedAt: conflictClaimedAt,
				Metadata:  conflictMetadata,
			}
			return 0, databases.NewDbErrorOnShardConditionFail("failed to insert shard record due to concurrent insert", nil, conflictInfo)
		} else {
			// Successfully created new record
			return newVersion, nil
		}
	}

	// Update the shard with new version and ownership using optimistic concurrency control
	newVersion := currentVersion + 1
	updateQuery := `UPDATE shards SET version = ?, owner_id = ?, claimed_at = ?, metadata = ? 
	                WHERE shard_id = ? IF version = ?`

	// When CAS fails, Cassandra returns the current row values
	var updateConflictShardId int64
	var updateConflictVersion int64
	var updateConflictOwnerId string
	var updateConflictClaimedAt time.Time
	var updateConflictMetadata string

	applied, err := c.session.Query(updateQuery, newVersion, ownerId, now, metadataJSON, shardId, currentVersion).
		WithContext(ctx).ScanCAS(&updateConflictShardId, &updateConflictVersion, &updateConflictOwnerId, &updateConflictClaimedAt, &updateConflictMetadata)

	if err != nil {
		return 0, databases.NewGenericDbError("failed to update shard record", err)
	}

	if !applied {
		// Version changed concurrently, return conflict info
		conflictInfo := &databases.ShardInfo{
			ShardId:   updateConflictShardId,
			OwnerId:   updateConflictOwnerId,
			ClaimedAt: updateConflictClaimedAt,
			Metadata:  updateConflictMetadata,
		}
		return 0, databases.NewDbErrorOnShardConditionFail("shard ownership claim failed due to concurrent modification", nil, conflictInfo)
	}

	return newVersion, nil
}

func (c *CassandraTimerStore) CreateTimer(ctx context.Context, shardId int, shardVersion int64, timer *databases.DbTimer) (err *databases.DbError) {
	//TODO implement me
	panic("implement me")
}

func (c *CassandraTimerStore) GetTimersUpToTimestamp(ctx context.Context, shardId int, request *databases.RangeGetTimersRequest) (*databases.RangeGetTimersResponse, *databases.DbError) {
	//TODO implement me
	panic("implement me")
}

func (c *CassandraTimerStore) DeleteTimersUpToTimestamp(ctx context.Context, shardId int, shardVersion int64, request *databases.RangeDeleteTimersRequest) (*databases.RangeDeleteTimersResponse, *databases.DbError) {
	//TODO implement me
	panic("implement me")
}

func (c *CassandraTimerStore) UpdateTimer(ctx context.Context, shardId int, shardVersion int64, timerId string, request *databases.UpdateDbTimerRequest) (err *databases.DbError) {
	//TODO implement me
	panic("implement me")
}

func (c *CassandraTimerStore) GetTimer(ctx context.Context, shardId int, timerId string) (timer *databases.DbTimer, err *databases.DbError) {
	//TODO implement me
	panic("implement me")
}

func (c *CassandraTimerStore) DeleteTimer(ctx context.Context, shardId int, shardVersion int64, timerId string) *databases.DbError {
	//TODO implement me
	panic("implement me")
}
