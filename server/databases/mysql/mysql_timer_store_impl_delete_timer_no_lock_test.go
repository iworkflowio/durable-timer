package mysql

import (
	"context"
	"testing"
	"time"

	"github.com/iworkflowio/durable-timer/databases"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeleteTimerNoLock_Success(t *testing.T) {
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

	// Create a timer to delete
	now := time.Now().UTC().Truncate(time.Millisecond)
	timer := &databases.DbTimer{
		Id:                     "test-timer-delete-no-lock",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "test-timer-delete-no-lock"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
		Payload:                map[string]interface{}{"key": "value"},
		RetryPolicy:            map[string]interface{}{"maxAttempts": 3},
	}

	// Insert the timer
	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Verify timer exists before deletion
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timer.Id)
	require.Nil(t, getErr)
	require.NotNil(t, retrievedTimer)

	// Delete the timer using NoLock method
	deleteErr := store.DeleteTimerNoLock(ctx, shardId, namespace, timer.Id)
	require.Nil(t, deleteErr)

	// Verify timer no longer exists
	retrievedTimer, getErr = store.GetTimer(ctx, shardId, namespace, timer.Id)
	assert.Nil(t, retrievedTimer)
	assert.NotNil(t, getErr)
	assert.True(t, databases.IsDbErrorNotExists(getErr))
}

func TestDeleteTimerNoLock_NotExists(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Try to delete a non-existent timer - should return not found error
	deleteErr := store.DeleteTimerNoLock(ctx, shardId, namespace, "non-existent-timer")
	require.NotNil(t, deleteErr)
	assert.True(t, databases.IsDbErrorNotExists(deleteErr))
}

func TestDeleteTimerNoLock_NoShardVersionCheck(t *testing.T) {
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

	// Create a timer to delete
	now := time.Now().UTC().Truncate(time.Millisecond)
	timer := &databases.DbTimer{
		Id:                     "test-timer-no-lock-delete",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "test-timer-no-lock-delete"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
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

	// Now try to delete timer using DeleteTimerNoLock
	// This should succeed because DeleteTimerNoLock doesn't check shard version
	deleteErr := store.DeleteTimerNoLock(ctx, shardId, namespace, timer.Id)
	require.Nil(t, deleteErr)

	// Verify timer was deleted
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timer.Id)
	assert.Nil(t, retrievedTimer)
	assert.NotNil(t, getErr)
	assert.True(t, databases.IsDbErrorNotExists(getErr))

	// For comparison, create another timer and try using the locked version with the old shard version
	timer2 := &databases.DbTimer{
		Id:                     "test-timer-locked-delete",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "test-timer-locked-delete"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(20 * time.Minute),
		CallbackUrl:            "https://example.com/callback2",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
	}

	// Insert the second timer using NoLock (since shard version changed)
	createErr2 := store.CreateTimerNoLock(ctx, shardId, namespace, timer2)
	require.Nil(t, createErr2)

	// This should fail due to shard version mismatch
	lockedDeleteErr := store.DeleteTimer(ctx, shardId, shardVersion, namespace, timer2.Id)
	require.NotNil(t, lockedDeleteErr)
	assert.True(t, lockedDeleteErr.ShardConditionFail)

	// But NoLock version should work
	noLockDeleteErr := store.DeleteTimerNoLock(ctx, shardId, namespace, timer2.Id)
	require.Nil(t, noLockDeleteErr)
}

func TestDeleteTimerNoLock_MultipleTimers(t *testing.T) {
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

	// Create multiple timers
	now := time.Now().UTC().Truncate(time.Millisecond)
	timerIds := []string{"timer1", "timer2", "timer3"}

	for _, timerId := range timerIds {
		timer := &databases.DbTimer{
			Id:                     timerId,
			TimerUuid:              databases.GenerateTimerUUID(namespace, timerId),
			Namespace:              namespace,
			ExecuteAt:              now.Add(10 * time.Minute),
			CallbackUrl:            "https://example.com/callback",
			CallbackTimeoutSeconds: 30,
			CreatedAt:              now,
		}

		createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
		require.Nil(t, createErr)
	}

	// Verify all timers exist
	for _, timerId := range timerIds {
		retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timerId)
		require.Nil(t, getErr)
		require.NotNil(t, retrievedTimer)
		assert.Equal(t, timerId, retrievedTimer.Id)
	}

	// Delete timers one by one using NoLock
	for _, timerId := range timerIds {
		deleteErr := store.DeleteTimerNoLock(ctx, shardId, namespace, timerId)
		require.Nil(t, deleteErr)

		// Verify this timer is deleted
		retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, timerId)
		assert.Nil(t, retrievedTimer)
		assert.NotNil(t, getErr)
		assert.True(t, databases.IsDbErrorNotExists(getErr))
	}

	// Verify all timers are deleted - try to delete them again (should return not found)
	for _, timerId := range timerIds {
		deleteErr := store.DeleteTimerNoLock(ctx, shardId, namespace, timerId)
		require.NotNil(t, deleteErr)
		assert.True(t, databases.IsDbErrorNotExists(deleteErr))
	}
}

