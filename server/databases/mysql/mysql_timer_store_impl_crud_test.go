package mysql

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
	require.Nil(t, err)
	shardVersion := currentShardInfo.ShardVersion
	require.Nil(t, err)
	require.Equal(t, int64(1), shardVersion)

	// Create a timer to retrieve - use UTC times to avoid timezone issues
	now := time.Now().UTC().Truncate(time.Millisecond)
	timer := &databases.DbTimer{
		Id:                     "test-timer-get",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "test-timer-get"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback-get",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Payload:                map[string]interface{}{"key": "value", "number": 42},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 3, "backoff": "exponential"},
	}

	// Insert the timer
	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Get the timer
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timer.Id)
	require.Nil(t, getErr)
	require.NotNil(t, retrievedTimer)

	// Verify all fields match
	assert.Equal(t, timer.Id, retrievedTimer.Id)
	assert.Equal(t, timer.TimerUuid, retrievedTimer.TimerUuid)
	assert.Equal(t, timer.Namespace, retrievedTimer.Namespace)
	assert.Equal(t, timer.CallbackUrl, retrievedTimer.CallbackUrl)
	assert.Equal(t, timer.CallbackTimeoutSeconds, retrievedTimer.CallbackTimeoutSeconds)
	assert.True(t, timer.ExecuteAt.Equal(retrievedTimer.ExecuteAt))
	assert.True(t, timer.CreatedAt.Equal(retrievedTimer.CreatedAt))

	// Verify payload
	assert.NotNil(t, retrievedTimer.Payload)
	payloadMap, ok := retrievedTimer.Payload.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "value", payloadMap["key"])
	assert.Equal(t, float64(42), payloadMap["number"]) // JSON unmarshals numbers as float64

	// Verify retry policy
	assert.NotNil(t, retrievedTimer.RetryPolicy)
	retryMap, ok := retrievedTimer.RetryPolicy.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, float64(3), retryMap["maxAttempts"])
	assert.Equal(t, "exponential", retryMap["backoff"])
}

func TestGetTimer_NotExists(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Try to get a non-existent timer
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, "non-existent-timer")
	assert.Nil(t, retrievedTimer)
	assert.NotNil(t, getErr)
	assert.True(t, databases.IsDbErrorNotExists(getErr))
}

func TestGetTimer_NilFields(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Create shard record
	ownerAddr := "owner-1"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	require.Nil(t, err)
	shardVersion := currentShardInfo.ShardVersion
	require.Nil(t, err)

	// Create timer with nil payload and retry policy
	now := time.Now().UTC().Truncate(time.Millisecond)
	timer := &databases.DbTimer{
		Id:                     "test-timer-nil-fields",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "test-timer-nil-fields"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback-nil",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Payload:                nil,
		RetryPolicy:            nil,
	}

	// Insert the timer
	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Get the timer
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timer.Id)
	require.Nil(t, getErr)
	require.NotNil(t, retrievedTimer)

	// Verify nil fields
	assert.Nil(t, retrievedTimer.Payload)
	assert.Nil(t, retrievedTimer.RetryPolicy)
}

func TestDeleteTimer_Success(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// First, create a shard record
	ownerAddr := "owner-1"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	require.Nil(t, err)
	shardVersion := currentShardInfo.ShardVersion
	require.Nil(t, err)
	require.Equal(t, int64(1), shardVersion)

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
	}

	// Insert the timer
	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Verify timer exists before deletion
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timer.Id)
	require.Nil(t, getErr)
	require.NotNil(t, retrievedTimer)

	// Delete the timer
	deleteErr := store.DeleteTimer(ctx, shardId, shardVersion, namespace, timer.Id)
	require.Nil(t, deleteErr)

	// Verify timer no longer exists
	retrievedTimer, getErr = store.GetTimer(ctx, shardId, namespace, timer.Id)
	assert.Nil(t, retrievedTimer)
	assert.NotNil(t, getErr)
	assert.True(t, databases.IsDbErrorNotExists(getErr))
}

func TestDeleteTimer_NotExists(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// First, create a shard record
	ownerAddr := "owner-1"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	require.Nil(t, err)
	shardVersion := currentShardInfo.ShardVersion
	require.Nil(t, err)
	require.Equal(t, int64(1), shardVersion)

	// Try to delete a non-existent timer - should succeed (idempotent)
	deleteErr := store.DeleteTimer(ctx, shardId, shardVersion, namespace, "non-existent-timer")
	assert.Nil(t, deleteErr)
}

