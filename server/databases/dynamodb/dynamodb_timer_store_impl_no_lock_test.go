package dynamodb

import (
	"context"
	"testing"
	"time"

	"github.com/iworkflowio/durable-timer/databases"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateTimerNoLock_Success(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"
	timerId := "test-timer-update-no-lock"

	// First, create a shard record (though NoLock doesn't use it)
	ownerAddr := "owner-1"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	assert.Nil(t, err)
	require.NotNil(t, currentShardInfo)
	shardVersion := currentShardInfo.ShardVersion

	// Create a timer to update
	now := time.Now().UTC().Truncate(time.Millisecond)
	originalTimer := &databases.DbTimer{
		Id:                     timerId,
		TimerUuid:              databases.GenerateTimerUUID(namespace, timerId),
		Namespace:              namespace,
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback-original",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Attempts:               0,
		Payload:                map[string]interface{}{"original": "value"},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 3},
	}

	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, originalTimer)
	require.Nil(t, createErr)

	// Update the timer using NoLock version
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                timerId,
		CallbackUrl:            "https://example.com/callback-updated",
		CallbackTimeoutSeconds: 60,
		Payload:                map[string]interface{}{"updated": "value", "number": 42},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 5, "backoff": "exponential"},
	}

	updateErr := store.UpdateTimerNoLock(ctx, shardId, namespace, updateRequest)
	require.Nil(t, updateErr)

	// Verify the timer was updated
	updatedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timerId)
	require.Nil(t, getErr)
	require.NotNil(t, updatedTimer)

	// Check that updated fields have changed
	assert.Equal(t, "https://example.com/callback-updated", updatedTimer.CallbackUrl)
	assert.Equal(t, int32(60), updatedTimer.CallbackTimeoutSeconds)

	// JSON deserialization converts int to float64
	expectedPayload := map[string]interface{}{"updated": "value", "number": float64(42)}
	expectedRetryPolicy := map[string]interface{}{"maxAttempts": float64(5), "backoff": "exponential"}
	assert.Equal(t, expectedPayload, updatedTimer.Payload)
	assert.Equal(t, expectedRetryPolicy, updatedTimer.RetryPolicy)

	// Check that non-updated fields remain the same
	assert.Equal(t, originalTimer.Id, updatedTimer.Id)
	assert.Equal(t, originalTimer.TimerUuid, updatedTimer.TimerUuid)
	assert.Equal(t, originalTimer.Namespace, updatedTimer.Namespace)
	assert.True(t, originalTimer.ExecuteAt.Equal(updatedTimer.ExecuteAt))
	assert.True(t, originalTimer.CreatedAt.Equal(updatedTimer.CreatedAt))
	assert.Equal(t, originalTimer.Attempts, updatedTimer.Attempts)
}

func TestUpdateTimerNoLock_PartialUpdate(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"
	timerId := "test-timer-partial-update"

	// Create shard and timer
	ownerAddr := "owner-1"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	assert.Nil(t, err)
	require.NotNil(t, currentShardInfo)
	shardVersion := currentShardInfo.ShardVersion

	now := time.Now().UTC().Truncate(time.Millisecond)
	originalTimer := &databases.DbTimer{
		Id:                     timerId,
		TimerUuid:              databases.GenerateTimerUUID(namespace, timerId),
		Namespace:              namespace,
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback-original",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Attempts:               0,
		Payload:                map[string]interface{}{"original": "value"},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 3},
	}

	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, originalTimer)
	require.Nil(t, createErr)

	// Update only the callback URL
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:     timerId,
		CallbackUrl: "https://example.com/callback-updated-only",
		// Note: Other fields are not set (zero values), so they shouldn't be updated
	}

	updateErr := store.UpdateTimerNoLock(ctx, shardId, namespace, updateRequest)
	require.Nil(t, updateErr)

	// Verify only the callback URL was updated
	updatedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timerId)
	require.Nil(t, getErr)
	require.NotNil(t, updatedTimer)

	// Check that only the callback URL changed
	assert.Equal(t, "https://example.com/callback-updated-only", updatedTimer.CallbackUrl)

	// Check that other fields remain unchanged
	assert.Equal(t, originalTimer.CallbackTimeoutSeconds, updatedTimer.CallbackTimeoutSeconds)

	// JSON deserialization converts int to float64, so we need to compare with adjusted types
	expectedPayload := map[string]interface{}{"original": "value"}
	expectedRetryPolicy := map[string]interface{}{"maxAttempts": float64(3)}
	assert.Equal(t, expectedPayload, updatedTimer.Payload)
	assert.Equal(t, expectedRetryPolicy, updatedTimer.RetryPolicy)
}

