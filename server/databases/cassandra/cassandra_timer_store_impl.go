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
	// When CAS fails, Cassandra returns the existing row values
	previous := make(map[string]interface{})

	// First, try to read the current shard record from unified timers table
	var currentVersion int64
	var currentOwnerId string
	query := `SELECT shard_version, shard_owner_id FROM timers WHERE shard_id = ? AND row_type = ?`
	err := c.session.Query(query, shardId, databases.RowTypeShard).WithContext(ctx).Scan(&currentVersion, &currentOwnerId)

	if err != nil && !errors.Is(err, gocql.ErrNotFound) {
		return 0, databases.NewGenericDbError("failed to read shard record: %w", err)
	}

	if errors.Is(err, gocql.ErrNotFound) {
		// Shard doesn't exist, create it with version 1
		newVersion := int64(1)

		insertQuery := `INSERT INTO timers (shard_id, row_type, timer_execute_at, timer_uuid, shard_version, shard_owner_id, shard_claimed_at, shard_metadata) 
		                VALUES (?, ?, ?, ?, ?, ?, ?, ?) IF NOT EXISTS`

		applied, err := c.session.Query(insertQuery, shardId, databases.RowTypeShard, databases.ZeroTimestamp, databases.ZeroUUID, newVersion, ownerId, now, metadataJSON).
			WithContext(ctx).MapScanCAS(previous)

		if err != nil {
			return 0, databases.NewGenericDbError("failed to insert new shard record", err)
		}

		if !applied {
			// Another instance created the record concurrently, return conflict info
			conflictInfo := &databases.ShardInfo{
				ShardId:      int64(previous["shard_id"].(int)),
				OwnerId:      previous["shard_owner_id"].(string),
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
	updateQuery := `UPDATE timers SET shard_version = ?, shard_owner_id = ?, shard_claimed_at = ?, shard_metadata = ? 
	                WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid = ? IF shard_version = ?`

	applied, err := c.session.Query(updateQuery, newVersion, ownerId, now, metadataJSON,
		shardId, databases.RowTypeShard, databases.ZeroTimestamp, databases.ZeroUUID, currentVersion).
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
