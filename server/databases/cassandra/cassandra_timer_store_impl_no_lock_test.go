package cassandra

import (
	"context"
	"testing"
	"time"

	"github.com/iworkflowio/durable-timer/databases"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateTimerNoLock_InPlaceUpdate(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test-namespace"
	timerId := "test-timer-1"

	// Create a timer first
	timer := &databases.DbTimer{
		Id:                     timerId,
		TimerUuid:              databases.GenerateTimerUUID(namespace, timerId),
		Namespace:              namespace,
		ExecuteAt:              time.Now().UTC().Add(10 * time.Minute),
		CallbackUrl:            "http://example.com/callback",
		Payload:                map[string]interface{}{"key": "value"},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 3},
		CallbackTimeoutSeconds: 30,
		CreatedAt:              time.Now().UTC(),
		Attempts:               0,
	}

	err := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
	require.Nil(t, err)

	// Update the timer (same execution time - in-place update)
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                timerId,
		ExecuteAt:              timer.ExecuteAt, // Same execution time
		CallbackUrl:            "http://example.com/new-callback",
		Payload:                map[string]interface{}{"key": "new-value"},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 5},
		CallbackTimeoutSeconds: 60,
	}

	err = store.UpdateTimerNoLock(ctx, shardId, namespace, updateRequest)
	require.Nil(t, err)

	// Verify the timer was updated
	updatedTimer, err := store.GetTimer(ctx, shardId, namespace, timerId)
	require.Nil(t, err)
	require.NotNil(t, updatedTimer)

	assert.Equal(t, "http://example.com/new-callback", updatedTimer.CallbackUrl)
	assert.Equal(t, map[string]interface{}{"key": "new-value"}, updatedTimer.Payload)
	assert.Equal(t, map[string]interface{}{"maxAttempts": 5.0}, updatedTimer.RetryPolicy) // JSON numbers become float64
	assert.Equal(t, int32(60), updatedTimer.CallbackTimeoutSeconds)
	assert.Equal(t, int32(0), updatedTimer.Attempts)                       // Should be reset to 0
	assert.Equal(t, timer.ExecuteAt.Unix(), updatedTimer.ExecuteAt.Unix()) // Same execution time
	assert.Equal(t, timer.CreatedAt.Unix(), updatedTimer.CreatedAt.Unix()) // Creation time preserved
}

func TestUpdateTimerNoLock_ExecutionTimeChange(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test-namespace"
	timerId := "test-timer-2"

	// Create a timer first
	timer := &databases.DbTimer{
		Id:                     timerId,
		TimerUuid:              databases.GenerateTimerUUID(namespace, timerId),
		Namespace:              namespace,
		ExecuteAt:              time.Now().UTC().Add(10 * time.Minute),
		CallbackUrl:            "http://example.com/callback",
		Payload:                map[string]interface{}{"key": "value"},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 3},
		CallbackTimeoutSeconds: 30,
		CreatedAt:              time.Now().UTC(),
		Attempts:               5, // Non-zero attempts
	}

	err := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
	require.Nil(t, err)

	// Update the timer with different execution time (delete+insert)
	newExecuteAt := time.Now().UTC().Add(20 * time.Minute)
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                timerId,
		ExecuteAt:              newExecuteAt, // Different execution time
		CallbackUrl:            "http://example.com/new-callback",
		Payload:                map[string]interface{}{"key": "new-value"},
		RetryPolicy:            nil, // Clear retry policy
		CallbackTimeoutSeconds: 45,
	}

	err = store.UpdateTimerNoLock(ctx, shardId, namespace, updateRequest)
	require.Nil(t, err)

	// Verify the timer was updated
	updatedTimer, err := store.GetTimer(ctx, shardId, namespace, timerId)
	require.Nil(t, err)
	require.NotNil(t, updatedTimer)

	assert.Equal(t, "http://example.com/new-callback", updatedTimer.CallbackUrl)
	assert.Equal(t, map[string]interface{}{"key": "new-value"}, updatedTimer.Payload)
	assert.Nil(t, updatedTimer.RetryPolicy) // Should be nil
	assert.Equal(t, int32(45), updatedTimer.CallbackTimeoutSeconds)
	assert.Equal(t, int32(0), updatedTimer.Attempts)                       // Should be reset to 0
	assert.Equal(t, newExecuteAt.Unix(), updatedTimer.ExecuteAt.Unix())    // New execution time
	assert.Equal(t, timer.CreatedAt.Unix(), updatedTimer.CreatedAt.Unix()) // Creation time preserved
}

