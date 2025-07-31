package mongodb

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/iworkflowio/durable-timer/databases"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
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
	ownerAddr := "test-owner-123"
	metadata := map[string]interface{}{
		"region": "us-west-2",
		"zone":   "us-west-2a",
	}

	// Convert ZeroUUID to high/low format for test queries
	zeroUuidHigh, zeroUuidLow := databases.UuidToHighLow(databases.ZeroUUID)

	// Claim ownership of a new shard
	version, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, metadata)

	assert.Nil(t, err)
	assert.Equal(t, int64(1), version, "New shard should start with version 1")

	// Verify the shard was created correctly in the database
	filter := bson.M{
		"shard_id":         shardId,
		"row_type":         databases.RowTypeShard,
		"timer_execute_at": databases.ZeroTimestamp,
		"timer_uuid_high":  zeroUuidHigh,
		"timer_uuid_low":   zeroUuidLow,
	}

	var result bson.M
	findErr := store.collection.FindOne(ctx, filter).Decode(&result)

	require.NoError(t, findErr)
	assert.Equal(t, int64(1), getInt64FromBSON(result, "shard_version"))
	assert.Equal(t, ownerAddr, getStringFromBSON(result, "shard_owner_addr"))

	metadataStr := getStringFromBSON(result, "shard_metadata")
	assert.Contains(t, metadataStr, "us-west-2")
	assert.Contains(t, metadataStr, "us-west-2a")

	claimedAt := getTimeFromBSON(result, "shard_claimed_at")
	assert.True(t, time.Since(claimedAt) < 5*time.Second, "claimed_at should be recent")
}

func TestClaimShardOwnership_ExistingShard(t *testing.T) {
	store, cleanup := setupTestStore(t)
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

	// Convert ZeroUUID to high/low format for test queries
	zeroUuidHigh, zeroUuidLow := databases.UuidToHighLow(databases.ZeroUUID)

	// Verify final state
	filter := bson.M{
		"shard_id":         shardId,
		"row_type":         databases.RowTypeShard,
		"timer_execute_at": databases.ZeroTimestamp,
		"timer_uuid_high":  zeroUuidHigh,
		"timer_uuid_low":   zeroUuidLow,
	}

	var result bson.M
	findErr := store.collection.FindOne(ctx, filter).Decode(&result)

	require.NoError(t, findErr)
	assert.Equal(t, int64(3), getInt64FromBSON(result, "shard_version"))
	assert.Equal(t, "owner-1", getStringFromBSON(result, "shard_owner_addr"))
}

func TestClaimShardOwnership_ConcurrentClaims(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 3
	numGoroutines := 10

	var wg sync.WaitGroup
	results := make([]struct {
		version int64
		err     *databases.DbError
		ownerAddr string
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
			version, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, map[string]int{"attempt": idx})
			results[idx] = struct {
				version int64
				err     *databases.DbError
				ownerAddr string
			}{version, err, ownerAddr}
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
			if result.version > maxVersion {
				maxVersion = result.version
				lastSuccessfulOwner = result.ownerAddr
			}
		} else {
			failureCount++
			assert.True(t, result.err.ShardConditionFail, "should fail on shard condition, but is %s", result.err.OriginalError)
			assert.Greater(t, result.err.ConflictShardVersion, int64(0), "should have a valid version")
		}
	}

	// All goroutines should either succeed or fail, but we should have at least some successes
	assert.Greater(t, successCount, 0, "At least one claim should succeed")
	assert.Greater(t, failureCount, 1, "Should have some failures due to concurrency")
	assert.Greater(t, maxVersion, int64(0), "Maximum version should be positive")

	// Convert ZeroUUID to high/low format for test queries
	zeroUuidHigh, zeroUuidLow := databases.UuidToHighLow(databases.ZeroUUID)

	// Verify final database state
	filter := bson.M{
		"shard_id":         shardId,
		"row_type":         databases.RowTypeShard,
		"timer_execute_at": databases.ZeroTimestamp,
		"timer_uuid_high":  zeroUuidHigh,
		"timer_uuid_low":   zeroUuidLow,
	}

	var result bson.M
	findErr := store.collection.FindOne(ctx, filter).Decode(&result)

	require.NoError(t, findErr)
	assert.Equal(t, maxVersion, getInt64FromBSON(result, "shard_version"), "Database version should match highest successful claim")
	assert.Equal(t, lastSuccessfulOwner, getStringFromBSON(result, "shard_owner_addr"), "Database owner should match last successful claimer")
}

func TestClaimShardOwnership_NilMetadata(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 4
	ownerAddr := "owner-nil-metadata"

	// Claim with nil metadata
	version, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)

	assert.Nil(t, err)
	assert.Equal(t, int64(1), version)

	// Convert ZeroUUID to high/low format for test queries
	zeroUuidHigh, zeroUuidLow := databases.UuidToHighLow(databases.ZeroUUID)

	// Verify metadata is empty/null in database
	filter := bson.M{
		"shard_id":         shardId,
		"row_type":         databases.RowTypeShard,
		"timer_execute_at": databases.ZeroTimestamp,
		"timer_uuid_high":  zeroUuidHigh,
		"timer_uuid_low":   zeroUuidLow,
	}

	var result bson.M
	findErr := store.collection.FindOne(ctx, filter).Decode(&result)

	require.NoError(t, findErr)
	// Should be null/nil
	metadata := result["shard_metadata"]
	assert.Nil(t, metadata)
}

func TestClaimShardOwnership_ComplexMetadata(t *testing.T) {
	store, cleanup := setupTestStore(t)
	if store == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	shardId := 5
	ownerAddr := "owner-complex"

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
	version, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, complexMetadata)

	assert.Nil(t, err)
	assert.Equal(t, int64(1), version)

	// Convert ZeroUUID to high/low format for test queries
	zeroUuidHigh, zeroUuidLow := databases.UuidToHighLow(databases.ZeroUUID)

	// Verify metadata is properly serialized
	filter := bson.M{
		"shard_id":         shardId,
		"row_type":         databases.RowTypeShard,
		"timer_execute_at": databases.ZeroTimestamp,
		"timer_uuid_high":  zeroUuidHigh,
		"timer_uuid_low":   zeroUuidLow,
	}

	var result bson.M
	findErr := store.collection.FindOne(ctx, filter).Decode(&result)

	require.NoError(t, findErr)

	metadataStr := getStringFromBSON(result, "shard_metadata")
	assert.Contains(t, metadataStr, "i-1234567890abcdef0")
	assert.Contains(t, metadataStr, "us-west-2")
	assert.Contains(t, metadataStr, "maxConnections")
	assert.Contains(t, metadataStr, "production")
}