func TestDeleteTimer_ShardVersionMismatch(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// First, create a shard record
	ownerAddr := "owner-1"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	require.Nil(t, err)
	shardVersion := currentShardInfo.ShardVersion
	require.Nil(t, err)
	require.Equal(t, int64(1), shardVersion)

	// Create a timer to delete
	now := time.Now().UTC().Truncate(time.Millisecond)
	timer := &databases.DbTimer{
		Id:                     "test-timer-shard-version",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "test-timer-shard-version"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
	}

	// Insert the timer
	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Try to delete with wrong shard version
	wrongShardVersion := shardVersion + 1
	deleteErr := store.DeleteTimer(ctx, shardId, wrongShardVersion, namespace, timer.Id)
	require.NotNil(t, deleteErr)
	assert.True(t, deleteErr.ShardConditionFail)

	// Verify timer still exists
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timer.Id)
	require.Nil(t, getErr)
	require.NotNil(t, retrievedTimer)
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
	require.Nil(t, err)
	shardVersion := currentShardInfo.ShardVersion
	require.Nil(t, err)

	// Create original timer
	now := time.Now().UTC().Truncate(time.Millisecond)
	originalExecuteAt := now.Add(10 * time.Minute)
	timer := &databases.DbTimer{
		Id:                     "test-timer-update-inplace",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "test-timer-update-inplace"),
		Namespace:              namespace,
		ExecuteAt:              originalExecuteAt,
		CallbackUrl:            "https://original.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Payload:                map[string]interface{}{"version": "1"},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 3},
	}

	// Insert the timer
	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Update timer with same execution time (in-place update)
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                "test-timer-update-inplace",
		ExecuteAt:              originalExecuteAt, // Same execution time
		CallbackUrl:            "https://updated.com/callback",
		CallbackTimeoutSeconds: 60,
		Payload:                map[string]interface{}{"version": "2", "updated": true},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 5, "strategy": "exponential"},
	}

	updateErr := store.UpdateTimer(ctx, shardId, shardVersion, namespace, updateRequest)
	require.Nil(t, updateErr)

	// Retrieve and verify updated timer
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timer.Id)
	require.Nil(t, getErr)
	require.NotNil(t, retrievedTimer)

	// Verify all updated fields
	assert.Equal(t, updateRequest.CallbackUrl, retrievedTimer.CallbackUrl)
	assert.Equal(t, updateRequest.CallbackTimeoutSeconds, retrievedTimer.CallbackTimeoutSeconds)
	assert.True(t, updateRequest.ExecuteAt.Equal(retrievedTimer.ExecuteAt))

	// Verify payload
	payloadMap, ok := retrievedTimer.Payload.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "2", payloadMap["version"])
	assert.Equal(t, true, payloadMap["updated"])

	// Verify retry policy
	retryMap, ok := retrievedTimer.RetryPolicy.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, float64(5), retryMap["maxAttempts"])
	assert.Equal(t, "exponential", retryMap["strategy"])

	// Verify fields that should remain unchanged
	assert.Equal(t, timer.Id, retrievedTimer.Id)
	assert.Equal(t, timer.Namespace, retrievedTimer.Namespace)
	assert.True(t, timer.CreatedAt.Equal(retrievedTimer.CreatedAt))

	// Verify attempts were reset
	assert.Equal(t, int32(0), retrievedTimer.Attempts)
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
	require.Nil(t, err)
	shardVersion := currentShardInfo.ShardVersion
	require.Nil(t, err)

	// Create original timer
	now := time.Now().UTC().Truncate(time.Millisecond)
	originalExecuteAt := now.Add(10 * time.Minute)
	timer := &databases.DbTimer{
		Id:                     "test-timer-update-exectime",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "test-timer-update-exectime"),
		Namespace:              namespace,
		ExecuteAt:              originalExecuteAt,
		CallbackUrl:            "https://original.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Payload:                map[string]interface{}{"data": "original"},
	}

	// Insert the timer
	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Update timer with different execution time
	newExecuteAt := now.Add(20 * time.Minute)
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                "test-timer-update-exectime",
		ExecuteAt:              newExecuteAt,
		CallbackUrl:            "https://updated.com/callback",
		CallbackTimeoutSeconds: 45,
		Payload:                map[string]interface{}{"data": "updated"},
	}

	updateErr := store.UpdateTimer(ctx, shardId, shardVersion, namespace, updateRequest)
	require.Nil(t, updateErr)

	// Retrieve and verify updated timer
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timer.Id)
	require.Nil(t, getErr)
	require.NotNil(t, retrievedTimer)

	// Verify all updated fields
	assert.Equal(t, updateRequest.CallbackUrl, retrievedTimer.CallbackUrl)
	assert.Equal(t, updateRequest.CallbackTimeoutSeconds, retrievedTimer.CallbackTimeoutSeconds)
	assert.True(t, updateRequest.ExecuteAt.Equal(retrievedTimer.ExecuteAt))

	// Verify payload
	payloadMap, ok := retrievedTimer.Payload.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "updated", payloadMap["data"])

	// Verify fields that should remain unchanged
	assert.Equal(t, timer.Id, retrievedTimer.Id)
	assert.Equal(t, timer.Namespace, retrievedTimer.Namespace)
	assert.True(t, timer.CreatedAt.Equal(retrievedTimer.CreatedAt))
}

