package cassandra

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gocql/gocql"
	"github.com/iworkflowio/durable-timer/config"
	"github.com/iworkflowio/durable-timer/databases"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func cleanupShards(store *CassandraTimerStore) {
	store.session.Query("TRUNCATE shards").Exec()
}

func TestClaimShardOwnership_NewShard(t *testing.T) {
	store, cleanup := setupTestStore(t)
	if store == nil {
		return // Cassandra not available
	}
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	ownerId := "owner-1"
	metadata := map[string]interface{}{
		"instanceId": "instance-1",
		"region":     "us-west-2",
	}

	// Claim ownership of a new shard
	version, err := store.ClaimShardOwnership(ctx, shardId, ownerId, metadata)

	assert.Nil(t, err)
	assert.Equal(t, int64(1), version, "New shard should start with version 1")

	// Verify the record was created correctly
	var dbVersion int64
	var dbOwnerId string
	var dbMetadata string
	var dbClaimedAt time.Time

	query := "SELECT version, owner_id, metadata, claimed_at FROM shards WHERE shard_id = ?"
	scanErr := store.session.Query(query, shardId).Scan(&dbVersion, &dbOwnerId, &dbMetadata, &dbClaimedAt)

	require.NoError(t, scanErr)
	assert.Equal(t, int64(1), dbVersion)
	assert.Equal(t, ownerId, dbOwnerId)
	assert.Contains(t, dbMetadata, "instance-1")
	assert.Contains(t, dbMetadata, "us-west-2")
	assert.True(t, time.Since(dbClaimedAt) < 5*time.Second, "claimed_at should be recent")
}

func TestClaimShardOwnership_ExistingShard(t *testing.T) {
	store, cleanup := setupTestStore(t)
	if store == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	shardId := 2

	// First claim
	version1, err1 := store.ClaimShardOwnership(ctx, shardId, "owner-1", map[string]string{"key": "value1"})
	assert.Nil(t, err1)
	assert.Equal(t, int64(1), version1)

	// Second claim by different owner
	version2, err2 := store.ClaimShardOwnership(ctx, shardId, "owner-2", map[string]string{"key": "value2"})
	assert.Nil(t, err2)
	assert.Equal(t, int64(2), version2)

	// Third claim by original owner
	version3, err3 := store.ClaimShardOwnership(ctx, shardId, "owner-1", map[string]string{"key": "value3"})
	assert.Nil(t, err3)
	assert.Equal(t, int64(3), version3)

	// Verify final state
	var dbVersion int64
	var dbOwnerId string
	query := "SELECT version, owner_id FROM shards WHERE shard_id = ?"
	scanErr := store.session.Query(query, shardId).Scan(&dbVersion, &dbOwnerId)

	require.NoError(t, scanErr)
	assert.Equal(t, int64(3), dbVersion)
	assert.Equal(t, "owner-1", dbOwnerId)
}

func TestClaimShardOwnership_ConcurrentClaims(t *testing.T) {
	store, cleanup := setupTestStore(t)
	if store == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	shardId := 3
	numGoroutines := 10

	var wg sync.WaitGroup
	results := make([]struct {
		version int64
		err     *databases.DbError
		ownerId string
	}, numGoroutines)

	// Launch concurrent claims
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ownerId := fmt.Sprintf("owner-%d", idx)
			version, err := store.ClaimShardOwnership(ctx, shardId, ownerId, map[string]int{"attempt": idx})
			results[idx] = struct {
				version int64
				err     *databases.DbError
				ownerId string
			}{version, err, ownerId}
		}(i)
	}

	wg.Wait()

	// Analyze results
	successCount := 0
	var maxVersion int64
	var lastSuccessfulOwner string

	for _, result := range results {
		if result.err == nil {
			successCount++
			if result.version > maxVersion {
				maxVersion = result.version
				lastSuccessfulOwner = result.ownerId
			}
		}
	}

	// All goroutines should either succeed or fail, but we should have at least some successes
	assert.Greater(t, successCount, 0, "At least one claim should succeed")
	assert.Greater(t, maxVersion, int64(0), "Maximum version should be positive")

	// Verify final database state
	var dbVersion int64
	var dbOwnerId string
	query := "SELECT version, owner_id FROM shards WHERE shard_id = ?"
	scanErr := store.session.Query(query, shardId).Scan(&dbVersion, &dbOwnerId)

	require.NoError(t, scanErr)
	assert.Equal(t, maxVersion, dbVersion, "Database version should match highest successful claim")
	assert.Equal(t, lastSuccessfulOwner, dbOwnerId, "Database owner should match last successful claimer")
}

