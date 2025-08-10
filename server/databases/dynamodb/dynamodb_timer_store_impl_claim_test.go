package dynamodb

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
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
	assert.Nil(t, prevShardInfo, "prevShardInfo should be nil for new shard")
	assert.NotNil(t, currentShardInfo, "currentShardInfo should not be nil")
	assert.Equal(t, int64(1), currentShardInfo.ShardVersion, "New shard should start with version 1")
	assert.Equal(t, int64(shardId), currentShardInfo.ShardId)
	assert.Equal(t, ownerAddr, currentShardInfo.OwnerAddr)

	// Verify the record was created correctly
	key := map[string]types.AttributeValue{
		"shard_id": &types.AttributeValueMemberN{Value: "1"},
		"sort_key": &types.AttributeValueMemberS{Value: shardSortKey},
	}

	getItemInput := &dynamodb.GetItemInput{
		TableName: aws.String(store.tableName),
		Key:       key,
	}

	result, getErr := store.client.GetItem(ctx, getItemInput)
	require.NoError(t, getErr)
	require.NotNil(t, result.Item)

	// Verify shard fields
	shardVersion, versionErr := extractShardVersionFromItem(result.Item)
	require.NoError(t, versionErr)
	assert.Equal(t, int64(1), shardVersion)

	shardInfo := extractShardInfoFromItem(result.Item, int64(shardId))
	assert.Equal(t, ownerAddr, shardInfo.OwnerAddr)
	// For new shard, metadata should be default (empty)
	assert.Equal(t, databases.ShardMetadata{}, shardInfo.Metadata)
	assert.True(t, time.Since(shardInfo.ClaimedAt) < 5*time.Second, "claimed_at should be recent")
	// Verify currentShardInfo matches what's in the database
	assert.Equal(t, shardInfo.ShardVersion, currentShardInfo.ShardVersion)
	assert.Equal(t, shardInfo.OwnerAddr, currentShardInfo.OwnerAddr)
	assert.True(t, time.Since(currentShardInfo.ClaimedAt) < 5*time.Second, "currentShardInfo claimed_at should be recent")
}

func TestClaimShardOwnership_ExistingShard(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 2

	// First claim
	prev1, current1, err1 := store.ClaimShardOwnership(ctx, shardId, "owner-1")
	assert.Nil(t, err1)
	assert.Nil(t, prev1, "prevShardInfo should be nil for new shard")
	assert.NotNil(t, current1)
	assert.Equal(t, int64(1), current1.ShardVersion)
	assert.Equal(t, "owner-1", current1.OwnerAddr)

	// Second claim by different owner
	prev2, current2, err2 := store.ClaimShardOwnership(ctx, shardId, "owner-2")
	assert.Nil(t, err2)
	assert.NotNil(t, prev2, "prevShardInfo should not be nil for existing shard")
	assert.NotNil(t, current2)
	assert.Equal(t, int64(1), prev2.ShardVersion)
	assert.Equal(t, "owner-1", prev2.OwnerAddr)
	assert.Equal(t, int64(2), current2.ShardVersion)
	assert.Equal(t, "owner-2", current2.OwnerAddr)

	// Third claim by original owner
	prev3, current3, err3 := store.ClaimShardOwnership(ctx, shardId, "owner-1")
	assert.Nil(t, err3)
	assert.NotNil(t, prev3)
	assert.NotNil(t, current3)
	assert.Equal(t, int64(2), prev3.ShardVersion)
	assert.Equal(t, "owner-2", prev3.OwnerAddr)
	assert.Equal(t, int64(3), current3.ShardVersion)
	assert.Equal(t, "owner-1", current3.OwnerAddr)

	// Verify final state
	key := map[string]types.AttributeValue{
		"shard_id": &types.AttributeValueMemberN{Value: "2"},
		"sort_key": &types.AttributeValueMemberS{Value: shardSortKey},
	}

	getItemInput := &dynamodb.GetItemInput{
		TableName: aws.String(store.tableName),
		Key:       key,
	}

	result, getErr := store.client.GetItem(ctx, getItemInput)
	require.NoError(t, getErr)
	require.NotNil(t, result.Item)

	shardVersion, versionErr := extractShardVersionFromItem(result.Item)
	require.NoError(t, versionErr)
	assert.Equal(t, int64(3), shardVersion)

	shardInfo := extractShardInfoFromItem(result.Item, int64(shardId))
	assert.Equal(t, "owner-1", shardInfo.OwnerAddr)
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
			_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
			var version int64
			if currentShardInfo != nil {
				version = currentShardInfo.ShardVersion
			}
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
		}
	}

	// All goroutines should either succeed or fail, but we should have at least some successes
	assert.Greater(t, successCount, 0, "At least one claim should succeed")
	assert.Greater(t, failureCount, 1, "Should have some failures due to concurrency")
	assert.Greater(t, maxVersion, int64(0), "Maximum version should be positive")

	// Verify final database state
	key := map[string]types.AttributeValue{
		"shard_id": &types.AttributeValueMemberN{Value: "3"},
		"sort_key": &types.AttributeValueMemberS{Value: shardSortKey},
	}

	getItemInput := &dynamodb.GetItemInput{
		TableName: aws.String(store.tableName),
		Key:       key,
	}

	result, getErr := store.client.GetItem(ctx, getItemInput)
	require.NoError(t, getErr)
	require.NotNil(t, result.Item)

	dbVersion, versionErr := extractShardVersionFromItem(result.Item)
	require.NoError(t, versionErr)
	assert.Equal(t, maxVersion, dbVersion, "Database version should match highest successful claim")

	shardInfo := extractShardInfoFromItem(result.Item, int64(shardId))
	assert.Equal(t, lastSuccessfulOwner, shardInfo.OwnerAddr, "Database owner should match last successful claimer")
}

