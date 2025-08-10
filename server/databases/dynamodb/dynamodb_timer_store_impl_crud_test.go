package dynamodb

import (
	"context"
	"testing"
	"time"

	"github.com/iworkflowio/durable-timer/databases"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetTimer_Success(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// First, create a shard record
	ownerAddr := "owner-1"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	assert.Nil(t, err)
	require.NotNil(t, currentShardInfo)
	shardVersion := currentShardInfo.ShardVersion

	require.Equal(t, int64(1), shardVersion)

	// Create a timer to retrieve
	now := time.Now().UTC().Truncate(time.Millisecond)
	timer := &databases.DbTimer{
		Id:                     "test-timer-get",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "test-timer-get"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback-get",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Attempts:               5, // Test non-zero attempts
		Payload:                map[string]interface{}{"key": "value", "number": 42},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 3, "backoff": "exponential"},
	}

	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Retrieve the timer
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timer.Id)
	require.Nil(t, getErr)
	require.NotNil(t, retrievedTimer)

	// Verify all fields
	assert.Equal(t, timer.Id, retrievedTimer.Id)
	assert.Equal(t, timer.TimerUuid, retrievedTimer.TimerUuid)
	assert.Equal(t, timer.Namespace, retrievedTimer.Namespace)
	assert.Equal(t, timer.CallbackUrl, retrievedTimer.CallbackUrl)
	assert.Equal(t, timer.CallbackTimeoutSeconds, retrievedTimer.CallbackTimeoutSeconds)
	assert.True(t, timer.ExecuteAt.Equal(retrievedTimer.ExecuteAt))
	assert.True(t, timer.CreatedAt.Equal(retrievedTimer.CreatedAt))

	// JSON deserialization converts int to float64
	expectedPayload := map[string]interface{}{"key": "value", "number": float64(42)}
	expectedRetryPolicy := map[string]interface{}{"maxAttempts": float64(3), "backoff": "exponential"}
	assert.Equal(t, expectedPayload, retrievedTimer.Payload)
	assert.Equal(t, expectedRetryPolicy, retrievedTimer.RetryPolicy)

	// Verify Attempts field
	assert.Equal(t, int32(5), retrievedTimer.Attempts)
}

func TestGetTimer_NotFound(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Try to get a non-existent timer
	timer, err := store.GetTimer(ctx, shardId, namespace, "non-existent-timer")
	assert.Nil(t, timer)
	assert.NotNil(t, err)
	assert.True(t, databases.IsDbErrorNotExists(err))
}

func TestDeleteTimer_Success(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Create shard record
	ownerAddr := "owner-1"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	assert.Nil(t, err)
	require.NotNil(t, currentShardInfo)
	shardVersion := currentShardInfo.ShardVersion

	// Create a timer to delete
	now := time.Now().UTC().Truncate(time.Millisecond)
	timer := &databases.DbTimer{
		Id:                     "test-timer-delete",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "test-timer-delete"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback-delete",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Attempts:               3,
	}

	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Verify timer exists
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timer.Id)
	require.Nil(t, getErr)
	require.NotNil(t, retrievedTimer)

	// Delete the timer
	deleteErr := store.DeleteTimer(ctx, shardId, shardVersion, namespace, timer.Id)
	if deleteErr != nil {
		t.Logf("Delete error: %v", deleteErr)
	}
	require.Nil(t, deleteErr)

	// Verify timer is deleted
	deletedTimer, getErr2 := store.GetTimer(ctx, shardId, namespace, timer.Id)
	assert.Nil(t, deletedTimer)
	assert.NotNil(t, getErr2)
	assert.True(t, databases.IsDbErrorNotExists(getErr2))
}

func TestDeleteTimer_NotExists(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Create shard record
	ownerAddr := "owner-1"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	assert.Nil(t, err)
	require.NotNil(t, currentShardInfo)
	shardVersion := currentShardInfo.ShardVersion

	// Try to delete a non-existent timer (should be idempotent)
	deleteErr := store.DeleteTimer(ctx, shardId, shardVersion, namespace, "non-existent-timer")
	assert.Nil(t, deleteErr) // Should succeed (idempotent)
}