func TestUpdateTimer_NotExists(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// First, create a shard record
	ownerAddr := "owner-1"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	require.Nil(t, err)
	shardVersion := currentShardInfo.ShardVersion
	require.Nil(t, err)
	require.Equal(t, int64(1), shardVersion)

	// Try to update a non-existent timer
	now := time.Now().UTC().Truncate(time.Millisecond)
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                "non-existent-timer",
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
	}

	updateErr := store.UpdateTimer(ctx, shardId, shardVersion, namespace, updateRequest)
	require.NotNil(t, updateErr)
	assert.True(t, databases.IsDbErrorNotExists(updateErr))
}

func TestUpdateTimer_ShardVersionMismatch(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Create shard record
	ownerAddr := "owner-1"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	require.Nil(t, err)
	shardVersion := currentShardInfo.ShardVersion
	require.Nil(t, err)

	// Create timer
	now := time.Now().UTC().Truncate(time.Millisecond)
	timer := &databases.DbTimer{
		Id:                     "test-timer-shard-version-update",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "test-timer-shard-version-update"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
	}

	// Insert the timer
	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Try to update with wrong shard version
	wrongShardVersion := shardVersion + 1
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                "test-timer-shard-version-update",
		ExecuteAt:              now.Add(15 * time.Minute),
		CallbackUrl:            "https://updated.com/callback",
		CallbackTimeoutSeconds: 60,
	}

	updateErr := store.UpdateTimer(ctx, shardId, wrongShardVersion, namespace, updateRequest)
	require.NotNil(t, updateErr)
	assert.True(t, updateErr.ShardConditionFail)

	// Verify timer wasn't updated
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timer.Id)
	require.Nil(t, getErr)
	require.NotNil(t, retrievedTimer)
	assert.Equal(t, timer.CallbackUrl, retrievedTimer.CallbackUrl) // Should be original
}

func TestUpdateTimer_NilToNonNil(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Create shard record
	ownerAddr := "owner-1"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	require.Nil(t, err)
	shardVersion := currentShardInfo.ShardVersion
	require.Nil(t, err)

	// Create timer with nil payload and retry policy
	now := time.Now().UTC().Truncate(time.Millisecond)
	timer := &databases.DbTimer{
		Id:                     "test-timer-nil-to-nonnil",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "test-timer-nil-to-nonnil"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Payload:                nil,
		RetryPolicy:            nil,
	}

	// Insert the timer
	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Update with non-nil payload and retry policy
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                "test-timer-nil-to-nonnil",
		ExecuteAt:              now.Add(15 * time.Minute),
		CallbackUrl:            "https://updated.com/callback",
		CallbackTimeoutSeconds: 45,
		Payload:                map[string]interface{}{"new": "data"},
		RetryPolicy:            map[string]interface{}{"attempts": 3},
	}

	updateErr := store.UpdateTimer(ctx, shardId, shardVersion, namespace, updateRequest)
	require.Nil(t, updateErr)

	// Verify update
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timer.Id)
	require.Nil(t, getErr)
	require.NotNil(t, retrievedTimer)

	assert.NotNil(t, retrievedTimer.Payload)
	assert.NotNil(t, retrievedTimer.RetryPolicy)

	payloadMap, ok := retrievedTimer.Payload.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "data", payloadMap["new"])
}

