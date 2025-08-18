package mongodb

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
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	shardVersion := currentShardInfo.ShardVersion
	require.Nil(t, err)
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
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	shardVersion := currentShardInfo.ShardVersion
	require.Nil(t, err)

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
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	shardVersion := currentShardInfo.ShardVersion
	require.Nil(t, err)

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
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	shardVersion := currentShardInfo.ShardVersion
	require.Nil(t, err)

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
		TimerId:                timer.Id,
		ExecuteAt:              timer.ExecuteAt, // Same ExecuteAt - should do in-place update
		CallbackUrl:            "https://example.com/callback-updated",
		CallbackTimeoutSeconds: 60,
		Payload:                map[string]interface{}{"updated": "payload", "number": 123},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 5, "backoff": "linear"},
	}

	updateErr := store.UpdateTimer(ctx, shardId, shardVersion, namespace, updateRequest)
	require.Nil(t, updateErr)

	// Verify timer was updated
	updatedTimer, getErr2 := store.GetTimer(ctx, shardId, namespace, timer.Id)
	require.Nil(t, getErr2)
	require.NotNil(t, updatedTimer)

	// Verify updated fields
	assert.Equal(t, "https://example.com/callback-updated", updatedTimer.CallbackUrl)
	assert.Equal(t, int32(60), updatedTimer.CallbackTimeoutSeconds)
	assert.True(t, updateRequest.ExecuteAt.Equal(updatedTimer.ExecuteAt))

	// JSON deserialization converts int to float64
	expectedPayload := map[string]interface{}{"updated": "payload", "number": float64(123)}
	expectedRetryPolicy := map[string]interface{}{"maxAttempts": float64(5), "backoff": "linear"}
	assert.Equal(t, expectedPayload, updatedTimer.Payload)
	assert.Equal(t, expectedRetryPolicy, updatedTimer.RetryPolicy)

	// Verify that attempts and other fields are preserved
	assert.Equal(t, int32(2), updatedTimer.Attempts) // Attempts should be preserved
	assert.Equal(t, timer.Id, updatedTimer.Id)
	assert.Equal(t, timer.Namespace, updatedTimer.Namespace)
	assert.True(t, timer.CreatedAt.Equal(updatedTimer.CreatedAt)) // CreatedAt should be preserved
}

func TestUpdateTimer_WithNewExecuteAt(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Create shard record
	ownerAddr := "owner-1"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	shardVersion := currentShardInfo.ShardVersion
	require.Nil(t, err)

	// Create a timer to update
	now := time.Now().UTC().Truncate(time.Millisecond)
	timer := &databases.DbTimer{
		Id:                     "test-timer-update-time",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "test-timer-update-time"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback-original",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Attempts:               7,
	}

	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Update with different ExecuteAt
	newExecuteAt := now.Add(20 * time.Minute)
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                timer.Id,
		ExecuteAt:              newExecuteAt, // Different ExecuteAt
		CallbackUrl:            "https://example.com/callback-new-time",
		CallbackTimeoutSeconds: 45,
	}

	updateErr := store.UpdateTimer(ctx, shardId, shardVersion, namespace, updateRequest)
	require.Nil(t, updateErr)

	// Verify timer was updated
	updatedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timer.Id)
	require.Nil(t, getErr)
	require.NotNil(t, updatedTimer)

	// Verify updated fields
	assert.Equal(t, "https://example.com/callback-new-time", updatedTimer.CallbackUrl)
	assert.Equal(t, int32(45), updatedTimer.CallbackTimeoutSeconds)
	assert.True(t, newExecuteAt.Equal(updatedTimer.ExecuteAt))

	// Verify that attempts are preserved
	assert.Equal(t, int32(7), updatedTimer.Attempts)
}

