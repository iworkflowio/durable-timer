package postgresql

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/iworkflowio/durable-timer/databases"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRangeDeleteWithLimit_Basic(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test-namespace"
	now := time.Now().UTC().Truncate(time.Millisecond)

	// First, create a shard record
	ownerAddr := "owner-1"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	require.Nil(t, err)
	shardVersion := currentShardInfo.ShardVersion
	require.Nil(t, err)
	require.Equal(t, int64(1), shardVersion)

	// Create 3 timers at different times
	for i := 0; i < 3; i++ {
		timer := &databases.DbTimer{
			TimerUuid:              databases.GenerateTimerUUID(namespace, fmt.Sprintf("timer-%d", i)),
			Namespace:              namespace,
			Id:                     fmt.Sprintf("timer-%d", i),
			ExecuteAt:              now.Add(time.Duration(i) * time.Minute),
			CallbackUrl:            fmt.Sprintf("http://test-%d.com", i),
			CallbackTimeoutSeconds: 30,
			CreatedAt:              now,
			Attempts:               int32(i),
		}

		createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
		require.Nil(t, createErr)
	}

	// Delete with limit 2 - should delete first 2 timers by ExecuteAt order (PostgreSQL now respects limit)
	deleteRequest := &databases.RangeDeleteTimersRequest{
		StartTimestamp: now,
		StartTimeUuid:  databases.ZeroUUID,
		EndTimestamp:   now.Add(10 * time.Minute),
		EndTimeUuid:    databases.GenerateTimerUUID("max", "max"),
	}

	response, deleteErr := store.RangeDeleteWithLimit(ctx, shardId, deleteRequest, 2)
	assert.Nil(t, deleteErr)
	require.NotNil(t, response)
	assert.Equal(t, 2, response.DeletedCount) // Only 2 timers should be deleted

	// Verify first 2 timers are deleted, last one remains (PostgreSQL now respects limit)
	for i := 0; i < 3; i++ {
		timer, getErr := store.GetTimer(ctx, shardId, namespace, fmt.Sprintf("timer-%d", i))

		if i < 2 {
			assert.NotNil(t, getErr, fmt.Sprintf("Timer %d should be deleted", i))
			assert.True(t, databases.IsDbErrorNotExists(getErr), fmt.Sprintf("Timer %d should not exist", i))
			assert.Nil(t, timer)
		} else {
			assert.Nil(t, getErr, fmt.Sprintf("Timer %d should still exist", i))
			assert.Equal(t, fmt.Sprintf("timer-%d", i), timer.Id)
		}
	}
}

func TestRangeDeleteWithLimit_EmptyRange(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 2
	namespace := "test-namespace"
	now := time.Now().UTC().Truncate(time.Millisecond)

	// First, create a shard record
	ownerAddr := "owner-2"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	require.Nil(t, err)
	shardVersion := currentShardInfo.ShardVersion
	require.Nil(t, err)
	require.Equal(t, int64(1), shardVersion)

	// Create a timer at 20 minutes from now
	timer := &databases.DbTimer{
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-far"),
		Namespace:              namespace,
		Id:                     "timer-far",
		ExecuteAt:              now.Add(20 * time.Minute),
		CallbackUrl:            "http://test-far.com",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Attempts:               0,
	}

	createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
	require.Nil(t, createErr)

	// Delete in range that doesn't include the timer (timer is at 20 minutes)
	deleteRequest := &databases.RangeDeleteTimersRequest{
		StartTimestamp: now.Add(2 * time.Minute),
		StartTimeUuid:  databases.ZeroUUID,
		EndTimestamp:   now.Add(10 * time.Minute), // Timer is at 20 minutes, so outside range
		EndTimeUuid:    databases.GenerateTimerUUID("max", "max"),
	}

	response, deleteErr := store.RangeDeleteWithLimit(ctx, shardId, deleteRequest, 5)
	assert.Nil(t, deleteErr)
	require.NotNil(t, response)
	assert.Equal(t, 0, response.DeletedCount)

	// Verify timer still exists
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, "timer-far")
	assert.Nil(t, getErr)
	assert.Equal(t, "timer-far", retrievedTimer.Id)
}
