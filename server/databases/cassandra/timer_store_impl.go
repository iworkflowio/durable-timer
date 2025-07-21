package cassandra

import (
	"context"
	"fmt"
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

func (c *CassandraTimerStore) ClaimShardOwnership(ctx context.Context, shardId int, ownerId string, metadata interface{}) (shardVersion int64, err error) {
	//TODO implement me
	panic("implement me")
}

func (c *CassandraTimerStore) CreateTimer(ctx context.Context, shardId int, shardVersion int64, timer *databases.DbTimer) (err error) {
	//TODO implement me
	panic("implement me")
}

func (c *CassandraTimerStore) GetTimersUpToTimestamp(ctx context.Context, shardId int, request *databases.RangeGetTimersRequest) (*databases.RangeGetTimersResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (c *CassandraTimerStore) DeleteTimersUpToTimestamp(ctx context.Context, shardId int, shardVersion int64, request *databases.RangeDeleteTimersRequest) (*databases.RangeDeleteTimersResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (c *CassandraTimerStore) UpdateTimer(ctx context.Context, shardId int, shardVersion int64, timerId string, request *databases.UpdateDbTimerRequest) (notExists bool, err error) {
	//TODO implement me
	panic("implement me")
}

func (c *CassandraTimerStore) GetTimer(ctx context.Context, shardId int, timerId string) (timer *databases.DbTimer, notExists bool, err error) {
	//TODO implement me
	panic("implement me")
}

func (c *CassandraTimerStore) DeleteTimer(ctx context.Context, shardId int, shardVersion int64, timerId string) error {
	//TODO implement me
	panic("implement me")
}