func TestUpdateTimerNoLock_NotFound(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Try to update a non-existent timer
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:     "non-existent-timer",
		CallbackUrl: "https://example.com/callback",
	}

	updateErr := store.UpdateTimerNoLock(ctx, shardId, namespace, updateRequest)
	require.NotNil(t, updateErr)
	assert.True(t, databases.IsDbErrorNotExists(updateErr))
}

func TestUpdateTimerNoLock_ExecuteAtNotSupported(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Try to update ExecuteAt, which should not be supported
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:   "test-timer",
		ExecuteAt: time.Now().Add(1 * time.Hour),
	}

	updateErr := store.UpdateTimerNoLock(ctx, shardId, namespace, updateRequest)
	require.NotNil(t, updateErr)
	assert.True(t, updateErr.NotSupport)
}

func TestUpdateTimerNoLock_EmptyUpdate(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"
	timerId := "test-timer-empty-update"

	// Create shard and timer
	ownerAddr := "owner-1"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	assert.Nil(t, err)
	require.NotNil(t, currentShardInfo)
	shardVersion := currentShardInfo.ShardVersion

	now := time.Now().UTC().Truncate(time.Millisecond)
	originalTimer := &databases.DbTimer{
		Id:                     timerId,
		TimerUuid:              databases.GenerateTimerUUID(namespace, timerId),
		Namespace:              namespace,
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback-original",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Attempts:               0,
	}

	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, originalTimer)
	require.Nil(t, createErr)

	// Update with no actual changes (all zero values)
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId: timerId,
		// All other fields are zero values
	}

	updateErr := store.UpdateTimerNoLock(ctx, shardId, namespace, updateRequest)
	require.Nil(t, updateErr) // Should succeed with no-op

	// Verify timer is unchanged
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timerId)
	require.Nil(t, getErr)
	require.NotNil(t, retrievedTimer)

	// Should be identical to original
	assert.Equal(t, originalTimer.CallbackUrl, retrievedTimer.CallbackUrl)
	assert.Equal(t, originalTimer.CallbackTimeoutSeconds, retrievedTimer.CallbackTimeoutSeconds)
}

func TestDeleteTimerNoLock_Success(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"
	timerId := "test-timer-delete-no-lock"

	// Create shard and timer
	ownerAddr := "owner-1"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	assert.Nil(t, err)
	require.NotNil(t, currentShardInfo)
	shardVersion := currentShardInfo.ShardVersion

	now := time.Now().UTC().Truncate(time.Millisecond)
	timer := &databases.DbTimer{
		Id:                     timerId,
		TimerUuid:              databases.GenerateTimerUUID(namespace, timerId),
		Namespace:              namespace,
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Attempts:               0,
	}

	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Verify timer exists
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timerId)
	require.Nil(t, getErr)
	require.NotNil(t, retrievedTimer)

	// Delete the timer using NoLock version
	deleteErr := store.DeleteTimerNoLock(ctx, shardId, namespace, timerId)
	require.Nil(t, deleteErr)

	// Verify timer is deleted
	deletedTimer, getErr2 := store.GetTimer(ctx, shardId, namespace, timerId)
	assert.Nil(t, deletedTimer)
	assert.NotNil(t, getErr2)
	assert.True(t, databases.IsDbErrorNotExists(getErr2))
}

func TestDeleteTimerNoLock_NotFound(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"
	timerId := "non-existent-timer"

	// Delete a non-existent timer - should return NotExists error
	deleteErr := store.DeleteTimerNoLock(ctx, shardId, namespace, timerId)
	require.NotNil(t, deleteErr)
	assert.True(t, databases.IsDbErrorNotExists(deleteErr))
}