func TestUpdateTimerNoLock_NotExists(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test-namespace"
	timerId := "non-existent-timer"

	// Try to update a non-existent timer
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                timerId,
		ExecuteAt:              time.Now().UTC().Add(10 * time.Minute),
		CallbackUrl:            "http://example.com/callback",
		Payload:                map[string]interface{}{"key": "value"},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 3},
		CallbackTimeoutSeconds: 30,
	}

	err := store.UpdateTimerNoLock(ctx, shardId, namespace, updateRequest)
	require.NotNil(t, err)
	assert.True(t, err.NotExists)
}

func TestUpdateTimerNoLock_NilPayload(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test-namespace"
	timerId := "test-timer-3"

	// Create a timer with payload first
	timer := &databases.DbTimer{
		Id:                     timerId,
		TimerUuid:              databases.GenerateTimerUUID(namespace, timerId),
		Namespace:              namespace,
		ExecuteAt:              time.Now().UTC().Add(10 * time.Minute),
		CallbackUrl:            "http://example.com/callback",
		Payload:                map[string]interface{}{"key": "value"},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 3},
		CallbackTimeoutSeconds: 30,
		CreatedAt:              time.Now().UTC(),
		Attempts:               0,
	}

	err := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
	require.Nil(t, err)

	// Update the timer with nil payload
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                timerId,
		ExecuteAt:              timer.ExecuteAt,
		CallbackUrl:            "http://example.com/new-callback",
		Payload:                nil, // Clear payload
		RetryPolicy:            nil, // Clear retry policy
		CallbackTimeoutSeconds: 60,
	}

	err = store.UpdateTimerNoLock(ctx, shardId, namespace, updateRequest)
	require.Nil(t, err)

	// Verify the timer was updated
	updatedTimer, err := store.GetTimer(ctx, shardId, namespace, timerId)
	require.Nil(t, err)
	require.NotNil(t, updatedTimer)

	assert.Equal(t, "http://example.com/new-callback", updatedTimer.CallbackUrl)
	assert.Nil(t, updatedTimer.Payload)     // Should be nil
	assert.Nil(t, updatedTimer.RetryPolicy) // Should be nil
	assert.Equal(t, int32(60), updatedTimer.CallbackTimeoutSeconds)
}

func TestDeleteTimerNoLock_Success(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test-namespace"
	timerId := "test-timer-4"

	// Create a timer first
	timer := &databases.DbTimer{
		Id:                     timerId,
		TimerUuid:              databases.GenerateTimerUUID(namespace, timerId),
		Namespace:              namespace,
		ExecuteAt:              time.Now().UTC().Add(10 * time.Minute),
		CallbackUrl:            "http://example.com/callback",
		Payload:                map[string]interface{}{"key": "value"},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 3},
		CallbackTimeoutSeconds: 30,
		CreatedAt:              time.Now().UTC(),
		Attempts:               0,
	}

	err := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
	require.Nil(t, err)

	// Verify timer exists
	existingTimer, err := store.GetTimer(ctx, shardId, namespace, timerId)
	require.Nil(t, err)
	require.NotNil(t, existingTimer)

	// Delete the timer
	err = store.DeleteTimerNoLock(ctx, shardId, namespace, timerId)
	require.Nil(t, err)

	// Verify timer is deleted
	deletedTimer, err := store.GetTimer(ctx, shardId, namespace, timerId)
	require.NotNil(t, err)
	require.Nil(t, deletedTimer)
	assert.True(t, err.NotExists)
}

func TestDeleteTimerNoLock_NotExists(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test-namespace"
	timerId := "non-existent-timer"

	// Try to delete a non-existent timer
	err := store.DeleteTimerNoLock(ctx, shardId, namespace, timerId)
	require.NotNil(t, err)
	assert.True(t, err.NotExists)
}

func TestDeleteTimerNoLock_WrongNamespace(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test-namespace"
	wrongNamespace := "wrong-namespace"
	timerId := "test-timer-5"

	// Create a timer in the correct namespace
	timer := &databases.DbTimer{
		Id:                     timerId,
		TimerUuid:              databases.GenerateTimerUUID(namespace, timerId),
		Namespace:              namespace,
		ExecuteAt:              time.Now().UTC().Add(10 * time.Minute),
		CallbackUrl:            "http://example.com/callback",
		Payload:                map[string]interface{}{"key": "value"},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 3},
		CallbackTimeoutSeconds: 30,
		CreatedAt:              time.Now().UTC(),
		Attempts:               0,
	}

	err := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
	require.Nil(t, err)

	// Try to delete with wrong namespace
	err = store.DeleteTimerNoLock(ctx, shardId, wrongNamespace, timerId)
	require.NotNil(t, err)
	assert.True(t, err.NotExists)

	// Verify timer still exists in correct namespace
	existingTimer, err := store.GetTimer(ctx, shardId, namespace, timerId)
	require.Nil(t, err)
	require.NotNil(t, existingTimer)
}