func TestUpdateTimer_InPlaceUpdate(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Create shard record
	ownerAddr := "owner-1"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	assert.Nil(t, err)
	require.NotNil(t, currentShardInfo)
	shardVersion := currentShardInfo.ShardVersion

	// Create a timer to update
	now := time.Now().UTC().Truncate(time.Millisecond)
	timer := &databases.DbTimer{
		Id:                     "test-timer-update",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "test-timer-update"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback-original",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Attempts:               2,
		Payload:                map[string]interface{}{"original": "payload"},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 3},
	}

	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Verify timer was created
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timer.Id)
	require.Nil(t, getErr)
	assert.Equal(t, int32(2), retrievedTimer.Attempts)

	// Update the timer (without changing ExecuteAt - in-place update)
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId: timer.Id,
		// ExecuteAt is intentionally omitted (zero value) - no ExecuteAt change
		CallbackUrl:            "https://example.com/callback-updated",
		CallbackTimeoutSeconds: 60,
		Payload:                map[string]interface{}{"updated": "payload", "number": 123},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 5, "backoff": "linear"},
	}

	updateErr := store.UpdateTimer(ctx, shardId, shardVersion, namespace, updateRequest)
	if updateErr != nil {
		t.Logf("Update error: %v", updateErr)
	}
	require.Nil(t, updateErr)

	// Verify timer was updated
	updatedTimer, getErr2 := store.GetTimer(ctx, shardId, namespace, timer.Id)
	require.Nil(t, getErr2)
	require.NotNil(t, updatedTimer)

	// Verify updated fields
	assert.Equal(t, updateRequest.CallbackUrl, updatedTimer.CallbackUrl)
	assert.Equal(t, updateRequest.CallbackTimeoutSeconds, updatedTimer.CallbackTimeoutSeconds)

	// JSON deserialization converts int to float64
	expectedPayload := map[string]interface{}{"updated": "payload", "number": float64(123)}
	expectedRetryPolicy := map[string]interface{}{"maxAttempts": float64(5), "backoff": "linear"}
	assert.Equal(t, expectedPayload, updatedTimer.Payload)
	assert.Equal(t, expectedRetryPolicy, updatedTimer.RetryPolicy)

	// Verify unchanged fields
	assert.Equal(t, timer.Id, updatedTimer.Id)
	assert.Equal(t, timer.TimerUuid, updatedTimer.TimerUuid)
	assert.Equal(t, timer.Namespace, updatedTimer.Namespace)
	assert.True(t, timer.ExecuteAt.Equal(updatedTimer.ExecuteAt))
	assert.True(t, timer.CreatedAt.Equal(updatedTimer.CreatedAt))
	assert.Equal(t, int32(2), updatedTimer.Attempts) // Attempts should be preserved
}

func TestUpdateTimer_ExecutionTimeChange(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Create shard record
	ownerAddr := "owner-1"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	assert.Nil(t, err)
	require.NotNil(t, currentShardInfo)
	shardVersion := currentShardInfo.ShardVersion

	// Create a timer to update
	now := time.Now().UTC().Truncate(time.Millisecond)
	timer := &databases.DbTimer{
		Id:                     "test-timer-time-change",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "test-timer-time-change"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Attempts:               1,
	}

	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Try to update the timer with new ExecuteAt (should return an error)
	newExecuteAt := now.Add(20 * time.Minute)
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                timer.Id,
		ExecuteAt:              newExecuteAt, // Different ExecuteAt
		CallbackUrl:            "https://example.com/callback-new-time",
		CallbackTimeoutSeconds: 45,
	}

	updateErr := store.UpdateTimer(ctx, shardId, shardVersion, namespace, updateRequest)
	require.NotNil(t, updateErr)
	assert.True(t, updateErr.NotSupport)
	assert.Contains(t, updateErr.CustomMessage, "UpdateTimer does not support ExecuteAt changes")

	// Verify timer was not updated
	unchangedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timer.Id)
	require.Nil(t, getErr)
	require.NotNil(t, unchangedTimer)

	// Verify fields remain unchanged
	assert.Equal(t, timer.CallbackUrl, unchangedTimer.CallbackUrl)
	assert.Equal(t, timer.CallbackTimeoutSeconds, unchangedTimer.CallbackTimeoutSeconds)
	assert.True(t, timer.ExecuteAt.Equal(unchangedTimer.ExecuteAt))
	assert.Equal(t, timer.Id, unchangedTimer.Id)
	assert.Equal(t, timer.Namespace, unchangedTimer.Namespace)
	assert.True(t, timer.CreatedAt.Equal(unchangedTimer.CreatedAt))
	assert.Equal(t, int32(1), unchangedTimer.Attempts)
}

