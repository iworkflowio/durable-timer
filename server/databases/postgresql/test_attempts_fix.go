package postgresql

import (
	"context"
	"testing"
	"time"

	"github.com/iworkflowio/durable-timer/databases"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateTimer_WithAttempts(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// First, create a shard record
	ownerAddr := "owner-1"
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
	require.Nil(t, err)
	require.Equal(t, int64(1), shardVersion)

	// Create a timer with specific attempts count
	now := time.Now().UTC()
	timer := &databases.DbTimer{
		Id:                     "timer-with-attempts",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-with-attempts"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Attempts:               5, // Non-zero attempts
	}

	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Retrieve the timer and verify attempts field
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, "timer-with-attempts")
	assert.Nil(t, getErr)
	require.NotNil(t, retrievedTimer)

	// The key assertion - attempts should be preserved
	assert.Equal(t, int32(5), retrievedTimer.Attempts)
	assert.Equal(t, timer.Id, retrievedTimer.Id)
	assert.Equal(t, timer.TimerUuid, retrievedTimer.TimerUuid)
}

func TestCreateTimerNoLock_WithAttempts(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Create a timer with specific attempts count using NoLock method
	now := time.Now().UTC()
	timer := &databases.DbTimer{
		Id:                     "timer-nolock-attempts",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-nolock-attempts"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Attempts:               3, // Non-zero attempts
	}

	createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
	require.Nil(t, createErr)

	// Retrieve the timer and verify attempts field
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, "timer-nolock-attempts")
	assert.Nil(t, getErr)
	require.NotNil(t, retrievedTimer)

	// The key assertion - attempts should be preserved
	assert.Equal(t, int32(3), retrievedTimer.Attempts)
	assert.Equal(t, timer.Id, retrievedTimer.Id)
	assert.Equal(t, timer.TimerUuid, retrievedTimer.TimerUuid)
}

func TestDeleteTimersUpToTimestampWithBatchInsert_WithAttempts(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// First, create a shard record
	ownerAddr := "owner-1"
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
	require.Nil(t, err)
	require.Equal(t, int64(1), shardVersion)

	// Create an initial timer to be deleted
	now := time.Now().UTC()
	initialTimer := &databases.DbTimer{
		Id:                     "timer-to-delete",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-to-delete"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(-5 * time.Minute), // Past time
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now.Add(-10 * time.Minute),
		Attempts:               0,
	}

	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, initialTimer)
	require.Nil(t, createErr)

	// Prepare new timer to insert with specific attempts count
	newTimer := &databases.DbTimer{
		Id:                     "timer-batch-insert",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-batch-insert"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/new-callback",
		CallbackTimeoutSeconds: 60,
		CreatedAt:              now,
		Attempts:               7, // Non-zero attempts
	}

	// Perform batch delete and insert
	deleteRequest := &databases.RangeDeleteTimersRequest{
		StartTimestamp: now.Add(-10 * time.Minute),
		StartTimeUuid:  databases.GenerateTimerUUID(namespace, "start"),
		EndTimestamp:   now,
		EndTimeUuid:    databases.GenerateTimerUUID(namespace, "end"),
	}

	timersToInsert := []*databases.DbTimer{newTimer}

	response, deleteErr := store.RangeDeleteWithBatchInsert(ctx, shardId, shardVersion, deleteRequest, timersToInsert)
	assert.Nil(t, deleteErr)
	require.NotNil(t, response)
	assert.Equal(t, 1, response.DeletedCount)

	// Verify the inserted timer has correct attempts count
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, "timer-batch-insert")
	assert.Nil(t, getErr)
	require.NotNil(t, retrievedTimer)

	// The key assertion - attempts should be preserved during batch insert
	assert.Equal(t, int32(7), retrievedTimer.Attempts)
	assert.Equal(t, newTimer.Id, retrievedTimer.Id)
	assert.Equal(t, newTimer.TimerUuid, retrievedTimer.TimerUuid)

	// Verify the old timer was deleted
	deletedTimer, getErr2 := store.GetTimer(ctx, shardId, namespace, "timer-to-delete")
	assert.Nil(t, deletedTimer)
	assert.NotNil(t, getErr2)
	assert.True(t, databases.IsDbErrorNotExists(getErr2))
}
