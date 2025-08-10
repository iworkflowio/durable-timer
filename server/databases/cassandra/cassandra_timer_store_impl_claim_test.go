package cassandra

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/iworkflowio/durable-timer/databases"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaimShardOwnership_Setup(t *testing.T) {
	_, cleanup := setupTestStore(t)
	defer cleanup()
}

func TestClaimShardOwnership_NewShard(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	ownerAddr := "owner-1"

	// Claim ownership of a new shard
	prevShardInfo, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)

	assert.Nil(t, err)
	assert.Nil(t, prevShardInfo, "Should be nil for new shard")
	require.NotNil(t, currentShardInfo, "Current shard info should not be nil")

	assert.Equal(t, int64(shardId), currentShardInfo.ShardId)
	assert.Equal(t, ownerAddr, currentShardInfo.OwnerAddr)
	assert.Equal(t, int64(1), currentShardInfo.ShardVersion, "New shard should start with version 1")
	assert.True(t, time.Since(currentShardInfo.ClaimedAt) < 5*time.Second, "claimed_at should be recent")

	// Verify the record was created correctly in database
	var dbVersion int64
	var dbOwnerAddr string
	var dbMetadata string
	var dbClaimedAt time.Time

	query := "SELECT shard_version, shard_owner_addr, shard_metadata, shard_claimed_at FROM timers WHERE shard_id = ? AND row_type = ?"
	scanErr := store.session.Query(query, shardId, 1).Scan(&dbVersion, &dbOwnerAddr, &dbMetadata, &dbClaimedAt)

	require.NoError(t, scanErr)
	assert.Equal(t, int64(1), dbVersion)
	assert.Equal(t, ownerAddr, dbOwnerAddr)
	assert.True(t, time.Since(dbClaimedAt) < 5*time.Second, "claimed_at should be recent")
}

func TestClaimShardOwnership_ExistingShard(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 2

	// First claim
	prev1, curr1, err1 := store.ClaimShardOwnership(ctx, shardId, "owner-1")
	assert.Nil(t, err1)
	assert.Nil(t, prev1, "Should be nil for new shard")
	require.NotNil(t, curr1)
	assert.Equal(t, int64(1), curr1.ShardVersion)
	assert.Equal(t, "owner-1", curr1.OwnerAddr)

	// Second claim by different owner
	prev2, curr2, err2 := store.ClaimShardOwnership(ctx, shardId, "owner-2")
	assert.Nil(t, err2)
	require.NotNil(t, prev2, "Should have previous shard info")
	require.NotNil(t, curr2, "Should have current shard info")

	// Check previous shard info
	assert.Equal(t, int64(1), prev2.ShardVersion)
	assert.Equal(t, "owner-1", prev2.OwnerAddr)

	// Check current shard info
	assert.Equal(t, int64(2), curr2.ShardVersion)
	assert.Equal(t, "owner-2", curr2.OwnerAddr)

	// Third claim by original owner
	prev3, curr3, err3 := store.ClaimShardOwnership(ctx, shardId, "owner-1")
	assert.Nil(t, err3)
	require.NotNil(t, prev3)
	require.NotNil(t, curr3)

	// Check previous shard info
	assert.Equal(t, int64(2), prev3.ShardVersion)
	assert.Equal(t, "owner-2", prev3.OwnerAddr)

	// Check current shard info
	assert.Equal(t, int64(3), curr3.ShardVersion)
	assert.Equal(t, "owner-1", curr3.OwnerAddr)

	// Verify final state in database
	var dbVersion int64
	var dbOwnerAddr string
	query := "SELECT shard_version, shard_owner_addr FROM timers WHERE shard_id = ? AND row_type = ?"
	scanErr := store.session.Query(query, shardId, 1).Scan(&dbVersion, &dbOwnerAddr)

	require.NoError(t, scanErr)
	assert.Equal(t, int64(3), dbVersion)
	assert.Equal(t, "owner-1", dbOwnerAddr)
}

