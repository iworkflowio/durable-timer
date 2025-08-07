package postgresql

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
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
	require.Nil(t, err)
	require.Equal(t, int64(1), shardVersion)

	// Create a timer first
	now := time.Now().UTC()
	timer := &databases.DbTimer{
		Id:                     "timer-1",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-1"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		Payload:                map[string]interface{}{"key": "value"},
		RetryPolicy:            map[string]interface{}{"maxRetries": 3},
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
	}

	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Now test GetTimer
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, "timer-1")
	assert.Nil(t, getErr)
	require.NotNil(t, retrievedTimer)

	// Verify all fields
	assert.Equal(t, timer.Id, retrievedTimer.Id)
	assert.Equal(t, timer.TimerUuid, retrievedTimer.TimerUuid)
	assert.Equal(t, timer.Namespace, retrievedTimer.Namespace)
	assert.WithinDuration(t, timer.ExecuteAt, retrievedTimer.ExecuteAt, time.Second)
	assert.Equal(t, timer.CallbackUrl, retrievedTimer.CallbackUrl)
	assert.Equal(t, timer.CallbackTimeoutSeconds, retrievedTimer.CallbackTimeoutSeconds)
	assert.WithinDuration(t, timer.CreatedAt, retrievedTimer.CreatedAt, time.Second)
	assert.Equal(t, int32(0), retrievedTimer.Attempts) // Should be 0 for new timer

	// Verify payload and retry policy - JSON unmarshaling converts int to float64
	expectedPayload := map[string]interface{}{"key": "value"}
	expectedRetryPolicy := map[string]interface{}{"maxRetries": float64(3)}
	assert.Equal(t, expectedPayload, retrievedTimer.Payload)
	assert.Equal(t, expectedRetryPolicy, retrievedTimer.RetryPolicy)
}

func TestGetTimer_NotFound(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Try to get a timer that doesn't exist
	timer, err := store.GetTimer(ctx, shardId, namespace, "non-existent-timer")
	assert.Nil(t, timer)
	require.NotNil(t, err)
	assert.True(t, databases.IsDbErrorNotExists(err))
}

func TestUpdateTimer_SameExecuteAt(t *testing.T) {
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

	// Create a timer first
	now := time.Now().UTC()
	executeAt := now.Add(5 * time.Minute)
	timer := &databases.DbTimer{
		Id:                     "timer-1",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-1"),
		Namespace:              namespace,
		ExecuteAt:              executeAt,
		CallbackUrl:            "https://example.com/callback",
		Payload:                map[string]interface{}{"key": "value"},
		RetryPolicy:            map[string]interface{}{"maxRetries": 3},
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
	}

	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Update timer with same ExecuteAt (should use simple UPDATE)
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                "timer-1",
		ExecuteAt:              executeAt, // Same ExecuteAt
		CallbackUrl:            "https://example.com/new-callback",
		Payload:                map[string]interface{}{"updated": "payload"},
		RetryPolicy:            map[string]interface{}{"maxRetries": 5},
		CallbackTimeoutSeconds: 60,
	}

	updateErr := store.UpdateTimer(ctx, shardId, shardVersion, namespace, updateRequest)
	assert.Nil(t, updateErr)

	// Verify the update
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, "timer-1")
	assert.Nil(t, getErr)
	require.NotNil(t, retrievedTimer)

	assert.Equal(t, updateRequest.CallbackUrl, retrievedTimer.CallbackUrl)
	// JSON unmarshaling converts int to float64
	expectedPayload := map[string]interface{}{"updated": "payload"}
	expectedRetryPolicy := map[string]interface{}{"maxRetries": float64(5)}
	assert.Equal(t, expectedPayload, retrievedTimer.Payload)
	assert.Equal(t, expectedRetryPolicy, retrievedTimer.RetryPolicy)
	assert.Equal(t, updateRequest.CallbackTimeoutSeconds, retrievedTimer.CallbackTimeoutSeconds)
	assert.WithinDuration(t, executeAt, retrievedTimer.ExecuteAt, time.Second)       // ExecuteAt should remain the same
	assert.WithinDuration(t, timer.CreatedAt, retrievedTimer.CreatedAt, time.Second) // CreatedAt should be preserved
}