func TestDeleteTimerNoLock_DifferentNamespaces(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace1 := "namespace1"
	namespace2 := "namespace2"
	timerId := "same-timer-id"

	// First, create a shard record
	ownerAddr := "owner-1"
	_, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	require.Nil(t, err)
	shardVersion := currentShardInfo.ShardVersion

	// Create timers with same ID in different namespaces
	now := time.Now().UTC().Truncate(time.Millisecond)

	timer1 := &databases.DbTimer{
		Id:                     timerId,
		TimerUuid:              databases.GenerateTimerUUID(namespace1, timerId),
		Namespace:              namespace1,
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback1",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
	}

	timer2 := &databases.DbTimer{
		Id:                     timerId,
		TimerUuid:              databases.GenerateTimerUUID(namespace2, timerId),
		Namespace:              namespace2,
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback2",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
	}

	// Insert both timers
	createErr1 := store.CreateTimer(ctx, shardId, shardVersion, namespace1, timer1)
	require.Nil(t, createErr1)

	createErr2 := store.CreateTimer(ctx, shardId, shardVersion, namespace2, timer2)
	require.Nil(t, createErr2)

	// Verify both timers exist
	retrievedTimer1, getErr1 := store.GetTimer(ctx, shardId, namespace1, timerId)
	require.Nil(t, getErr1)
	require.NotNil(t, retrievedTimer1)
	assert.Equal(t, namespace1, retrievedTimer1.Namespace)

	retrievedTimer2, getErr2 := store.GetTimer(ctx, shardId, namespace2, timerId)
	require.Nil(t, getErr2)
	require.NotNil(t, retrievedTimer2)
	assert.Equal(t, namespace2, retrievedTimer2.Namespace)

	// Delete timer from namespace1 only
	deleteErr := store.DeleteTimerNoLock(ctx, shardId, namespace1, timerId)
	require.Nil(t, deleteErr)

	// Verify timer in namespace1 is deleted but namespace2 timer still exists
	retrievedTimer1, getErr1 = store.GetTimer(ctx, shardId, namespace1, timerId)
	assert.Nil(t, retrievedTimer1)
	assert.NotNil(t, getErr1)
	assert.True(t, databases.IsDbErrorNotExists(getErr1))

	retrievedTimer2, getErr2 = store.GetTimer(ctx, shardId, namespace2, timerId)
	require.Nil(t, getErr2)
	require.NotNil(t, retrievedTimer2)
	assert.Equal(t, namespace2, retrievedTimer2.Namespace)

	// Delete timer from namespace2
	deleteErr2 := store.DeleteTimerNoLock(ctx, shardId, namespace2, timerId)
	require.Nil(t, deleteErr2)

	// Verify both timers are now deleted
	retrievedTimer2, getErr2 = store.GetTimer(ctx, shardId, namespace2, timerId)
	assert.Nil(t, retrievedTimer2)
	assert.NotNil(t, getErr2)
	assert.True(t, databases.IsDbErrorNotExists(getErr2))
}

func TestDeleteTimerNoLock_ContextCancellation(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	shardId := 1
	namespace := "test_namespace"

	// Create a cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Try to delete with cancelled context
	deleteErr := store.DeleteTimerNoLock(ctx, shardId, namespace, "test-timer")
	require.NotNil(t, deleteErr)
	// Should get a context cancellation error
	assert.Contains(t, deleteErr.Error(), "context canceled")
}