func TestUpdateTimer_NilPayloadAndRetryPolicy(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Create shard record
	ownerAddr := "owner-1"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	shardVersion := currentShardInfo.ShardVersion
	require.Nil(t, err)

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
		Payload:                map[string]interface{}{"original": "payload"},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 3},
	}

	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Update with nil payload and retry policy
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                timer.Id,
		ExecuteAt:              timer.ExecuteAt,
		CallbackUrl:            "https://example.com/callback-nil-fields",
		CallbackTimeoutSeconds: 45,
		Payload:                nil,
		RetryPolicy:            nil,
	}

	updateErr := store.UpdateTimer(ctx, shardId, shardVersion, namespace, updateRequest)
	require.Nil(t, updateErr)

	// Verify timer was updated
	updatedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timer.Id)
	require.Nil(t, getErr)
	require.NotNil(t, updatedTimer)

	// Verify fields were updated from non-nil to nil
	assert.Nil(t, updatedTimer.Payload)
	assert.Nil(t, updatedTimer.RetryPolicy)
	assert.Equal(t, updateRequest.CallbackUrl, updatedTimer.CallbackUrl)
	assert.Equal(t, updateRequest.CallbackTimeoutSeconds, updatedTimer.CallbackTimeoutSeconds)
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
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	shardVersion := currentShardInfo.ShardVersion
	require.Nil(t, err)

	// Try to update a non-existent timer
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                "non-existent-timer",
		ExecuteAt:              time.Now().Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
	}

	updateErr := store.UpdateTimer(ctx, shardId, shardVersion, namespace, updateRequest)
	assert.NotNil(t, updateErr)
	assert.True(t, databases.IsDbErrorNotExists(updateErr))
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
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	shardVersion := currentShardInfo.ShardVersion
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

	// Verify Attempts are preserved in CreateTimer
	retrievedTimer1, getErr1 := store.GetTimer(ctx, shardId, namespace, timer1.Id)
	require.Nil(t, getErr1)
	assert.Equal(t, int32(0), retrievedTimer1.Attempts)

	retrievedTimer2, getErr2 := store.GetTimer(ctx, shardId, namespace, timer2.Id)
	require.Nil(t, getErr2)
	assert.Equal(t, int32(7), retrievedTimer2.Attempts)
}

func TestDeleteTimersUpToTimestampWithBatchInsert_AttemptsPreservation(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Create shard record
	ownerAddr := "owner-1"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	shardVersion := currentShardInfo.ShardVersion
	require.Nil(t, err)

	now := time.Now().UTC().Truncate(time.Millisecond)

	// Create timer to delete
	timerToDelete := &databases.DbTimer{
		Id:                     "timer-to-delete",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-to-delete"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/callback-delete",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Attempts:               3,
	}

	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timerToDelete)
	require.Nil(t, createErr)

	// Create timers to insert with different attempts values
	timerToInsert1 := &databases.DbTimer{
		Id:                     "timer-insert-1",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-insert-1"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback-insert-1",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Attempts:               5, // Non-zero attempts
	}

	timerToInsert2 := &databases.DbTimer{
		Id:                     "timer-insert-2",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-insert-2"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(15 * time.Minute),
		CallbackUrl:            "https://example.com/callback-insert-2",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Attempts:               0, // Zero attempts
	}

	// Define delete range and batch insert
	deleteRequest := &databases.RangeDeleteTimersRequest{
		StartTimestamp: now.Add(2 * time.Minute),
		StartTimeUuid:  databases.ZeroUUID,
		EndTimestamp:   now.Add(8 * time.Minute),
		EndTimeUuid:    databases.GenerateTimerUUID("max", "max"),
	}

	timersToInsert := []*databases.DbTimer{timerToInsert1, timerToInsert2}

	// Execute delete and batch insert
	response, deleteErr := store.RangeDeleteWithBatchInsertTxn(ctx, shardId, shardVersion, deleteRequest, timersToInsert)
	require.Nil(t, deleteErr)
	require.NotNil(t, response)
	assert.Equal(t, 1, response.DeletedCount) // Should delete timerToDelete

	// Verify old timer was deleted
	deletedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timerToDelete.Id)
	assert.Nil(t, deletedTimer)
	assert.NotNil(t, getErr)
	assert.True(t, databases.IsDbErrorNotExists(getErr))

	// Verify new timers were inserted with correct attempts values
	insertedTimer1, getErr1 := store.GetTimer(ctx, shardId, namespace, timerToInsert1.Id)
	require.Nil(t, getErr1)
	require.NotNil(t, insertedTimer1)
	assert.Equal(t, int32(5), insertedTimer1.Attempts) // Should preserve non-zero attempts

	insertedTimer2, getErr2 := store.GetTimer(ctx, shardId, namespace, timerToInsert2.Id)
	require.Nil(t, getErr2)
	require.NotNil(t, insertedTimer2)
	assert.Equal(t, int32(0), insertedTimer2.Attempts) // Should preserve zero attempts

	// Verify other fields
	assert.Equal(t, timerToInsert1.Id, insertedTimer1.Id)
	assert.Equal(t, timerToInsert1.CallbackUrl, insertedTimer1.CallbackUrl)
	assert.Equal(t, timerToInsert2.Id, insertedTimer2.Id)
	assert.Equal(t, timerToInsert2.CallbackUrl, insertedTimer2.CallbackUrl)
}

