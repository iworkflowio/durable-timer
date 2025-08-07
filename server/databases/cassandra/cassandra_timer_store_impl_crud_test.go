package cassandra

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

	// Create a timer to retrieve - use UTC times to avoid timezone issues
	now := time.Now().UTC().Truncate(time.Millisecond).Truncate(time.Millisecond)
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
	assert.True(t, timer.ExecuteAt.Equal(retrievedTimer.ExecuteAt), "ExecuteAt times should be equal. Expected: %v, Got: %v", timer.ExecuteAt, retrievedTimer.ExecuteAt)
	assert.True(t, timer.CreatedAt.Equal(retrievedTimer.CreatedAt), "CreatedAt times should be equal. Expected: %v, Got: %v", timer.CreatedAt, retrievedTimer.CreatedAt)

	// JSON deserialization converts int to float64, so we need to compare expected values
	expectedPayload := map[string]interface{}{"key": "value", "number": float64(42)}
	expectedRetryPolicy := map[string]interface{}{"maxAttempts": float64(3), "backoff": "exponential"}
	assert.Equal(t, expectedPayload, retrievedTimer.Payload)
	assert.Equal(t, expectedRetryPolicy, retrievedTimer.RetryPolicy)

	// Verify Attempts field defaults to 0
	assert.Equal(t, int32(0), retrievedTimer.Attempts)
}

func TestGetTimer_NotFound(t *testing.T) {
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

	// Try to get a non-existent timer
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, "non-existent-timer")
	assert.Nil(t, retrievedTimer)
	assert.NotNil(t, getErr)
	assert.True(t, databases.IsDbErrorNotExists(getErr))
	assert.Contains(t, getErr.CustomMessage, "timer not found")
}

func TestGetTimer_NilPayloadAndRetryPolicy(t *testing.T) {
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

	// Create a timer with nil payload and retry policy
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

	// Verify nil fields are handled correctly
	assert.Equal(t, timer.Id, retrievedTimer.Id)
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
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
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
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
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
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
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
	assert.Equal(t, shardVersion, deleteErr.ConflictShardVersion)

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

	// First, create a shard record
	ownerAddr := "owner-1"
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
	require.Nil(t, err)
	require.Equal(t, int64(1), shardVersion)

	// Create a timer to update
	now := time.Now().UTC().Truncate(time.Millisecond)
	executeAt := now.Add(10 * time.Minute)
	timer := &databases.DbTimer{
		Id:                     "test-timer-update-inplace",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "test-timer-update-inplace"),
		Namespace:              namespace,
		ExecuteAt:              executeAt,
		CallbackUrl:            "https://example.com/callback-original",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Payload:                map[string]interface{}{"original": true},
		RetryPolicy:            map[string]interface{}{"maxAttempts": float64(3)},
	}

	// Insert the timer
	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Verify timer was created by trying to get it
	createdTimer, getErr := store.GetTimer(ctx, shardId, namespace, timer.Id)
	require.Nil(t, getErr, "Timer should exist after creation")
	require.NotNil(t, createdTimer)

	// Update timer (same execution time - in-place update)
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                "test-timer-update-inplace",
		ExecuteAt:              executeAt, // Same execution time
		CallbackUrl:            "https://example.com/callback-updated",
		CallbackTimeoutSeconds: 60,
		Payload:                map[string]interface{}{"updated": true, "version": float64(2)},
		RetryPolicy:            map[string]interface{}{"maxAttempts": float64(5), "backoff": "linear"},
	}

	updateErr := store.UpdateTimer(ctx, shardId, shardVersion, namespace, updateRequest)
	require.Nil(t, updateErr)

	// Verify timer was updated
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timer.Id)
	require.Nil(t, getErr)
	require.NotNil(t, retrievedTimer)

	// Verify updated fields
	assert.Equal(t, updateRequest.CallbackUrl, retrievedTimer.CallbackUrl)
	assert.Equal(t, updateRequest.CallbackTimeoutSeconds, retrievedTimer.CallbackTimeoutSeconds)

	// JSON deserialization converts int to float64, so we need to compare expected values
	expectedPayload := map[string]interface{}{"updated": true, "version": float64(2)}
	expectedRetryPolicy := map[string]interface{}{"maxAttempts": float64(5), "backoff": "linear"}
	assert.Equal(t, expectedPayload, retrievedTimer.Payload)
	assert.Equal(t, expectedRetryPolicy, retrievedTimer.RetryPolicy)
	assert.True(t, updateRequest.ExecuteAt.Equal(retrievedTimer.ExecuteAt))

	// Verify fields that should remain unchanged
	assert.Equal(t, timer.Id, retrievedTimer.Id)
	assert.Equal(t, timer.TimerUuid, retrievedTimer.TimerUuid)
	assert.Equal(t, timer.Namespace, retrievedTimer.Namespace)
	assert.True(t, timer.CreatedAt.Equal(retrievedTimer.CreatedAt))
}