func TestUpdateTimer_NotExists(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Create shard record
	ownerAddr := "owner-1"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	assert.Nil(t, err)
	require.NotNil(t, currentShardInfo)
	shardVersion := currentShardInfo.ShardVersion

	// Try to update a non-existent timer
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                "non-existent-timer",
		ExecuteAt:              time.Now().Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
	}

	updateErr := store.UpdateTimer(ctx, shardId, shardVersion, namespace, updateRequest)
	assert.NotNil(t, updateErr)
}

func TestCreateTimerAttemptsPreservation(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Create shard record
	ownerAddr := "owner-1"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	assert.Nil(t, err)
	require.NotNil(t, currentShardInfo)
	shardVersion := currentShardInfo.ShardVersion

	now := time.Now().UTC().Truncate(time.Millisecond)

	// Test CreateTimer with zero Attempts
	timer1 := &databases.DbTimer{
		Id:                     "timer-zero-attempts",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-zero-attempts"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(1 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Attempts:               0,
	}

	// Test CreateTimer with non-zero Attempts
	timer2 := &databases.DbTimer{
		Id:                     "timer-nonzero-attempts",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-nonzero-attempts"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(2 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Attempts:               7, // Non-zero value
	}

	// Create both timers
	createErr1 := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer1)
	require.Nil(t, createErr1)
	createErr2 := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer2)
	require.Nil(t, createErr2)

	// Verify Attempts are preserved in CreateTimer
	retrievedTimer1, getErr1 := store.GetTimer(ctx, shardId, namespace, timer1.Id)
	require.Nil(t, getErr1)
	assert.Equal(t, int32(0), retrievedTimer1.Attempts)

	retrievedTimer2, getErr2 := store.GetTimer(ctx, shardId, namespace, timer2.Id)
	require.Nil(t, getErr2)
	assert.Equal(t, int32(7), retrievedTimer2.Attempts)

	// Test CreateTimerNoLock as well
	timer3 := &databases.DbTimer{
		Id:                     "timer-nolock-attempts",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-nolock-attempts"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(3 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Attempts:               12, // Non-zero value
	}

	createErr3 := store.CreateTimerNoLock(ctx, shardId, namespace, timer3)
	require.Nil(t, createErr3)

	retrievedTimer3, getErr3 := store.GetTimer(ctx, shardId, namespace, timer3.Id)
	require.Nil(t, getErr3)
	assert.Equal(t, int32(12), retrievedTimer3.Attempts)

	// Test GetTimersUpToTimestamp returns correct Attempts
	request := &databases.RangeGetTimersRequest{
		StartTimestamp: time.Unix(0, 0),
		StartTimeUuid:  databases.ZeroUUID,
		EndTimestamp:   now.Add(5 * time.Minute),
		EndTimeUuid:    databases.MaxUUID,
		Limit:          10,
	}
	response, getErr4 := store.RangeGetTimers(ctx, shardId, request)
	require.Nil(t, getErr4)
	require.Len(t, response.Timers, 3)

	// Verify all timers have correct Attempts values
	timersByExecuteAt := make(map[string]*databases.DbTimer)
	for _, timer := range response.Timers {
		timersByExecuteAt[timer.Id] = timer
	}

	assert.Equal(t, int32(0), timersByExecuteAt["timer-zero-attempts"].Attempts)
	assert.Equal(t, int32(7), timersByExecuteAt["timer-nonzero-attempts"].Attempts)
	assert.Equal(t, int32(12), timersByExecuteAt["timer-nolock-attempts"].Attempts)
}