func TestDeleteTimerNoLock_MultipleTimers(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Create shard
	ownerAddr := "owner-1"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	assert.Nil(t, err)
	require.NotNil(t, currentShardInfo)
	shardVersion := currentShardInfo.ShardVersion

	// Create multiple timers
	now := time.Now().UTC().Truncate(time.Millisecond)
	timerIds := []string{"timer-1", "timer-2", "timer-3"}

	for _, timerId := range timerIds {
		timer := &databases.DbTimer{
			Id:                     timerId,
			TimerUuid:              databases.GenerateTimerUUID(namespace, timerId),
			Namespace:              namespace,
			ExecuteAt:              now.Add(10 * time.Minute),
			CallbackUrl:            "https://example.com/callback",
			CallbackTimeoutSeconds: 30,
			CreatedAt:              now,
			Attempts:               0,
		}

		createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
		require.Nil(t, createErr)
	}

	// Verify all timers exist
	for _, timerId := range timerIds {
		retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timerId)
		require.Nil(t, getErr)
		require.NotNil(t, retrievedTimer)
	}

	// Delete all timers using NoLock
	for _, timerId := range timerIds {
		deleteErr := store.DeleteTimerNoLock(ctx, shardId, namespace, timerId)
		require.Nil(t, deleteErr)
	}

	// Verify all timers are deleted
	for _, timerId := range timerIds {
		deletedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timerId)
		assert.Nil(t, deletedTimer)
		assert.NotNil(t, getErr)
		assert.True(t, databases.IsDbErrorNotExists(getErr))
	}
}

func TestUpdateTimerNoLock_ComplexPayload(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"
	timerId := "test-timer-complex-payload"

	// Create shard and timer
	ownerAddr := "owner-1"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	assert.Nil(t, err)
	require.NotNil(t, currentShardInfo)
	shardVersion := currentShardInfo.ShardVersion

	now := time.Now().UTC().Truncate(time.Millisecond)
	originalTimer := &databases.DbTimer{
		Id:                     timerId,
		TimerUuid:              databases.GenerateTimerUUID(namespace, timerId),
		Namespace:              namespace,
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback-original",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Attempts:               0,
		Payload:                map[string]interface{}{"simple": "value"},
		RetryPolicy:            map[string]interface{}{"simple": "policy"},
	}

	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, originalTimer)
	require.Nil(t, createErr)

	// Update with complex nested payload and retry policy
	complexPayload := map[string]interface{}{
		"nested": map[string]interface{}{
			"deep": map[string]interface{}{
				"value": "test",
				"array": []interface{}{1, 2, 3, "string"},
			},
		},
		"numbers": []interface{}{42, 3.14, -100},
		"boolean": true,
		"null":    nil,
	}

	complexRetryPolicy := map[string]interface{}{
		"strategy": "exponential",
		"config": map[string]interface{}{
			"maxAttempts":   10,
			"baseDelay":     1000,
			"maxDelay":      60000,
			"multiplier":    2.0,
			"jitterEnabled": true,
		},
		"exceptions": []interface{}{"TimeoutException", "NetworkException"},
	}

	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:     timerId,
		Payload:     complexPayload,
		RetryPolicy: complexRetryPolicy,
	}

	updateErr := store.UpdateTimerNoLock(ctx, shardId, namespace, updateRequest)
	require.Nil(t, updateErr)

	// Verify the complex payload was stored and retrieved correctly
	updatedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timerId)
	require.Nil(t, getErr)
	require.NotNil(t, updatedTimer)

	// Note: JSON marshaling/unmarshaling converts numbers to float64
	expectedPayload := map[string]interface{}{
		"nested": map[string]interface{}{
			"deep": map[string]interface{}{
				"value": "test",
				"array": []interface{}{float64(1), float64(2), float64(3), "string"},
			},
		},
		"numbers": []interface{}{float64(42), float64(3.14), float64(-100)},
		"boolean": true,
		"null":    nil,
	}

	expectedRetryPolicy := map[string]interface{}{
		"strategy": "exponential",
		"config": map[string]interface{}{
			"maxAttempts":   float64(10),
			"baseDelay":     float64(1000),
			"maxDelay":      float64(60000),
			"multiplier":    float64(2.0),
			"jitterEnabled": true,
		},
		"exceptions": []interface{}{"TimeoutException", "NetworkException"},
	}

	assert.Equal(t, expectedPayload, updatedTimer.Payload)
	assert.Equal(t, expectedRetryPolicy, updatedTimer.RetryPolicy)
}