func TestUpdateTimer_DifferentExecuteAt(t *testing.T) {
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

	// Create a timer first
	now := time.Now().UTC()
	originalExecuteAt := now.Add(5 * time.Minute)
	timer := &databases.DbTimer{
		Id:                     "timer-1",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-1"),
		Namespace:              namespace,
		ExecuteAt:              originalExecuteAt,
		CallbackUrl:            "https://example.com/callback",
		Payload:                map[string]interface{}{"key": "value"},
		RetryPolicy:            map[string]interface{}{"maxRetries": 3},
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
	}

	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Update timer with different ExecuteAt (should use DELETE+INSERT)
	newExecuteAt := time.Now().UTC().Add(10 * time.Minute)
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                "timer-1",
		ExecuteAt:              newExecuteAt, // Different ExecuteAt
		CallbackUrl:            "https://example.com/new-callback",
		Payload:                map[string]interface{}{"updated": "payload"},
		RetryPolicy:            map[string]interface{}{"maxRetries": 5},
		CallbackTimeoutSeconds: 60,
	}

	updateErr := store.UpdateTimer(ctx, shardId, shardVersion, namespace, updateRequest)
	assert.Nil(t, updateErr)

	// Verify the update
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, "timer-1")
	assert.Nil(t, getErr)
	require.NotNil(t, retrievedTimer)

	assert.Equal(t, updateRequest.CallbackUrl, retrievedTimer.CallbackUrl)
	// JSON unmarshaling converts int to float64
	expectedPayload := map[string]interface{}{"updated": "payload"}
	expectedRetryPolicy := map[string]interface{}{"maxRetries": float64(5)}
	assert.Equal(t, expectedPayload, retrievedTimer.Payload)
	assert.Equal(t, expectedRetryPolicy, retrievedTimer.RetryPolicy)
	assert.Equal(t, updateRequest.CallbackTimeoutSeconds, retrievedTimer.CallbackTimeoutSeconds)
	assert.WithinDuration(t, newExecuteAt, retrievedTimer.ExecuteAt, time.Second)    // ExecuteAt should be updated
	assert.WithinDuration(t, timer.CreatedAt, retrievedTimer.CreatedAt, time.Second) // CreatedAt should be preserved
	assert.Equal(t, int32(0), retrievedTimer.Attempts)                               // Attempts should be preserved
}

func TestUpdateTimer_ShardVersionMismatch(t *testing.T) {
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

	// Create a timer first
	timer := &databases.DbTimer{
		Id:                     "timer-1",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-1"),
		Namespace:              namespace,
		ExecuteAt:              time.Now().UTC().Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              time.Now().UTC(),
	}

	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Try to update with wrong shard version
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                "timer-1",
		ExecuteAt:              time.Now().UTC().Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/new-callback",
		CallbackTimeoutSeconds: 60,
	}

	wrongShardVersion := int64(999)
	updateErr := store.UpdateTimer(ctx, shardId, wrongShardVersion, namespace, updateRequest)
	assert.NotNil(t, updateErr)
	assert.True(t, updateErr.ShardConditionFail)
	assert.Equal(t, shardVersion, updateErr.ConflictShardVersion)
}

func TestUpdateTimer_TimerNotFound(t *testing.T) {
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

	// Try to update a timer that doesn't exist
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                "non-existent-timer",
		ExecuteAt:              time.Now().Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
	}

	updateErr := store.UpdateTimer(ctx, shardId, shardVersion, namespace, updateRequest)
	assert.NotNil(t, updateErr)
	assert.True(t, databases.IsDbErrorNotExists(updateErr))
}

func TestDeleteTimer_Success(t *testing.T) {
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

	// Create a timer first
	timer := &databases.DbTimer{
		Id:                     "timer-1",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-1"),
		Namespace:              namespace,
		ExecuteAt:              time.Now().UTC().Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              time.Now().UTC(),
	}

	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Verify timer exists before deletion
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, "timer-1")
	assert.Nil(t, getErr)
	assert.NotNil(t, retrievedTimer)

	// Delete the timer
	deleteErr := store.DeleteTimer(ctx, shardId, shardVersion, namespace, "timer-1")
	assert.Nil(t, deleteErr)

	// Verify timer is deleted
	deletedTimer, getErr2 := store.GetTimer(ctx, shardId, namespace, "timer-1")
	assert.Nil(t, deletedTimer)
	assert.NotNil(t, getErr2)
	assert.True(t, databases.IsDbErrorNotExists(getErr2))
}

func TestDeleteTimer_ShardVersionMismatch(t *testing.T) {
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

	// Create a timer first
	timer := &databases.DbTimer{
		Id:                     "timer-1",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-1"),
		Namespace:              namespace,
		ExecuteAt:              time.Now().UTC().Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              time.Now().UTC(),
	}

	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Try to delete with wrong shard version
	wrongShardVersion := int64(999)
	deleteErr := store.DeleteTimer(ctx, shardId, wrongShardVersion, namespace, "timer-1")
	assert.NotNil(t, deleteErr)
	assert.True(t, deleteErr.ShardConditionFail)
	assert.Equal(t, shardVersion, deleteErr.ConflictShardVersion)

	// Verify timer still exists
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, "timer-1")
	assert.Nil(t, getErr)
	assert.NotNil(t, retrievedTimer)
}

