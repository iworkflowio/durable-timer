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
	metadata := map[string]interface{}{
		"instanceId": "instance-1",
		"region":     "us-west-2",
	}

	// Claim ownership of a new shard
	version, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, metadata)

	assert.Nil(t, err)
	assert.Equal(t, int64(1), version, "New shard should start with version 1")

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
	assert.Contains(t, shardInfo.Metadata, "instance-1")
	assert.Contains(t, shardInfo.Metadata, "us-west-2")
	assert.True(t, time.Since(shardInfo.ClaimedAt) < 5*time.Second, "claimed_at should be recent")
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

	// Verify metadata is not present in DynamoDB item
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

	// shard_metadata attribute should not exist
	_, exists := result.Item["shard_metadata"]
	assert.False(t, exists, "shard_metadata should not exist when nil")
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

	// Verify metadata is properly serialized
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
	assert.Contains(t, shardInfo.Metadata, "i-1234567890abcdef0")
	assert.Contains(t, shardInfo.Metadata, "us-west-2")
	assert.Contains(t, shardInfo.Metadata, "maxConnections")
	assert.Contains(t, shardInfo.Metadata, "production")
}