func TestUpdateTimer_NonNilToNil(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Create shard record
	ownerAddr := "owner-1"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	require.Nil(t, err)
	shardVersion := currentShardInfo.ShardVersion
	require.Nil(t, err)

	// Create timer with non-nil payload and retry policy
	now := time.Now().UTC().Truncate(time.Millisecond)
	timer := &databases.DbTimer{
		Id:                     "test-timer-nonnil-to-nil",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "test-timer-nonnil-to-nil"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Payload:                map[string]interface{}{"original": "data"},
		RetryPolicy:            map[string]interface{}{"attempts": 5},
	}

	// Insert the timer
	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Update with nil payload and retry policy
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                "test-timer-nonnil-to-nil",
		ExecuteAt:              now.Add(15 * time.Minute),
		CallbackUrl:            "https://updated.com/callback",
		CallbackTimeoutSeconds: 45,
		Payload:                nil,
		RetryPolicy:            nil,
	}

	updateErr := store.UpdateTimer(ctx, shardId, shardVersion, namespace, updateRequest)
	require.Nil(t, updateErr)

	// Verify update
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timer.Id)
	require.Nil(t, getErr)
	require.NotNil(t, retrievedTimer)

	assert.Nil(t, retrievedTimer.Payload)
	assert.Nil(t, retrievedTimer.RetryPolicy)
}

func TestUpdateTimer_IdenticalValues(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Create shard record
	ownerAddr := "owner-1"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	require.Nil(t, err)
	shardVersion := currentShardInfo.ShardVersion

	// Create original timer
	now := time.Now().UTC().Truncate(time.Millisecond) // Truncate to millisecond precision for MySQL
	executeAt := now.Add(10 * time.Minute)
	timer := &databases.DbTimer{
		Id:                     "test-timer-identical",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "test-timer-identical"),
		Namespace:              namespace,
		ExecuteAt:              executeAt,
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Payload:                map[string]interface{}{"version": "1", "data": "test"},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 3, "strategy": "linear"},
	}

	// Insert the timer
	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// First update with some different values
	updateRequest1 := &databases.UpdateDbTimerRequest{
		TimerId:                "test-timer-identical",
		ExecuteAt:              executeAt,
		CallbackUrl:            "https://updated.com/callback",
		CallbackTimeoutSeconds: 60,
		Payload:                map[string]interface{}{"version": "2", "updated": true},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 5, "strategy": "exponential"},
	}

	updateErr1 := store.UpdateTimer(ctx, shardId, shardVersion, namespace, updateRequest1)
	require.Nil(t, updateErr1)

	// Now update with EXACTLY the same values as the previous update (should succeed with clientFoundRows=true)
	// This tests the behavior where MySQL would normally return 0 rows affected (no changes),
	// but with clientFoundRows=true it returns 1 row affected (row matched the WHERE clause)
	updateRequest2 := &databases.UpdateDbTimerRequest{
		TimerId:                "test-timer-identical",
		ExecuteAt:              executeAt, // Same values as updateRequest1
		CallbackUrl:            "https://updated.com/callback",
		CallbackTimeoutSeconds: 60,
		Payload:                map[string]interface{}{"version": "2", "updated": true},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 5, "strategy": "exponential"},
	}

	updateErr2 := store.UpdateTimer(ctx, shardId, shardVersion, namespace, updateRequest2)
	require.Nil(t, updateErr2, "Update with identical values should succeed when clientFoundRows=true")

	// Verify the timer values remain the same
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timer.Id)
	require.Nil(t, getErr)
	require.NotNil(t, retrievedTimer)

	// Verify all fields match the last update
	assert.Equal(t, updateRequest2.CallbackUrl, retrievedTimer.CallbackUrl)
	assert.Equal(t, updateRequest2.CallbackTimeoutSeconds, retrievedTimer.CallbackTimeoutSeconds)
	assert.Equal(t, updateRequest2.ExecuteAt, retrievedTimer.ExecuteAt)
	assert.Equal(t, int32(0), retrievedTimer.Attempts) // Should be reset to 0 after update

	// Verify JSON payload and retry policy
	// Note: JSON unmarshaling converts numbers to float64 by default
	expectedPayload := map[string]interface{}{"version": "2", "updated": true}
	expectedRetryPolicy := map[string]interface{}{"maxAttempts": float64(5), "strategy": "exponential"}
	assert.Equal(t, expectedPayload, retrievedTimer.Payload)
	assert.Equal(t, expectedRetryPolicy, retrievedTimer.RetryPolicy)
}
