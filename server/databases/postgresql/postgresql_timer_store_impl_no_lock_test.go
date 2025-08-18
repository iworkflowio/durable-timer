package postgresql

import (
	"context"
	"testing"
	"time"

	"github.com/iworkflowio/durable-timer/databases"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// UpdateTimerNoLock Tests

func TestUpdateTimerNoLock_Basic(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"
	timerId := "test-timer-update-no-lock"

	// Create a timer first using CreateTimerNoLock
	timer := &databases.DbTimer{
		Id:                     timerId,
		TimerUuid:              databases.GenerateTimerUUID(namespace, timerId),
		Namespace:              namespace,
		ExecuteAt:              time.Now().UTC().Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		Payload:                map[string]interface{}{"key": "value"},
		CallbackTimeoutSeconds: 30,
		CreatedAt:              time.Now().UTC(),
		Attempts:               0,
	}

	createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
	require.Nil(t, createErr)

	// Now update the timer
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                timerId,
		ExecuteAt:              timer.ExecuteAt, // Same ExecuteAt
		CallbackUrl:            "https://updated.com/callback",
		Payload:                map[string]interface{}{"key": "updated_value"},
		CallbackTimeoutSeconds: 60,
	}

	updateErr := store.UpdateTimerNoLock(ctx, shardId, namespace, updateRequest)
	assert.Nil(t, updateErr)

	// Verify the update
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timerId)
	require.Nil(t, getErr)
	assert.Equal(t, updateRequest.CallbackUrl, retrievedTimer.CallbackUrl)
	assert.Equal(t, updateRequest.CallbackTimeoutSeconds, retrievedTimer.CallbackTimeoutSeconds)

	// Verify payload was updated
	payload, ok := retrievedTimer.Payload.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "updated_value", payload["key"])

	// Verify other fields were preserved
	assert.WithinDuration(t, timer.CreatedAt, retrievedTimer.CreatedAt, time.Second)
	assert.Equal(t, timer.Attempts, retrievedTimer.Attempts)
	expectedUuid := databases.GenerateTimerUUID(namespace, updateRequest.TimerId)
	assert.Equal(t, expectedUuid, retrievedTimer.TimerUuid)
}

func TestUpdateTimerNoLock_WithExecuteAtChange(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"
	timerId := "test-timer-execute-at-change"

	// Create a timer first
	originalExecuteAt := time.Now().UTC().Add(5 * time.Minute)
	timer := &databases.DbTimer{
		Id:                     timerId,
		TimerUuid:              databases.GenerateTimerUUID(namespace, timerId),
		Namespace:              namespace,
		ExecuteAt:              originalExecuteAt,
		CallbackUrl:            "https://example.com/callback",
		Payload:                map[string]interface{}{"key": "value"},
		CallbackTimeoutSeconds: 30,
		CreatedAt:              time.Now().UTC(),
		Attempts:               0,
	}

	createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
	require.Nil(t, createErr)

	// Update with different ExecuteAt
	newExecuteAt := time.Now().UTC().Add(10 * time.Minute)
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                timerId,
		ExecuteAt:              newExecuteAt,
		CallbackUrl:            "https://updated.com/callback",
		Payload:                map[string]interface{}{"key": "updated_value"},
		CallbackTimeoutSeconds: 60,
	}

	updateErr := store.UpdateTimerNoLock(ctx, shardId, namespace, updateRequest)
	assert.Nil(t, updateErr)

	// Verify the update
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timerId)
	require.Nil(t, getErr)
	assert.Equal(t, newExecuteAt.Unix(), retrievedTimer.ExecuteAt.Unix())
	assert.Equal(t, updateRequest.CallbackUrl, retrievedTimer.CallbackUrl)
	assert.Equal(t, updateRequest.CallbackTimeoutSeconds, retrievedTimer.CallbackTimeoutSeconds)

	// Verify other fields were preserved
	assert.WithinDuration(t, timer.CreatedAt, retrievedTimer.CreatedAt, time.Second)
	assert.Equal(t, timer.Attempts, retrievedTimer.Attempts)
	expectedUuid := databases.GenerateTimerUUID(namespace, timerId)
	assert.Equal(t, expectedUuid, retrievedTimer.TimerUuid)
}

func TestUpdateTimerNoLock_TimerNotFound(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"
	timerId := "non-existent-timer"

	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                timerId,
		ExecuteAt:              time.Now().UTC().Add(5 * time.Minute),
		CallbackUrl:            "https://updated.com/callback",
		CallbackTimeoutSeconds: 60,
	}

	updateErr := store.UpdateTimerNoLock(ctx, shardId, namespace, updateRequest)
	require.NotNil(t, updateErr)
	assert.True(t, databases.IsDbErrorNotExists(updateErr))
}

func TestUpdateTimerNoLock_WithNilPayload(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"
	timerId := "test-timer-update-nil"

	// Create a timer first with payload
	timer := &databases.DbTimer{
		Id:                     timerId,
		TimerUuid:              databases.GenerateTimerUUID(namespace, timerId),
		Namespace:              namespace,
		ExecuteAt:              time.Now().UTC().Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		Payload:                map[string]interface{}{"key": "value"},
		CallbackTimeoutSeconds: 30,
		CreatedAt:              time.Now().UTC(),
		Attempts:               0,
	}

	createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
	require.Nil(t, createErr)

	// Update with nil payload
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                timerId,
		ExecuteAt:              timer.ExecuteAt,
		CallbackUrl:            "https://updated.com/callback",
		Payload:                nil,
		CallbackTimeoutSeconds: 60,
	}

	updateErr := store.UpdateTimerNoLock(ctx, shardId, namespace, updateRequest)
	assert.Nil(t, updateErr)

	// Verify the update
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timerId)
	require.Nil(t, getErr)
	assert.Equal(t, updateRequest.CallbackUrl, retrievedTimer.CallbackUrl)
	assert.Nil(t, retrievedTimer.Payload)
}