func TestUpdateTimer_ExecutionTimeChange(t *testing.T) {
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

	// Create a timer to update
	now := time.Now().UTC().Truncate(time.Millisecond)
	originalExecuteAt := now.Add(10 * time.Minute)
	timer := &databases.DbTimer{
		Id:                     "test-timer-update-exectime",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "test-timer-update-exectime"),
		Namespace:              namespace,
		ExecuteAt:              originalExecuteAt,
		CallbackUrl:            "https://example.com/callback-original",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Payload:                map[string]interface{}{"version": 1},
	}

	// Insert the timer
	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Update timer with different execution time (delete + insert)
	newExecuteAt := now.Add(20 * time.Minute)
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                "test-timer-update-exectime",
		ExecuteAt:              newExecuteAt, // Different execution time
		CallbackUrl:            "https://example.com/callback-updated",
		CallbackTimeoutSeconds: 45,
		Payload:                map[string]interface{}{"version": float64(2), "updated": true},
		RetryPolicy:            map[string]interface{}{"maxAttempts": float64(3)},
	}

	updateErr := store.UpdateTimer(ctx, shardId, shardVersion, namespace, updateRequest)
	require.Nil(t, updateErr)

	// Verify timer was updated
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timer.Id)
	require.Nil(t, getErr)
	require.NotNil(t, retrievedTimer)

	// Verify all updated fields
	assert.Equal(t, updateRequest.CallbackUrl, retrievedTimer.CallbackUrl)
	assert.Equal(t, updateRequest.CallbackTimeoutSeconds, retrievedTimer.CallbackTimeoutSeconds)
	assert.Equal(t, updateRequest.Payload, retrievedTimer.Payload)
	assert.Equal(t, updateRequest.RetryPolicy, retrievedTimer.RetryPolicy)
	assert.True(t, updateRequest.ExecuteAt.Equal(retrievedTimer.ExecuteAt))

	// Verify fields that should remain unchanged
	assert.Equal(t, timer.Id, retrievedTimer.Id)
	assert.Equal(t, timer.TimerUuid, retrievedTimer.TimerUuid)
	assert.Equal(t, timer.Namespace, retrievedTimer.Namespace)
	assert.True(t, timer.CreatedAt.Equal(retrievedTimer.CreatedAt))

	// Verify no timer exists at the old execution time
	countQuery := `SELECT COUNT(*) FROM timers WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? ALLOW FILTERING`
	var count int
	scanErr := store.session.Query(countQuery, shardId, databases.RowTypeTimer, originalExecuteAt).Scan(&count)
	require.NoError(t, scanErr)
	assert.Equal(t, 0, count, "No timer should exist at the old execution time")
}