func TestClaimShardOwnership_DefaultMetadata(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 4
	ownerAddr := "owner-default-metadata"

	// Claim shard - metadata will be initialized to default
	prevShardInfo, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)

	assert.Nil(t, err)
	assert.Nil(t, prevShardInfo)
	assert.NotNil(t, currentShardInfo)
	assert.Equal(t, int64(1), currentShardInfo.ShardVersion)

	// Verify metadata is initialized to default
	key := map[string]types.AttributeValue{
		"shard_id": &types.AttributeValueMemberN{Value: "4"},
		"sort_key": &types.AttributeValueMemberS{Value: shardSortKey},
	}

	getItemInput := &dynamodb.GetItemInput{
		TableName: aws.String(store.tableName),
		Key:       key,
	}

	result, getErr := store.client.GetItem(ctx, getItemInput)
	require.NoError(t, getErr)
	require.NotNil(t, result.Item)

	// shard_metadata should exist and be default value
	shardInfo := extractShardInfoFromItem(result.Item, int64(shardId))
	assert.Equal(t, databases.ShardMetadata{}, shardInfo.Metadata)
	assert.Equal(t, databases.ShardMetadata{}, currentShardInfo.Metadata)
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

	// First claim with default metadata
	prev1, current1, err1 := store.ClaimShardOwnership(ctx, shardId, ownerAddr1)
	assert.Nil(t, err1)
	assert.Nil(t, prev1)
	assert.NotNil(t, current1)
	assert.Equal(t, int64(1), current1.ShardVersion)

	// Second claim should preserve the metadata from first claim
	prev2, current2, err2 := store.ClaimShardOwnership(ctx, shardId, ownerAddr2)
	assert.Nil(t, err2)
	assert.NotNil(t, prev2)
	assert.NotNil(t, current2)
	assert.Equal(t, int64(1), prev2.ShardVersion)
	assert.Equal(t, ownerAddr1, prev2.OwnerAddr)
	assert.Equal(t, int64(2), current2.ShardVersion)
	assert.Equal(t, ownerAddr2, current2.OwnerAddr)

	// Metadata should be preserved across claims
	assert.Equal(t, databases.ShardMetadata{}, prev2.Metadata, "Previous metadata should be default")
	assert.Equal(t, databases.ShardMetadata{}, current2.Metadata, "Current metadata should be preserved from previous claim")

	// Verify database state
	key := map[string]types.AttributeValue{
		"shard_id": &types.AttributeValueMemberN{Value: "5"},
		"sort_key": &types.AttributeValueMemberS{Value: shardSortKey},
	}

	getItemInput := &dynamodb.GetItemInput{
		TableName: aws.String(store.tableName),
		Key:       key,
	}

	result, getErr := store.client.GetItem(ctx, getItemInput)
	require.NoError(t, getErr)
	require.NotNil(t, result.Item)

	shardInfo := extractShardInfoFromItem(result.Item, int64(shardId))
	assert.Equal(t, databases.ShardMetadata{}, shardInfo.Metadata, "Database metadata should remain default")
}