func TestDeleteTimer_NonExistentTimer(t *testing.T) {
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

	// Try to delete a timer that doesn't exist (should return NotExists error)
	deleteErr := store.DeleteTimer(ctx, shardId, shardVersion, namespace, "non-existent-timer")
	assert.NotNil(t, deleteErr)
	assert.True(t, databases.IsDbErrorNotExists(deleteErr))
}

func TestGetTimersUpToTimestamp_WithAttempts(t *testing.T) {
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

	// Create timers with different execution times
	now := time.Now().UTC()
	timer1 := &databases.DbTimer{
		Id:                     "timer-1",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-1"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(-5 * time.Minute), // Past
		CallbackUrl:            "https://example.com/callback1",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now.Add(-10 * time.Minute),
	}

	timer2 := &databases.DbTimer{
		Id:                     "timer-2",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-2"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(5 * time.Minute), // Future
		CallbackUrl:            "https://example.com/callback2",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now.Add(-10 * time.Minute),
	}

	createErr1 := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer1)
	require.Nil(t, createErr1)

	createErr2 := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer2)
	require.Nil(t, createErr2)

	// Get timers up to now (should include timer1 but not timer2)
	request := &databases.RangeGetTimersRequest{
		UpToTimestamp: now,
		Limit:         10,
	}

	response, getErr := store.GetTimersUpToTimestamp(ctx, shardId, request)
	assert.Nil(t, getErr)
	require.NotNil(t, response)
	require.Len(t, response.Timers, 1)

	// Verify the returned timer
	returnedTimer := response.Timers[0]
	assert.Equal(t, timer1.Id, returnedTimer.Id)
	assert.Equal(t, timer1.TimerUuid, returnedTimer.TimerUuid)
	assert.Equal(t, timer1.Namespace, returnedTimer.Namespace)
	assert.Equal(t, int32(0), returnedTimer.Attempts) // Should include attempts field
}

func TestCRUD_CompleteWorkflow(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Step 1: Create shard
	ownerAddr := "owner-1"
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
	require.Nil(t, err)
	require.Equal(t, int64(1), shardVersion)

	// Step 2: Create timer
	timer := &databases.DbTimer{
		Id:                     "workflow-timer",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "workflow-timer"),
		Namespace:              namespace,
		ExecuteAt:              time.Now().Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		Payload:                map[string]interface{}{"step": "1"},
		RetryPolicy:            map[string]interface{}{"maxRetries": 3},
		CallbackTimeoutSeconds: 30,
		CreatedAt:              time.Now(),
	}

	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Step 3: Get timer and verify
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, "workflow-timer")
	assert.Nil(t, getErr)
	require.NotNil(t, retrievedTimer)
	assert.Equal(t, timer.Id, retrievedTimer.Id)

	// Step 4: Update timer
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                "workflow-timer",
		ExecuteAt:              time.Now().UTC().Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/updated-callback",
		Payload:                map[string]interface{}{"step": "2"},
		RetryPolicy:            map[string]interface{}{"maxRetries": 5},
		CallbackTimeoutSeconds: 60,
	}

	updateErr := store.UpdateTimer(ctx, shardId, shardVersion, namespace, updateRequest)
	assert.Nil(t, updateErr)

	// Step 5: Get updated timer and verify
	updatedTimer, getErr2 := store.GetTimer(ctx, shardId, namespace, "workflow-timer")
	assert.Nil(t, getErr2)
	require.NotNil(t, updatedTimer)
	assert.Equal(t, updateRequest.CallbackUrl, updatedTimer.CallbackUrl)
	// JSON unmarshaling converts int to float64
	expectedPayload := map[string]interface{}{"step": "2"}
	assert.Equal(t, expectedPayload, updatedTimer.Payload)

	// Step 6: Delete timer
	deleteErr := store.DeleteTimer(ctx, shardId, shardVersion, namespace, "workflow-timer")
	assert.Nil(t, deleteErr)

	// Step 7: Verify timer is deleted
	deletedTimer, getErr3 := store.GetTimer(ctx, shardId, namespace, "workflow-timer")
	assert.Nil(t, deletedTimer)
	assert.NotNil(t, getErr3)
	assert.True(t, databases.IsDbErrorNotExists(getErr3))
}
