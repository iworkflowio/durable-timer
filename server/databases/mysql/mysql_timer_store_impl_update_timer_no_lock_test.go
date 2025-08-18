package mysql

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

	// First, create a shard record and timer
	ownerAddr := "owner-1"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	require.Nil(t, err)
	shardVersion := currentShardInfo.ShardVersion

	// Create a timer to update
	now := time.Now().UTC().Truncate(time.Millisecond)
	timer := &databases.DbTimer{
		Id:                     "test-timer-update-no-lock",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "test-timer-update-no-lock"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback-original",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Payload:                map[string]interface{}{"key": "original", "number": 42},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 3, "backoff": "exponential"},
		Attempts:               5, // Set some attempts
	}

	// Insert the timer
	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Prepare update request
	newExecuteAt := now.Add(20 * time.Minute)
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                "test-timer-update-no-lock",
		ExecuteAt:              newExecuteAt,
		CallbackUrl:            "https://example.com/callback-updated",
		CallbackTimeoutSeconds: 60,
		Payload:                map[string]interface{}{"key": "updated", "number": 84},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 5, "backoff": "linear"},
	}

	// Update timer without locking
	updateErr := store.UpdateTimerNoLock(ctx, shardId, namespace, updateRequest)
	require.Nil(t, updateErr)

	// Verify timer was updated
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timer.Id)
	require.Nil(t, getErr)
	require.NotNil(t, retrievedTimer)

	// Verify updated fields
	assert.Equal(t, updateRequest.TimerId, retrievedTimer.Id)
	assert.True(t, newExecuteAt.Equal(retrievedTimer.ExecuteAt))
	assert.Equal(t, updateRequest.CallbackUrl, retrievedTimer.CallbackUrl)
	assert.Equal(t, updateRequest.CallbackTimeoutSeconds, retrievedTimer.CallbackTimeoutSeconds)
	assert.Equal(t, int32(0), retrievedTimer.Attempts) // Should be reset to 0

	// Verify updated payload
	assert.NotNil(t, retrievedTimer.Payload)
	payloadMap, ok := retrievedTimer.Payload.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "updated", payloadMap["key"])
	assert.Equal(t, float64(84), payloadMap["number"])

	// Verify updated retry policy
	assert.NotNil(t, retrievedTimer.RetryPolicy)
	retryMap, ok := retrievedTimer.RetryPolicy.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, float64(5), retryMap["maxAttempts"])
	assert.Equal(t, "linear", retryMap["backoff"])

	// Verify UUID was updated (should be based on namespace + timerId)
	expectedUuid := databases.GenerateTimerUUID(namespace, updateRequest.TimerId)
	assert.Equal(t, expectedUuid, retrievedTimer.TimerUuid)

	// Verify preserved fields
	assert.Equal(t, timer.Namespace, retrievedTimer.Namespace)
	assert.True(t, timer.CreatedAt.Equal(retrievedTimer.CreatedAt)) // Created time should be preserved
}

func TestUpdateTimerNoLock_NotExists(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Prepare update request for non-existent timer
	now := time.Now().UTC().Truncate(time.Millisecond)
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                "non-existent-timer",
		ExecuteAt:              now.Add(20 * time.Minute),
		CallbackUrl:            "https://example.com/callback-updated",
		CallbackTimeoutSeconds: 60,
		Payload:                map[string]interface{}{"key": "updated"},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 5},
	}

	// Try to update non-existent timer
	updateErr := store.UpdateTimerNoLock(ctx, shardId, namespace, updateRequest)
	require.NotNil(t, updateErr)
	assert.True(t, databases.IsDbErrorNotExists(updateErr))
}