func TestGetTimersUpToTimestamp_AttemptsPreservation(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Create shard record
	ownerAddr := "owner-1"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	shardVersion := currentShardInfo.ShardVersion
	require.Nil(t, err)

	now := time.Now().UTC().Truncate(time.Millisecond)

	// Create timers with different attempts values
	timer1 := &databases.DbTimer{
		Id:                     "timer-attempts-0",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-attempts-0"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(1 * time.Minute),
		CallbackUrl:            "https://example.com/callback1",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Attempts:               0, // Zero attempts
	}

	timer2 := &databases.DbTimer{
		Id:                     "timer-attempts-5",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-attempts-5"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(2 * time.Minute),
		CallbackUrl:            "https://example.com/callback2",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Attempts:               5, // Non-zero attempts
	}

	timer3 := &databases.DbTimer{
		Id:                     "timer-attempts-10",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-attempts-10"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(3 * time.Minute),
		CallbackUrl:            "https://example.com/callback3",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Attempts:               10, // High attempts
	}

	// Create all timers
	createErr1 := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer1)
	require.Nil(t, createErr1)
	createErr2 := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer2)
	require.Nil(t, createErr2)
	createErr3 := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer3)
	require.Nil(t, createErr3)

	// Retrieve timers using GetTimersUpToTimestamp
	request := &databases.RangeGetTimersRequest{
		StartTimestamp: time.Unix(0, 0),
		StartTimeUuid:  databases.ZeroUUID,
		EndTimestamp:   now.Add(5 * time.Minute),
		EndTimeUuid:    databases.MaxUUID,
		Limit:          10,
	}

	response, getErr := store.RangeGetTimers(ctx, shardId, request)
	require.Nil(t, getErr)
	require.NotNil(t, response)
	require.Len(t, response.Timers, 3)

	// Verify timers are returned in correct order (by execute time)
	assert.Equal(t, "timer-attempts-0", response.Timers[0].Id)
	assert.Equal(t, "timer-attempts-5", response.Timers[1].Id)
	assert.Equal(t, "timer-attempts-10", response.Timers[2].Id)

	// Verify attempts are preserved
	assert.Equal(t, int32(0), response.Timers[0].Attempts)
	assert.Equal(t, int32(5), response.Timers[1].Attempts)
	assert.Equal(t, int32(10), response.Timers[2].Attempts)

	// Verify other fields
	assert.Equal(t, timer1.CallbackUrl, response.Timers[0].CallbackUrl)
	assert.Equal(t, timer2.CallbackUrl, response.Timers[1].CallbackUrl)
	assert.Equal(t, timer3.CallbackUrl, response.Timers[2].CallbackUrl)
}

func TestUpdateTimerNoLock_Basic(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Create shard record (even though NoLock doesn't use it, we need a timer to update)
	ownerAddr := "owner-1"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	shardVersion := currentShardInfo.ShardVersion
	require.Nil(t, err)

	// Create a timer to update
	now := time.Now().UTC().Truncate(time.Millisecond)
	timer := &databases.DbTimer{
		Id:                     "test-timer-update-nolock",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "test-timer-update-nolock"),
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

	// Update the timer without shard locking
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                timer.Id,
		ExecuteAt:              now.Add(15 * time.Minute), // Different ExecuteAt
		CallbackUrl:            "https://example.com/callback-updated",
		CallbackTimeoutSeconds: 60,
		Payload:                map[string]interface{}{"updated": "payload", "number": 123},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 5, "backoff": "linear"},
	}

	updateErr := store.UpdateTimerNoLock(ctx, shardId, namespace, updateRequest)
	require.Nil(t, updateErr)

	// Verify timer was updated
	updatedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timer.Id)
	require.Nil(t, getErr)
	require.NotNil(t, updatedTimer)

	// Verify updated fields
	assert.Equal(t, "https://example.com/callback-updated", updatedTimer.CallbackUrl)
	assert.Equal(t, int32(60), updatedTimer.CallbackTimeoutSeconds)
	assert.True(t, updateRequest.ExecuteAt.Equal(updatedTimer.ExecuteAt))

	// JSON deserialization converts int to float64
	expectedPayload := map[string]interface{}{"updated": "payload", "number": float64(123)}
	expectedRetryPolicy := map[string]interface{}{"maxAttempts": float64(5), "backoff": "linear"}
	assert.Equal(t, expectedPayload, updatedTimer.Payload)
	assert.Equal(t, expectedRetryPolicy, updatedTimer.RetryPolicy)

	// Verify preserved fields
	assert.Equal(t, timer.Id, updatedTimer.Id)
	assert.Equal(t, timer.Namespace, updatedTimer.Namespace)
	assert.True(t, timer.CreatedAt.Equal(updatedTimer.CreatedAt)) // CreatedAt should be preserved
}