func TestClaimShardOwnership_ConcurrentClaims(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 3
	numGoroutines := 10

	var wg sync.WaitGroup
	results := make([]struct {
		prevShardInfo    *databases.ShardInfo
		currentShardInfo *databases.ShardInfo
		err              *databases.DbError
		ownerAddr        string
	}, numGoroutines)

	// Launch concurrent claims
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			if i > 5 {
				// sleep for 100 ms to run into the update case
				time.Sleep(100 * time.Millisecond)
			}
			ownerAddr := fmt.Sprintf("owner-%d", idx)
			prev, curr, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
			results[idx] = struct {
				prevShardInfo    *databases.ShardInfo
				currentShardInfo *databases.ShardInfo
				err              *databases.DbError
				ownerAddr        string
			}{prev, curr, err, ownerAddr}
		}(i)
	}

	wg.Wait()

	// Analyze results
	successCount := 0
	failureCount := 0
	var maxVersion int64
	var lastSuccessfulOwner string

	for _, result := range results {
		if result.err == nil {
			successCount++
			if result.currentShardInfo != nil && result.currentShardInfo.ShardVersion > maxVersion {
				maxVersion = result.currentShardInfo.ShardVersion
				lastSuccessfulOwner = result.ownerAddr
			}
		} else {
			failureCount++
			assert.True(t, result.err.ShardConditionFail, "should fail on shard condition, but is %s", result.err.OriginalError)
		}
	}

	// All goroutines should either succeed or fail, but we should have at least some successes
	assert.Greater(t, successCount, 0, "At least one claim should succeed")
	assert.Greater(t, failureCount, 0, "Should have some failures due to concurrency")
	assert.Greater(t, maxVersion, int64(0), "Maximum version should be positive")

	// Verify final database state
	var dbVersion int64
	var dbOwnerAddr string
	query := "SELECT shard_version, shard_owner_addr FROM timers WHERE shard_id = ? AND row_type = ?"
	scanErr := store.session.Query(query, shardId, 1).Scan(&dbVersion, &dbOwnerAddr)

	require.NoError(t, scanErr)
	assert.Equal(t, maxVersion, dbVersion, "Database version should match highest successful claim")
	assert.Equal(t, lastSuccessfulOwner, dbOwnerAddr, "Database owner should match last successful claimer")
}

func TestClaimShardOwnership_DefaultMetadata(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 4
	ownerAddr := "owner-default-metadata"

	// Claim shard (no metadata parameter anymore)
	prevShardInfo, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)

	assert.Nil(t, err)
	assert.Nil(t, prevShardInfo, "Should be nil for new shard")
	require.NotNil(t, currentShardInfo)
	assert.Equal(t, int64(1), currentShardInfo.ShardVersion)
	assert.Equal(t, ownerAddr, currentShardInfo.OwnerAddr)

	// Verify metadata is initialized with default ShardMetadata
	defaultMetadata := databases.ShardMetadata{}
	assert.Equal(t, defaultMetadata, currentShardInfo.Metadata)

	// Verify in database that metadata is properly serialized
	var dbMetadata string
	query := "SELECT shard_metadata FROM timers WHERE shard_id = ? AND row_type = ?"
	scanErr := store.session.Query(query, shardId, 1).Scan(&dbMetadata)

	require.NoError(t, scanErr)
	assert.NotEmpty(t, dbMetadata, "Should have serialized default metadata")
}

func TestClaimShardOwnership_MetadataPreservation(t *testing.T) {
	store, cleanup := setupTestStore(t)
	if store == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	shardId := 5
	ownerAddr1 := "owner-1"
	ownerAddr2 := "owner-2"

	// First claim creates shard with default metadata
	prevShardInfo1, currentShardInfo1, err1 := store.ClaimShardOwnership(ctx, shardId, ownerAddr1)

	assert.Nil(t, err1)
	assert.Nil(t, prevShardInfo1)
	require.NotNil(t, currentShardInfo1)
	assert.Equal(t, int64(1), currentShardInfo1.ShardVersion)
	assert.Equal(t, ownerAddr1, currentShardInfo1.OwnerAddr)

	// Verify default metadata structure
	defaultMetadata := databases.ShardMetadata{}
	assert.Equal(t, defaultMetadata, currentShardInfo1.Metadata)

	// Second claim should preserve the existing metadata
	prevShardInfo2, currentShardInfo2, err2 := store.ClaimShardOwnership(ctx, shardId, ownerAddr2)

	assert.Nil(t, err2)
	require.NotNil(t, prevShardInfo2)
	require.NotNil(t, currentShardInfo2)

	// Check that metadata is preserved across claims
	assert.Equal(t, currentShardInfo1.Metadata, prevShardInfo2.Metadata)
	assert.Equal(t, currentShardInfo1.Metadata, currentShardInfo2.Metadata)

	// Verify ownership change
	assert.Equal(t, ownerAddr1, prevShardInfo2.OwnerAddr)
	assert.Equal(t, ownerAddr2, currentShardInfo2.OwnerAddr)
	assert.Equal(t, int64(2), currentShardInfo2.ShardVersion)
}
