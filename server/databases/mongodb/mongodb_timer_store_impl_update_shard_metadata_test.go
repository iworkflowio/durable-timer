package mongodb

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/iworkflowio/durable-timer/databases"
	"github.com/stretchr/testify/assert"
)

func TestUpdateShardMetadata_Success(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	ownerAddr := "test-owner"

	// First claim a shard to create it
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	assert.Nil(t, err)
	assert.NotNil(t, currentShardInfo)
	assert.Equal(t, int64(1), currentShardInfo.ShardVersion)

	// Define new metadata to update
	testUuid := uuid.New()
	newMetadata := databases.ShardMetadata{
		CommittedOffsetTimestamp: time.Now().UTC(),
		CommittedOffsetUuid:      testUuid,
	}

	// Update shard metadata
	updateErr := store.UpdateShardMetadata(ctx, shardId, currentShardInfo.ShardVersion, newMetadata)
	assert.Nil(t, updateErr)

	// Verify the metadata was updated by claiming again and checking metadata
	prev, current, claimErr := store.ClaimShardOwnership(ctx, shardId, "new-owner")
	assert.Nil(t, claimErr)
	assert.NotNil(t, prev)
	assert.NotNil(t, current)

	// The previous shard info should have the updated metadata
	assert.Equal(t, newMetadata.CommittedOffsetUuid, prev.Metadata.CommittedOffsetUuid)
	assert.WithinDuration(t, newMetadata.CommittedOffsetTimestamp, prev.Metadata.CommittedOffsetTimestamp, time.Second)
}

func TestUpdateShardMetadata_VersionMismatch(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 2
	ownerAddr := "test-owner"

	// First claim a shard to create it
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	assert.Nil(t, err)
	assert.NotNil(t, currentShardInfo)
	assert.Equal(t, int64(1), currentShardInfo.ShardVersion)

	// Define new metadata to update
	testUuid := uuid.New()
	newMetadata := databases.ShardMetadata{
		CommittedOffsetTimestamp: time.Now().UTC(),
		CommittedOffsetUuid:      testUuid,
	}

	// Try to update with wrong version (should fail)
	wrongVersion := currentShardInfo.ShardVersion + 1
	updateErr := store.UpdateShardMetadata(ctx, shardId, wrongVersion, newMetadata)
	assert.NotNil(t, updateErr)
	assert.True(t, updateErr.ShardConditionFail)
}

func TestUpdateShardMetadata_NonExistentShard(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	nonExistentShardId := 999

	// Define metadata to update
	testUuid := uuid.New()
	metadata := databases.ShardMetadata{
		CommittedOffsetTimestamp: time.Now().UTC(),
		CommittedOffsetUuid:      testUuid,
	}

	// Try to update metadata for non-existent shard (should fail)
	updateErr := store.UpdateShardMetadata(ctx, nonExistentShardId, 1, metadata)
	assert.NotNil(t, updateErr)
	assert.True(t, updateErr.ShardConditionFail)
}

func TestUpdateShardMetadata_EmptyMetadata(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 3
	ownerAddr := "test-owner"

	// First claim a shard to create it (which creates it with empty metadata)
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	assert.Nil(t, err)
	assert.NotNil(t, currentShardInfo)
	assert.Equal(t, int64(1), currentShardInfo.ShardVersion)
	assert.Equal(t, databases.ShardMetadata{}, currentShardInfo.Metadata)

	// First update to some non-empty metadata
	testUuid := uuid.New()
	nonEmptyMetadata := databases.ShardMetadata{
		CommittedOffsetTimestamp: time.Now().UTC(),
		CommittedOffsetUuid:      testUuid,
	}
	updateErr1 := store.UpdateShardMetadata(ctx, shardId, currentShardInfo.ShardVersion, nonEmptyMetadata)
	assert.Nil(t, updateErr1)

	// Now update back to empty metadata (should succeed)
	emptyMetadata := databases.ShardMetadata{}
	updateErr2 := store.UpdateShardMetadata(ctx, shardId, currentShardInfo.ShardVersion, emptyMetadata)
	assert.Nil(t, updateErr2)

	// Verify the metadata was updated to empty
	prev, current, claimErr := store.ClaimShardOwnership(ctx, shardId, "new-owner")
	assert.Nil(t, claimErr)
	assert.NotNil(t, prev)
	assert.NotNil(t, current)

	// The previous shard info should have empty metadata
	assert.Equal(t, emptyMetadata, prev.Metadata)
}

func TestUpdateShardMetadata_AfterShardVersionChange(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 4
	ownerAddr1 := "test-owner-1"
	ownerAddr2 := "test-owner-2"

	// First claim a shard to create it
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr1)
	assert.Nil(t, err)
	assert.NotNil(t, currentShardInfo)
	assert.Equal(t, int64(1), currentShardInfo.ShardVersion)

	// Claim the shard again with different owner (increments version)
	_, newShardInfo, claimErr := store.ClaimShardOwnership(ctx, shardId, ownerAddr2)
	assert.Nil(t, claimErr)
	assert.NotNil(t, newShardInfo)
	assert.Equal(t, int64(2), newShardInfo.ShardVersion)

	// Update with old version should fail
	testUuid := uuid.New()
	metadata := databases.ShardMetadata{
		CommittedOffsetTimestamp: time.Now().UTC(),
		CommittedOffsetUuid:      testUuid,
	}
	updateErr := store.UpdateShardMetadata(ctx, shardId, currentShardInfo.ShardVersion, metadata)
	assert.NotNil(t, updateErr)
	assert.True(t, updateErr.ShardConditionFail)

	// Update with correct version should succeed
	updateErr2 := store.UpdateShardMetadata(ctx, shardId, newShardInfo.ShardVersion, metadata)
	assert.Nil(t, updateErr2)
}

func TestUpdateShardMetadata_ConcurrentUpdate(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 5
	ownerAddr := "test-owner"

	// First claim a shard to create it
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	assert.Nil(t, err)
	assert.NotNil(t, currentShardInfo)
	assert.Equal(t, int64(1), currentShardInfo.ShardVersion)

	// First update should succeed
	testUuid1 := uuid.New()
	metadata1 := databases.ShardMetadata{
		CommittedOffsetTimestamp: time.Now().UTC(),
		CommittedOffsetUuid:      testUuid1,
	}
	updateErr1 := store.UpdateShardMetadata(ctx, shardId, currentShardInfo.ShardVersion, metadata1)
	assert.Nil(t, updateErr1)

	// Claim ownership again to change the shard version
	_, newShardInfo, claimErr := store.ClaimShardOwnership(ctx, shardId, "new-owner")
	assert.Nil(t, claimErr)
	assert.NotNil(t, newShardInfo)
	assert.Equal(t, int64(2), newShardInfo.ShardVersion)

	// Second update with old version should fail (version is now stale)
	testUuid2 := uuid.New()
	metadata2 := databases.ShardMetadata{
		CommittedOffsetTimestamp: time.Now().UTC(),
		CommittedOffsetUuid:      testUuid2,
	}
	updateErr2 := store.UpdateShardMetadata(ctx, shardId, currentShardInfo.ShardVersion, metadata2)
	assert.NotNil(t, updateErr2)
	assert.True(t, updateErr2.ShardConditionFail)
}
