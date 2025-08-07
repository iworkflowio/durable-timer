package mysql

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

	// Create shard record
	ownerAddr := "owner-1"
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
	require.Nil(t, err)

	// Create timer with non-zero attempts
	now := time.Now().UTC().Truncate(time.Millisecond)
	timer := &databases.DbTimer{
		Id:                     "timer-with-attempts",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-with-attempts"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Attempts:               5, // Non-zero attempts
	}

	// Create the timer
	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Retrieve the timer and verify attempts field
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timer.Id)
	require.Nil(t, getErr)
	require.NotNil(t, retrievedTimer)
	assert.Equal(t, int32(5), retrievedTimer.Attempts)
}

func TestCreateTimerNoLock_WithAttempts(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Create timer with non-zero attempts (no shard needed for NoLock)
	now := time.Now().UTC().Truncate(time.Millisecond)
	timer := &databases.DbTimer{
		Id:                     "timer-nolock-attempts",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-nolock-attempts"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Attempts:               3, // Non-zero attempts
	}

	// Create the timer without lock
	createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
	require.Nil(t, createErr)

	// Retrieve the timer and verify attempts field
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timer.Id)
	require.Nil(t, getErr)
	require.NotNil(t, retrievedTimer)
	assert.Equal(t, int32(3), retrievedTimer.Attempts)
}

func TestGetTimersUpToTimestamp_AttemptsField(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Create shard record
	ownerAddr := "owner-1"
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
	require.Nil(t, err)

	// Create timers with different attempts values
	now := time.Now().UTC().Truncate(time.Millisecond)
	timers := []*databases.DbTimer{
		{
			Id:                     "timer-attempts-0",
			TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-attempts-0"),
			Namespace:              namespace,
			ExecuteAt:              now.Add(1 * time.Minute),
			CallbackUrl:            "https://example.com/callback1",
			CallbackTimeoutSeconds: 30,
			CreatedAt:              now,
			Attempts:               0, // Zero attempts
		},
		{
			Id:                     "timer-attempts-2",
			TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-attempts-2"),
			Namespace:              namespace,
			ExecuteAt:              now.Add(2 * time.Minute),
			CallbackUrl:            "https://example.com/callback2",
			CallbackTimeoutSeconds: 30,
			CreatedAt:              now,
			Attempts:               2, // Two attempts
		},
		{
			Id:                     "timer-attempts-7",
			TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-attempts-7"),
			Namespace:              namespace,
			ExecuteAt:              now.Add(3 * time.Minute),
			CallbackUrl:            "https://example.com/callback3",
			CallbackTimeoutSeconds: 30,
			CreatedAt:              now,
			Attempts:               7, // Seven attempts
		},
	}

	// Insert all timers
	for _, timer := range timers {
		createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
		require.Nil(t, createErr)
	}

	// Retrieve all timers
	request := &databases.RangeGetTimersRequest{
		UpToTimestamp: now.Add(5 * time.Minute),
		Limit:         10,
	}

	response, getErr := store.GetTimersUpToTimestamp(ctx, shardId, request)
	require.Nil(t, getErr)
	require.NotNil(t, response)
	require.Len(t, response.Timers, 3)

	// Verify attempts values are preserved and returned correctly
	assert.Equal(t, int32(0), response.Timers[0].Attempts) // timer-attempts-0
	assert.Equal(t, int32(2), response.Timers[1].Attempts) // timer-attempts-2
	assert.Equal(t, int32(7), response.Timers[2].Attempts) // timer-attempts-7
}

func TestDeleteTimersUpToTimestampWithBatchInsert_AttemptsField(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Create shard record
	ownerAddr := "owner-1"
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
	require.Nil(t, err)

	// Create a timer to be deleted
	now := time.Now().UTC().Truncate(time.Millisecond)
	timerToDelete := &databases.DbTimer{
		Id:                     "timer-to-delete",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-to-delete"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/delete-me",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Attempts:               0,
	}

	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timerToDelete)
	require.Nil(t, createErr)

	// Create a new timer to be inserted with specific attempts value
	newTimer := &databases.DbTimer{
		Id:                     "new-timer-with-attempts",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "new-timer-with-attempts"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(15 * time.Minute),
		CallbackUrl:            "https://example.com/new-callback",
		CallbackTimeoutSeconds: 45,
		CreatedAt:              now,
		Attempts:               4, // Specific attempts value
	}

	// Define delete range and batch insert
	deleteRequest := &databases.RangeDeleteTimersRequest{
		StartTimestamp: now.Add(2 * time.Minute),
		StartTimeUuid:  databases.ZeroUUID,
		EndTimestamp:   now.Add(10 * time.Minute),
		EndTimeUuid:    databases.GenerateTimerUUID("max", "max"),
	}

	timersToInsert := []*databases.DbTimer{newTimer}

	// Execute delete and batch insert
	response, deleteErr := store.DeleteTimersUpToTimestampWithBatchInsert(ctx, shardId, shardVersion, deleteRequest, timersToInsert)
	require.Nil(t, deleteErr)
	require.NotNil(t, response)
	assert.Equal(t, 1, response.DeletedCount)

	// Verify the new timer was inserted with correct attempts value
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, "new-timer-with-attempts")
	require.Nil(t, getErr)
	require.NotNil(t, retrievedTimer)
	assert.Equal(t, int32(4), retrievedTimer.Attempts)

	// Verify the original timer was deleted
	deletedTimer, getErr := store.GetTimer(ctx, shardId, namespace, "timer-to-delete")
	assert.Nil(t, deletedTimer)
	assert.NotNil(t, getErr)
	assert.True(t, databases.IsDbErrorNotExists(getErr))
}

func TestUpdateTimer_AttemptsReset(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Create shard record
	ownerAddr := "owner-1"
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
	require.Nil(t, err)

	// Create timer with non-zero attempts
	now := time.Now().UTC().Truncate(time.Millisecond)
	timer := &databases.DbTimer{
		Id:                     "timer-update-attempts",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-update-attempts"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/original",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Attempts:               5, // Start with 5 attempts
	}

	// Create the timer
	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Verify initial attempts
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timer.Id)
	require.Nil(t, getErr)
	assert.Equal(t, int32(5), retrievedTimer.Attempts)

	// Update the timer (should reset attempts to 0)
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                "timer-update-attempts",
		ExecuteAt:              now.Add(15 * time.Minute),
		CallbackUrl:            "https://example.com/updated",
		CallbackTimeoutSeconds: 60,
	}

	updateErr := store.UpdateTimer(ctx, shardId, shardVersion, namespace, updateRequest)
	require.Nil(t, updateErr)

	// Verify attempts were reset to 0 after update
	updatedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timer.Id)
	require.Nil(t, getErr)
	require.NotNil(t, updatedTimer)
	assert.Equal(t, int32(0), updatedTimer.Attempts)
	assert.Equal(t, "https://example.com/updated", updatedTimer.CallbackUrl)
}
