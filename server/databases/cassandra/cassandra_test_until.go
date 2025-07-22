package cassandra

import (
	"fmt"
	"github.com/gocql/gocql"
	"github.com/iworkflowio/durable-timer/config"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

const (
	testKeyspace = "timer_test"
	testHost     = "localhost:9042"
)

// setupTestStore creates a test store with a clean test keyspace
func setupTestStore(t *testing.T) (*CassandraTimerStore, func()) {
	// Try to connect to Cassandra
	cluster := gocql.NewCluster(testHost)
	cluster.Timeout = 5 * time.Second
	cluster.ConnectTimeout = 5 * time.Second

	session, err := cluster.CreateSession()
	if err != nil {
		t.Skip("Cassandra not available, skipping integration tests:", err)
		return nil, nil
	}
	session.Close()

	// Create test keyspace and tables
	err = createTestKeyspace()
	if err != nil {
		t.Fatal("Failed to create test keyspace:", err)
	}

	// Create store with test configuration
	config := &config.CassandraConnectConfig{
		Hosts:       []string{testHost},
		Keyspace:    testKeyspace,
		Consistency: gocql.Quorum,
		Timeout:     10 * time.Second,
	}

	store, err := NewCassandraTimerStore(config)
	require.NoError(t, err)
	cassandraStore := store.(*CassandraTimerStore)

	// Cleanup function
	cleanup := func() {
		cassandraStore.Close()
		dropTestKeyspace()
	}

	return cassandraStore, cleanup
}

func createTestKeyspace() error {
	cluster := gocql.NewCluster(testHost)
	cluster.Timeout = 5 * time.Second
	session, err := cluster.CreateSession()
	if err != nil {
		return err
	}
	defer session.Close()

	// Drop keyspace if exists
	err = session.Query(fmt.Sprintf("DROP KEYSPACE IF EXISTS %s", testKeyspace)).Exec()
	if err != nil {
		return err
	}

	// Create keyspace
	createKeyspaceQuery := fmt.Sprintf(`
		CREATE KEYSPACE %s 
		WITH REPLICATION = {
			'class': 'SimpleStrategy',
			'replication_factor': 1
		}`, testKeyspace)

	err = session.Query(createKeyspaceQuery).Exec()
	if err != nil {
		return err
	}

	// Switch to test keyspace
	cluster.Keyspace = testKeyspace
	testSession, err := cluster.CreateSession()
	if err != nil {
		return err
	}
	defer testSession.Close()

	// Create shards table
	createShardsTable := `
		CREATE TABLE shards (
			shard_id int,
			version bigint,
			owner_id text,
			claimed_at timestamp,
			metadata text,
			PRIMARY KEY (shard_id)
		)`

	err = testSession.Query(createShardsTable).Exec()
	if err != nil {
		return err
	}

	return nil
}

func dropTestKeyspace() {
	cluster := gocql.NewCluster(testHost)
	cluster.Timeout = 5 * time.Second
	session, err := cluster.CreateSession()
	if err != nil {
		return
	}
	defer session.Close()

	session.Query(fmt.Sprintf("DROP KEYSPACE IF EXISTS %s", testKeyspace)).Exec()
}