func TestClaimShardOwnership_NilMetadata(t *testing.T) {
	store, cleanup := setupTestStore(t)
	if store == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	shardId := 4
	ownerId := "owner-nil-metadata"

	// Claim with nil metadata
	version, err := store.ClaimShardOwnership(ctx, shardId, ownerId, nil)

	assert.Nil(t, err)
	assert.Equal(t, int64(1), version)

	// Verify metadata is empty/null in database
	var dbMetadata *string
	query := "SELECT metadata FROM shards WHERE shard_id = ?"
	scanErr := store.session.Query(query, shardId).Scan(&dbMetadata)

	require.NoError(t, scanErr)
	// Should be empty string or null
	if dbMetadata != nil {
		assert.Equal(t, "", *dbMetadata)
	}
}

func TestClaimShardOwnership_ComplexMetadata(t *testing.T) {
	store, cleanup := setupTestStore(t)
	if store == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	shardId := 5
	ownerId := "owner-complex"

	complexMetadata := map[string]interface{}{
		"instanceId": "i-1234567890abcdef0",
		"region":     "us-west-2",
		"zone":       "us-west-2a",
		"config": map[string]interface{}{
			"maxConnections": 100,
			"timeout":        30.5,
			"enabled":        true,
		},
		"tags": []string{"production", "timer-service"},
	}

	// Claim with complex metadata
	version, err := store.ClaimShardOwnership(ctx, shardId, ownerId, complexMetadata)

	assert.Nil(t, err)
	assert.Equal(t, int64(1), version)

	// Verify metadata is properly serialized
	var dbMetadata string
	query := "SELECT metadata FROM shards WHERE shard_id = ?"
	scanErr := store.session.Query(query, shardId).Scan(&dbMetadata)

	require.NoError(t, scanErr)
	assert.Contains(t, dbMetadata, "i-1234567890abcdef0")
	assert.Contains(t, dbMetadata, "us-west-2")
	assert.Contains(t, dbMetadata, "maxConnections")
	assert.Contains(t, dbMetadata, "production")
}

func TestClaimShardOwnership_InvalidMetadata(t *testing.T) {
	store, cleanup := setupTestStore(t)
	if store == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	shardId := 6
	ownerId := "owner-invalid"

	// Use metadata that can't be JSON marshaled (function)
	invalidMetadata := map[string]interface{}{
		"validField":   "test",
		"invalidField": func() {},
	}

	// Claim should fail due to unmarshallable metadata
	version, err := store.ClaimShardOwnership(ctx, shardId, ownerId, invalidMetadata)

	assert.NotNil(t, err)
	assert.Equal(t, int64(0), version)
	assert.Contains(t, err.CustomMessage, "marshal metadata")

	// Verify no record was created
	var count int
	countQuery := "SELECT COUNT(*) FROM shards WHERE shard_id = ?"
	scanErr := store.session.Query(countQuery, shardId).Scan(&count)

	require.NoError(t, scanErr)
	assert.Equal(t, 0, count, "No record should be created when metadata marshaling fails")
}

func TestClaimShardOwnership_ConnectionFailure(t *testing.T) {
	// Create a store with invalid host to test connection failures
	config := &config.CassandraConnectConfig{
		Hosts:       []string{"invalid-host:9042"},
		Keyspace:    testKeyspace,
		Consistency: gocql.Quorum,
		Timeout:     1 * time.Second,
	}

	store, err := NewCassandraTimerStore(config)
	if err == nil {
		defer store.Close()

		ctx := context.Background()

		// This should fail due to connection issues
		version, claimErr := store.(*CassandraTimerStore).ClaimShardOwnership(ctx, 999, "owner", nil)

		assert.NotNil(t, claimErr)
		assert.Equal(t, int64(0), version)
	}
	// If we can't even create the store, that's also a valid test outcome
}