func TestUpdateTimer_NotExists(t *testing.T) {
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

	// First, create a shard record
	ownerAddr := "owner-1"
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
	require.Nil(t, err)
	require.Equal(t, int64(1), shardVersion)

	// Create a timer to update
	now := time.Now().UTC().Truncate(time.Millisecond)
	timer := &databases.DbTimer{
		Id:                     "test-timer-shard-version-update",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "test-timer-shard-version-update"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback-original",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
	}

	// Insert the timer
	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Try to update with wrong shard version
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                "test-timer-shard-version-update",
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback-updated",
		CallbackTimeoutSeconds: 60,
	}

	wrongShardVersion := shardVersion + 1
	updateErr := store.UpdateTimer(ctx, shardId, wrongShardVersion, namespace, updateRequest)
	require.NotNil(t, updateErr)
	assert.True(t, updateErr.ShardConditionFail)
	assert.Equal(t, shardVersion, updateErr.ConflictShardVersion)

	// Verify timer was not updated
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timer.Id)
	require.Nil(t, getErr)
	require.NotNil(t, retrievedTimer)
	assert.Equal(t, timer.CallbackUrl, retrievedTimer.CallbackUrl) // Should be unchanged
	assert.Equal(t, timer.CallbackTimeoutSeconds, retrievedTimer.CallbackTimeoutSeconds)
}

func TestUpdateTimer_NilToNonNilFields(t *testing.T) {
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

	// Create a timer with nil payload and retry policy
	now := time.Now().UTC().Truncate(time.Millisecond)
	timer := &databases.DbTimer{
		Id:                     "test-timer-nil-to-nonnil",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "test-timer-nil-to-nonnil"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback-original",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Payload:                nil,
		RetryPolicy:            nil,
	}

	// Insert the timer
	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Update timer to add payload and retry policy
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                "test-timer-nil-to-nonnil",
		ExecuteAt:              timer.ExecuteAt,
		CallbackUrl:            "https://example.com/callback-updated",
		CallbackTimeoutSeconds: 45,
		Payload:                map[string]interface{}{"added": true, "data": "new"},
		RetryPolicy:            map[string]interface{}{"maxAttempts": float64(5)},
	}

	updateErr := store.UpdateTimer(ctx, shardId, shardVersion, namespace, updateRequest)
	require.Nil(t, updateErr)

	// Verify timer was updated
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timer.Id)
	require.Nil(t, getErr)
	require.NotNil(t, retrievedTimer)

	// Verify fields were updated from nil to non-nil
	assert.Equal(t, updateRequest.Payload, retrievedTimer.Payload)
	assert.Equal(t, updateRequest.RetryPolicy, retrievedTimer.RetryPolicy)
	assert.Equal(t, updateRequest.CallbackUrl, retrievedTimer.CallbackUrl)
	assert.Equal(t, updateRequest.CallbackTimeoutSeconds, retrievedTimer.CallbackTimeoutSeconds)
}

func TestUpdateTimer_NonNilToNilFields(t *testing.T) {
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

	// Create a timer with non-nil payload and retry policy
	now := time.Now().UTC().Truncate(time.Millisecond)
	timer := &databases.DbTimer{
		Id:                     "test-timer-nonnil-to-nil",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "test-timer-nonnil-to-nil"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback-original",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Payload:                map[string]interface{}{"original": true, "data": "existing"},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 3, "backoff": "exponential"},
	}

	// Insert the timer
	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Update timer to set payload and retry policy to nil
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                "test-timer-nonnil-to-nil",
		ExecuteAt:              timer.ExecuteAt,
		CallbackUrl:            "https://example.com/callback-updated",
		CallbackTimeoutSeconds: 45,
		Payload:                nil,
		RetryPolicy:            nil,
	}

	updateErr := store.UpdateTimer(ctx, shardId, shardVersion, namespace, updateRequest)
	require.Nil(t, updateErr)

	// Verify timer was updated
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timer.Id)
	require.Nil(t, getErr)
	require.NotNil(t, retrievedTimer)

	// Verify fields were updated from non-nil to nil
	assert.Nil(t, retrievedTimer.Payload)
	assert.Nil(t, retrievedTimer.RetryPolicy)
	assert.Equal(t, updateRequest.CallbackUrl, retrievedTimer.CallbackUrl)
	assert.Equal(t, updateRequest.CallbackTimeoutSeconds, retrievedTimer.CallbackTimeoutSeconds)
}

func TestCreateTimerAttemptsPreservation(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Create shard record
	ownerAddr := "owner-1"
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
	require.Nil(t, err)

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

	// Verify Attempts are preserved
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
}
