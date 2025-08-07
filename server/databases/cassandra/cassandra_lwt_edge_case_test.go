package cassandra

import (
	"context"
	"testing"
	"time"

	"github.com/gocql/gocql"
	"github.com/iworkflowio/durable-timer/databases"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCassandraLWTBatchEdgeCase tests the specific edge case where we combine:
// 1. A shard version check (which should succeed)
// 2. A delete operation on a non-existing timer (which should fail with IF EXISTS)
// This test verifies our understanding of how Cassandra returns the [applied] field in such cases.
func TestCassandraLWTBatchEdgeCase(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1

	// Step 1: Create a shard record first
	ownerAddr := "owner-1"
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
	require.Nil(t, err)
	require.Equal(t, int64(1), shardVersion)

	// Step 2: Set up the exact same batch pattern as DeleteTimer uses
	zeroUuidHigh, zeroUuidLow := databases.UuidToHighLow(databases.ZeroUUID)

	// Create fake timer coordinates for a non-existing timer
	fakeExecuteAt := time.Now().UTC().Add(5 * time.Minute)
	fakeUuidHigh := int64(12345)
	fakeUuidLow := int64(67890)

	// Create the same batch pattern as DeleteTimer method
	batch := store.session.NewBatch(gocql.LoggedBatch).WithContext(ctx)

	// Add shard version check (this should succeed since shard exists with correct version)
	checkVersionQuery := `UPDATE timers SET shard_version = ? WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid_high = ? AND timer_uuid_low = ? IF shard_version = ?`
	batch.Query(checkVersionQuery, shardVersion, shardId, databases.RowTypeShard, databases.ZeroTimestamp, zeroUuidHigh, zeroUuidLow, shardVersion)

	// Add DELETE statement for a non-existing timer with IF EXISTS (this should fail)
	deleteQuery := `DELETE FROM timers WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid_high = ? AND timer_uuid_low = ? IF EXISTS`
	batch.Query(deleteQuery, shardId, databases.RowTypeTimer, fakeExecuteAt, fakeUuidHigh, fakeUuidLow)

	// Execute the batch and capture results
	previous := make(map[string]interface{})
	applied, iter, batchErr := store.session.MapExecuteBatchCAS(batch, previous)
	if iter != nil {
		iter.Close()
	}

	// Verify no execution error
	require.Nil(t, batchErr, "Batch execution should not return an error")

	// The batch should not be applied because the DELETE IF EXISTS failed
	assert.False(t, applied, "Batch should not be applied due to non-existing timer")

	// Debug: Print all returned values for analysis
	t.Logf("Batch applied: %v", applied)
	t.Logf("Previous map contents:")
	for key, value := range previous {
		t.Logf("  %s: %v (type: %T)", key, value, value)
	}

	// CRITICAL DISCOVERY: The [applied] field does NOT exist in Cassandra LWT batch responses!
	// This means the current implementation in DeleteTimer is incorrect.
	// Test our edge case logic: check if [applied] field exists
	if existsValue, hasExists := previous["[applied]"]; hasExists {
		t.Errorf("UNEXPECTED: [applied] field found: %v", existsValue)
		t.Errorf("This contradicts our findings - Cassandra should not return [applied] in batch LWT")
	} else {
		t.Logf("✅ CONFIRMED: No [applied] field found in previous map")
		t.Logf("This means the current DeleteTimer implementation is WRONG!")
		t.Logf("Correct logic: When batch fails, check shard_version to determine the failure reason")
	}

	// Also check for shard_version field to ensure shard check logic works
	if shardVersionValue, exists := previous["shard_version"]; exists {
		t.Logf("Found shard_version field: %v", shardVersionValue)
		if shardVersionValue != nil {
			// This means shard version check succeeded but returned the current value
			currentVersion := shardVersionValue.(int64)
			assert.Equal(t, shardVersion, currentVersion, "Returned shard version should match expected")
		}
	} else {
		t.Logf("No shard_version field found in previous map")
	}
}

// TestCassandraLWTBatchSuccessCase tests the success case where both operations succeed
func TestCassandraLWTBatchSuccessCase(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Step 1: Create a shard record
	ownerAddr := "owner-1"
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
	require.Nil(t, err)
	require.Equal(t, int64(1), shardVersion)

	// Step 2: Create a real timer to delete
	timer := &databases.DbTimer{
		Id:                     "timer-for-lwt-test",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-for-lwt-test"),
		Namespace:              namespace,
		ExecuteAt:              time.Now().UTC().Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              time.Now().UTC(),
	}

	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Step 3: Get the timer's primary key components for deletion
	timerUuidHigh, timerUuidLow := databases.UuidToHighLow(timer.TimerUuid)
	zeroUuidHigh, zeroUuidLow := databases.UuidToHighLow(databases.ZeroUUID)

	// Step 4: Execute the same batch pattern but with an existing timer
	batch := store.session.NewBatch(gocql.LoggedBatch).WithContext(ctx)

	// Add shard version check
	checkVersionQuery := `UPDATE timers SET shard_version = ? WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid_high = ? AND timer_uuid_low = ? IF shard_version = ?`
	batch.Query(checkVersionQuery, shardVersion, shardId, databases.RowTypeShard, databases.ZeroTimestamp, zeroUuidHigh, zeroUuidLow, shardVersion)

	// Add DELETE statement for the existing timer
	deleteQuery := `DELETE FROM timers WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid_high = ? AND timer_uuid_low = ? IF EXISTS`
	batch.Query(deleteQuery, shardId, databases.RowTypeTimer, timer.ExecuteAt, timerUuidHigh, timerUuidLow)

	// Execute the batch
	previous := make(map[string]interface{})
	applied, iter, batchErr := store.session.MapExecuteBatchCAS(batch, previous)
	if iter != nil {
		iter.Close()
	}

	// Verify results
	require.Nil(t, batchErr, "Batch execution should not return an error")
	assert.True(t, applied, "Batch should be applied successfully")

	// Debug: Print results for success case
	t.Logf("Success case - Batch applied: %v", applied)
	t.Logf("Previous map contents:")
	for key, value := range previous {
		t.Logf("  %s: %v (type: %T)", key, value, value)
	}

	// Verify timer was actually deleted
	deletedTimer, getErr := store.GetTimer(ctx, shardId, namespace, "timer-for-lwt-test")
	assert.Nil(t, deletedTimer)
	assert.NotNil(t, getErr)
	assert.True(t, databases.IsDbErrorNotExists(getErr))
}

// TestCassandraLWTBatchShardVersionMismatch tests the case where shard version check fails
func TestCassandraLWTBatchShardVersionMismatch(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Step 1: Create a shard record
	ownerAddr := "owner-1"
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
	require.Nil(t, err)
	require.Equal(t, int64(1), shardVersion)

	// Step 2: Create a timer
	timer := &databases.DbTimer{
		Id:                     "timer-for-shard-test",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-for-shard-test"),
		Namespace:              namespace,
		ExecuteAt:              time.Now().UTC().Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              time.Now().UTC(),
	}

	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Step 3: Try batch with wrong shard version
	timerUuidHigh, timerUuidLow := databases.UuidToHighLow(timer.TimerUuid)
	zeroUuidHigh, zeroUuidLow := databases.UuidToHighLow(databases.ZeroUUID)
	wrongShardVersion := shardVersion + 999

	batch := store.session.NewBatch(gocql.LoggedBatch).WithContext(ctx)

	// Add shard version check with wrong version
	checkVersionQuery := `UPDATE timers SET shard_version = ? WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid_high = ? AND timer_uuid_low = ? IF shard_version = ?`
	batch.Query(checkVersionQuery, wrongShardVersion, shardId, databases.RowTypeShard, databases.ZeroTimestamp, zeroUuidHigh, zeroUuidLow, wrongShardVersion)

	// Add DELETE statement for the existing timer
	deleteQuery := `DELETE FROM timers WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid_high = ? AND timer_uuid_low = ? IF EXISTS`
	batch.Query(deleteQuery, shardId, databases.RowTypeTimer, timer.ExecuteAt, timerUuidHigh, timerUuidLow)

	// Execute the batch
	previous := make(map[string]interface{})
	applied, iter, batchErr := store.session.MapExecuteBatchCAS(batch, previous)
	if iter != nil {
		iter.Close()
	}

	// Verify results
	require.Nil(t, batchErr, "Batch execution should not return an error")
	assert.False(t, applied, "Batch should not be applied due to shard version mismatch")

	// Debug: Print results for shard version mismatch case
	t.Logf("Shard version mismatch case - Batch applied: %v", applied)
	t.Logf("Previous map contents:")
	for key, value := range previous {
		t.Logf("  %s: %v (type: %T)", key, value, value)
	}

	// Check for shard_version field indicating mismatch
	if shardVersionValue, exists := previous["shard_version"]; exists {
		t.Logf("Found shard_version field indicating current version: %v", shardVersionValue)
		if shardVersionValue != nil {
			currentVersion := shardVersionValue.(int64)
			assert.Equal(t, shardVersion, currentVersion, "Should return the actual current shard version")
			assert.NotEqual(t, wrongShardVersion, currentVersion, "Should not match our wrong version")
		}
	}

	// Verify timer still exists (was not deleted due to failed batch)
	existingTimer, getErr := store.GetTimer(ctx, shardId, namespace, "timer-for-shard-test")
	assert.Nil(t, getErr)
	assert.NotNil(t, existingTimer)
	assert.Equal(t, timer.Id, existingTimer.Id)
}

// TestDeleteTimer_NonExistentTimerWithCorrectShardVersion tests the specific edge case
// where we try to delete a non-existing timer with the correct shard version.
// This should return a NotExists error (caller can treat as idempotent if desired).
func TestDeleteTimer_NonExistentTimerWithCorrectShardVersion(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Step 1: Create a shard record
	ownerAddr := "owner-1"
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
	require.Nil(t, err)
	require.Equal(t, int64(1), shardVersion)

	// Step 2: Try to delete a non-existing timer (should return NotExists error)
	deleteErr := store.DeleteTimer(ctx, shardId, shardVersion, namespace, "non-existent-timer")

	// This should return NotExists error (caller can treat as idempotent if desired)
	assert.NotNil(t, deleteErr, "Deleting non-existent timer should return an error")
	assert.True(t, databases.IsDbErrorNotExists(deleteErr), "Should return NotExists error for non-existent timer")
}