func TestUpdateTimerNoLock_ConcurrentOperations(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test-namespace"
	timerId := "test-timer-6"

	// Create a timer first
	timer := &databases.DbTimer{
		Id:                     timerId,
		TimerUuid:              databases.GenerateTimerUUID(namespace, timerId),
		Namespace:              namespace,
		ExecuteAt:              time.Now().UTC().Add(10 * time.Minute),
		CallbackUrl:            "http://example.com/callback",
		Payload:                map[string]interface{}{"key": "value"},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 3},
		CallbackTimeoutSeconds: 30,
		CreatedAt:              time.Now().UTC(),
		Attempts:               0,
	}

	err := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
	require.Nil(t, err)

	// Simulate concurrent updates - both should succeed since there's no locking
	updateRequest1 := &databases.UpdateDbTimerRequest{
		TimerId:                timerId,
		ExecuteAt:              timer.ExecuteAt,
		CallbackUrl:            "http://example.com/callback-1",
		Payload:                map[string]interface{}{"key": "value-1"},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 1},
		CallbackTimeoutSeconds: 10,
	}

	updateRequest2 := &databases.UpdateDbTimerRequest{
		TimerId:                timerId,
		ExecuteAt:              timer.ExecuteAt,
		CallbackUrl:            "http://example.com/callback-2",
		Payload:                map[string]interface{}{"key": "value-2"},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 2},
		CallbackTimeoutSeconds: 20,
	}

	// Both updates should succeed (no version checking)
	err1 := store.UpdateTimerNoLock(ctx, shardId, namespace, updateRequest1)
	err2 := store.UpdateTimerNoLock(ctx, shardId, namespace, updateRequest2)

	// At least one should succeed (the last one wins)
	require.True(t, err1 == nil || err2 == nil, "At least one update should succeed")

	// Verify timer exists (one of the updates should have worked)
	finalTimer, err := store.GetTimer(ctx, shardId, namespace, timerId)
	require.Nil(t, err)
	require.NotNil(t, finalTimer)

	// The final state should be either from update1 or update2
	assert.Contains(t, []string{"http://example.com/callback-1", "http://example.com/callback-2"}, finalTimer.CallbackUrl)
}

func TestDeleteTimerNoLock_AfterUpdate(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test-namespace"
	timerId := "test-timer-7"

	// Create a timer first
	timer := &databases.DbTimer{
		Id:                     timerId,
		TimerUuid:              databases.GenerateTimerUUID(namespace, timerId),
		Namespace:              namespace,
		ExecuteAt:              time.Now().UTC().Add(10 * time.Minute),
		CallbackUrl:            "http://example.com/callback",
		Payload:                map[string]interface{}{"key": "value"},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 3},
		CallbackTimeoutSeconds: 30,
		CreatedAt:              time.Now().UTC(),
		Attempts:               0,
	}

	err := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
	require.Nil(t, err)

	// Update the timer first
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                timerId,
		ExecuteAt:              time.Now().UTC().Add(20 * time.Minute), // Different execution time
		CallbackUrl:            "http://example.com/new-callback",
		Payload:                map[string]interface{}{"key": "new-value"},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 5},
		CallbackTimeoutSeconds: 60,
	}

	err = store.UpdateTimerNoLock(ctx, shardId, namespace, updateRequest)
	require.Nil(t, err)

	// Verify timer was updated
	updatedTimer, err := store.GetTimer(ctx, shardId, namespace, timerId)
	require.Nil(t, err)
	require.NotNil(t, updatedTimer)

	// Now delete the updated timer
	err = store.DeleteTimerNoLock(ctx, shardId, namespace, timerId)
	require.Nil(t, err)

	// Verify timer is deleted
	deletedTimer, err := store.GetTimer(ctx, shardId, namespace, timerId)
	require.NotNil(t, err)
	require.Nil(t, deletedTimer)
	assert.True(t, err.NotExists)
}