func TestUpdateTimerNoLock_NotFound(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Try to update a non-existent timer
	now := time.Now().UTC().Truncate(time.Millisecond)
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                "non-existent-timer",
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		Payload:                map[string]interface{}{"test": "payload"},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 3},
	}

	updateErr := store.UpdateTimerNoLock(ctx, shardId, namespace, updateRequest)
	require.NotNil(t, updateErr)
	assert.True(t, databases.IsDbErrorNotExists(updateErr))
}

func TestUpdateTimerNoLock_NilPayloadAndRetryPolicy(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Create shard record
	ownerAddr := "owner-1"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	shardVersion := currentShardInfo.ShardVersion
	require.Nil(t, err)

	// Create a timer with payload and retry policy
	now := time.Now().UTC().Truncate(time.Millisecond)
	timer := &databases.DbTimer{
		Id:                     "test-timer-nil-update",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "test-timer-nil-update"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback-original",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Attempts:               1,
		Payload:                map[string]interface{}{"original": "payload"},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 3},
	}

	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Update with nil payload and retry policy
	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                timer.Id,
		ExecuteAt:              timer.ExecuteAt,
		CallbackUrl:            "https://example.com/callback-updated",
		CallbackTimeoutSeconds: 45,
		Payload:                nil, // Set to nil
		RetryPolicy:            nil, // Set to nil
	}

	updateErr := store.UpdateTimerNoLock(ctx, shardId, namespace, updateRequest)
	require.Nil(t, updateErr)

	// Verify timer was updated with nil payload and retry policy
	updatedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timer.Id)
	require.Nil(t, getErr)
	require.NotNil(t, updatedTimer)

	// Verify updated fields
	assert.Equal(t, "https://example.com/callback-updated", updatedTimer.CallbackUrl)
	assert.Equal(t, int32(45), updatedTimer.CallbackTimeoutSeconds)
	assert.Nil(t, updatedTimer.Payload)
	assert.Nil(t, updatedTimer.RetryPolicy)
}

func TestUpdateTimerNoLock_InvalidPayloadSerialization(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Try to update with an invalid payload that can't be marshaled to JSON
	invalidPayload := map[string]interface{}{
		"function": func() {}, // Functions can't be marshaled to JSON
	}

	updateRequest := &databases.UpdateDbTimerRequest{
		TimerId:                "test-timer",
		ExecuteAt:              time.Now().Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		Payload:                invalidPayload,
		RetryPolicy:            nil,
	}

	updateErr := store.UpdateTimerNoLock(ctx, shardId, namespace, updateRequest)
	require.NotNil(t, updateErr)
	assert.Contains(t, updateErr.Error(), "failed to marshal timer payload")
}

func TestDeleteTimerNoLock_Success(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Create shard record
	ownerAddr := "owner-1"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	shardVersion := currentShardInfo.ShardVersion
	require.Nil(t, err)

	// Create a timer to delete
	now := time.Now().UTC().Truncate(time.Millisecond)
	timer := &databases.DbTimer{
		Id:                     "test-timer-delete-nolock",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "test-timer-delete-nolock"),
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

	// Delete the timer without shard locking
	deleteErr := store.DeleteTimerNoLock(ctx, shardId, namespace, timer.Id)
	require.Nil(t, deleteErr)

	// Verify timer is deleted
	deletedTimer, getErr2 := store.GetTimer(ctx, shardId, namespace, timer.Id)
	assert.Nil(t, deletedTimer)
	assert.NotNil(t, getErr2)
	assert.True(t, databases.IsDbErrorNotExists(getErr2))
}

func TestDeleteTimerNoLock_NotExists(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Try to delete a non-existent timer (should return not found error)
	deleteErr := store.DeleteTimerNoLock(ctx, shardId, namespace, "non-existent-timer")
	require.NotNil(t, deleteErr)
	assert.True(t, databases.IsDbErrorNotExists(deleteErr))
}