func TestUpdateTimerNoLock_NilFields(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// First, create a shard record and timer
	ownerAddr := "owner-1"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	require.Nil(t, err)
	shardVersion := currentShardInfo.ShardVersion

	// Create a timer with payload and retry policy
	now := time.Now().UTC().Truncate(time.Millisecond)
	timer := &databases.DbTimer{
		Id:                     "test-timer-update-nil",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "test-timer-update-nil"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback-original",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Payload:                map[string]interface{}{"key": "original"},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 3},
	}

	// Insert the timer
	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Prepare update request with nil payload and retry policy
	newExecuteAt := now.Add(20 * time.Minute)
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                "test-timer-update-nil",
		ExecuteAt:              newExecuteAt,
		CallbackUrl:            "https://example.com/callback-updated",
		CallbackTimeoutSeconds: 60,
		Payload:                nil,
		RetryPolicy:            nil,
	}

	// Update timer
	updateErr := store.UpdateTimerNoLock(ctx, shardId, namespace, updateRequest)
	require.Nil(t, updateErr)

	// Verify timer was updated
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timer.Id)
	require.Nil(t, getErr)
	require.NotNil(t, retrievedTimer)

	// Verify nil fields
	assert.Nil(t, retrievedTimer.Payload)
	assert.Nil(t, retrievedTimer.RetryPolicy)

	// Verify other updated fields
	assert.True(t, newExecuteAt.Equal(retrievedTimer.ExecuteAt))
	assert.Equal(t, updateRequest.CallbackUrl, retrievedTimer.CallbackUrl)
	assert.Equal(t, updateRequest.CallbackTimeoutSeconds, retrievedTimer.CallbackTimeoutSeconds)
}

func TestUpdateTimerNoLock_NoShardVersionCheck(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// First, create a shard record and timer
	ownerAddr := "owner-1"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	require.Nil(t, err)
	shardVersion := currentShardInfo.ShardVersion

	// Create a timer
	now := time.Now().UTC().Truncate(time.Millisecond)
	timer := &databases.DbTimer{
		Id:                     "test-timer-no-lock",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "test-timer-no-lock"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback-original",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
	}

	// Insert the timer
	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Update shard version to simulate concurrent modification
	// (in real usage, another process might have changed the shard version)
	// We increment shard version by claiming ownership again
	_, _, claimErr := store.ClaimShardOwnership(ctx, shardId, "owner-2")
	require.Nil(t, claimErr)

	// Now try to update timer using UpdateTimerNoLock
	// This should succeed because UpdateTimerNoLock doesn't check shard version
	newExecuteAt := now.Add(20 * time.Minute)
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                "test-timer-no-lock",
		ExecuteAt:              newExecuteAt,
		CallbackUrl:            "https://example.com/callback-updated",
		CallbackTimeoutSeconds: 60,
	}

	// This should succeed because no lock is involved
	updateErr := store.UpdateTimerNoLock(ctx, shardId, namespace, updateRequest)
	require.Nil(t, updateErr)

	// Verify timer was updated
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timer.Id)
	require.Nil(t, getErr)
	require.NotNil(t, retrievedTimer)
	assert.True(t, newExecuteAt.Equal(retrievedTimer.ExecuteAt))
	assert.Equal(t, updateRequest.CallbackUrl, retrievedTimer.CallbackUrl)

	// For comparison, try using the locked version with the old shard version
	// This should fail due to shard version mismatch
	updateRequest2 := &databases.UpdateDbTimerRequest{
		TimerId:                "test-timer-no-lock",
		ExecuteAt:              now.Add(30 * time.Minute),
		CallbackUrl:            "https://example.com/callback-locked",
		CallbackTimeoutSeconds: 90,
	}

	lockedUpdateErr := store.UpdateTimer(ctx, shardId, shardVersion, namespace, updateRequest2)
	require.NotNil(t, lockedUpdateErr)
	assert.True(t, lockedUpdateErr.ShardConditionFail)
}

func TestUpdateTimerNoLock_ContextCancellation(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	shardId := 1
	namespace := "test_namespace"

	// Create a cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                "test-timer-cancelled",
		ExecuteAt:              time.Now().UTC().Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
	}

	// Try to update with cancelled context
	updateErr := store.UpdateTimerNoLock(ctx, shardId, namespace, updateRequest)
	require.NotNil(t, updateErr)
	// Should get a context cancellation error
	assert.Contains(t, updateErr.Error(), "context canceled")
}