// DeleteTimerNoLock Tests

func TestDeleteTimerNoLock_Success(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"
	timerId := "test-timer-no-lock"

	// Create a timer first
	timer := &databases.DbTimer{
		Id:                     timerId,
		TimerUuid:              databases.GenerateTimerUUID(namespace, timerId),
		Namespace:              namespace,
		ExecuteAt:              time.Now().UTC().Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              time.Now().UTC(),
		Attempts:               0,
	}

	createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
	require.Nil(t, createErr)

	// Delete the timer
	deleteErr := store.DeleteTimerNoLock(ctx, shardId, namespace, timerId)
	assert.Nil(t, deleteErr)

	// Verify timer is deleted
	_, getErr := store.GetTimer(ctx, shardId, namespace, timerId)
	require.NotNil(t, getErr)
	assert.True(t, databases.IsDbErrorNotExists(getErr))
}

func TestDeleteTimerNoLock_TimerNotFound(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"
	timerId := "non-existent-timer"

	// Try to delete non-existent timer
	deleteErr := store.DeleteTimerNoLock(ctx, shardId, namespace, timerId)
	require.NotNil(t, deleteErr)
	assert.True(t, databases.IsDbErrorNotExists(deleteErr))
}

func TestUpdateTimerNoLock_WithShardMetadata(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"
	timerId := "test-timer-with-metadata"

	// Test that updating timer works without shard version checks
	// (This test verifies that UpdateTimerNoLock doesn't perform shard metadata validation)

	// Create a timer first
	timer := &databases.DbTimer{
		Id:                     timerId,
		TimerUuid:              databases.GenerateTimerUUID(namespace, timerId),
		Namespace:              namespace,
		ExecuteAt:              time.Now().UTC().Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              time.Now().UTC(),
		Attempts:               0,
	}

	createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
	require.Nil(t, createErr)

	// Update timer (this should work without any shard version checks)
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                timerId,
		ExecuteAt:              timer.ExecuteAt,
		CallbackUrl:            "https://updated.com/callback",
		CallbackTimeoutSeconds: 60,
	}

	updateErr := store.UpdateTimerNoLock(ctx, shardId, namespace, updateRequest)
	assert.Nil(t, updateErr)

	// Verify the update
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timerId)
	require.Nil(t, getErr)
	assert.Equal(t, updateRequest.CallbackUrl, retrievedTimer.CallbackUrl)
	assert.Equal(t, updateRequest.CallbackTimeoutSeconds, retrievedTimer.CallbackTimeoutSeconds)
}

func TestDeleteTimerNoLock_MultipleDifferentNamespaces(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	timerId := "same-timer-id"

	namespaces := []string{"namespace1", "namespace2", "namespace3"}

	// Create timers in different namespaces with same timer ID
	for _, namespace := range namespaces {
		timer := &databases.DbTimer{
			Id:                     timerId,
			TimerUuid:              databases.GenerateTimerUUID(namespace, timerId),
			Namespace:              namespace,
			ExecuteAt:              time.Now().UTC().Add(5 * time.Minute),
			CallbackUrl:            "https://example.com/callback",
			CallbackTimeoutSeconds: 30,
			CreatedAt:              time.Now().UTC(),
			Attempts:               0,
		}

		createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
		require.Nil(t, createErr)
	}

	// Delete timer from one namespace
	targetNamespace := "namespace2"
	deleteErr := store.DeleteTimerNoLock(ctx, shardId, targetNamespace, timerId)
	assert.Nil(t, deleteErr)

	// Verify only the timer from target namespace is deleted
	_, getErr := store.GetTimer(ctx, shardId, targetNamespace, timerId)
	require.NotNil(t, getErr)
	assert.True(t, databases.IsDbErrorNotExists(getErr))

	// Verify timers in other namespaces still exist
	for _, namespace := range namespaces {
		if namespace != targetNamespace {
			_, getErr := store.GetTimer(ctx, shardId, namespace, timerId)
			assert.Nil(t, getErr, "Timer in namespace %s should still exist", namespace)
		}
	}
}

func TestUpdateTimerNoLock_WithRetryPolicy(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"
	timerId := "test-timer-retry-policy"

	// Create a timer first
	timer := &databases.DbTimer{
		Id:                     timerId,
		TimerUuid:              databases.GenerateTimerUUID(namespace, timerId),
		Namespace:              namespace,
		ExecuteAt:              time.Now().UTC().Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              time.Now().UTC(),
		Attempts:               0,
	}

	createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
	require.Nil(t, createErr)

	// Update with retry policy
	retryPolicy := map[string]interface{}{
		"max_attempts": 3,
		"backoff_ms":   1000,
	}

	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                timerId,
		ExecuteAt:              timer.ExecuteAt,
		CallbackUrl:            "https://updated.com/callback",
		RetryPolicy:            retryPolicy,
		CallbackTimeoutSeconds: 60,
	}

	updateErr := store.UpdateTimerNoLock(ctx, shardId, namespace, updateRequest)
	assert.Nil(t, updateErr)

	// Verify the update
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timerId)
	require.Nil(t, getErr)

	// Verify retry policy was updated
	retrievedPolicy, ok := retrievedTimer.RetryPolicy.(map[string]interface{})
	require.True(t, ok)

	// JSON numbers get decoded as float64
	assert.Equal(t, float64(3), retrievedPolicy["max_attempts"])
	assert.Equal(t, float64(1000), retrievedPolicy["backoff_ms"])
}
