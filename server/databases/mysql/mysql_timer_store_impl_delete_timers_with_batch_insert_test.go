package mysql

import (
	"context"
	"testing"
	"time"

	"github.com/iworkflowio/durable-timer/databases"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeleteTimersUpToTimestampWithBatchInsert_Basic(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// First, create a shard record
	ownerId := "owner-1"
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, ownerId, nil)
	require.Nil(t, err)
	require.Equal(t, int64(1), shardVersion)

	// Create some timers to be deleted
	now := time.Now()
	timer1 := &databases.DbTimer{
		Id:                     "timer-to-delete-1",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-to-delete-1"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/callback1",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
	}
	timer2 := &databases.DbTimer{
		Id:                     "timer-to-delete-2",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-to-delete-2"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback2",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
	}

	// Create timers that should NOT be deleted (outside range)
	timerOutsideRange := &databases.DbTimer{
		Id:                     "timer-outside-range",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-outside-range"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(20 * time.Minute), // Outside delete range
		CallbackUrl:            "https://example.com/callback-outside",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
	}

	// Insert initial timers
	createErr1 := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer1)
	require.Nil(t, createErr1)
	createErr2 := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer2)
	require.Nil(t, createErr2)
	createErr3 := store.CreateTimer(ctx, shardId, shardVersion, namespace, timerOutsideRange)
	require.Nil(t, createErr3)

	// Create new timers to be inserted
	newTimer1 := &databases.DbTimer{
		Id:                     "new-timer-1",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "new-timer-1"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(15 * time.Minute),
		CallbackUrl:            "https://example.com/new-callback1",
		CallbackTimeoutSeconds: 45,
		CreatedAt:              now,
		Payload:                map[string]interface{}{"key": "value1"},
	}
	newTimer2 := &databases.DbTimer{
		Id:                     "new-timer-2",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "new-timer-2"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(25 * time.Minute),
		CallbackUrl:            "https://example.com/new-callback2",
		CallbackTimeoutSeconds: 60,
		CreatedAt:              now,
		RetryPolicy:            map[string]interface{}{"maxAttempts": 3},
	}

	// Define delete range
	deleteRequest := &databases.RangeDeleteTimersRequest{
		StartTimestamp: now.Add(2 * time.Minute),                  // Should include timer1 and timer2
		StartTimeUuid:  databases.ZeroUUID,                        // Include all UUIDs from start time
		EndTimestamp:   now.Add(12 * time.Minute),                 // Should NOT include timerOutsideRange
		EndTimeUuid:    databases.GenerateTimerUUID("max", "max"), // Include all UUIDs up to end time
	}

	timersToInsert := []*databases.DbTimer{newTimer1, newTimer2}

	// Execute delete and insert operation
	response, deleteErr := store.DeleteTimersUpToTimestampWithBatchInsert(ctx, shardId, shardVersion, deleteRequest, timersToInsert)
	assert.Nil(t, deleteErr)
	require.NotNil(t, response)
	assert.Equal(t, 2, response.DeletedCount) // MySQL returns actual deleted count

	// Verify deleted timers are gone
	var count1, count2 int
	countQuery := `SELECT COUNT(*) FROM timers WHERE shard_id = ? AND row_type = ? AND timer_namespace = ? AND timer_id = ?`
	scanErr := store.db.QueryRow(countQuery, shardId, databases.RowTypeTimer, namespace, "timer-to-delete-1").Scan(&count1)
	require.NoError(t, scanErr)
	assert.Equal(t, 0, count1, "timer-to-delete-1 should be deleted")

	scanErr = store.db.QueryRow(countQuery, shardId, databases.RowTypeTimer, namespace, "timer-to-delete-2").Scan(&count2)
	require.NoError(t, scanErr)
	assert.Equal(t, 0, count2, "timer-to-delete-2 should be deleted")

	// Verify timer outside range still exists
	var countOutside int
	scanErr = store.db.QueryRow(countQuery, shardId, databases.RowTypeTimer, namespace, "timer-outside-range").Scan(&countOutside)
	require.NoError(t, scanErr)
	assert.Equal(t, 1, countOutside, "timer-outside-range should NOT be deleted")

	// Verify new timers were inserted
	var countNew1, countNew2 int
	scanErr = store.db.QueryRow(countQuery, shardId, databases.RowTypeTimer, namespace, "new-timer-1").Scan(&countNew1)
	require.NoError(t, scanErr)
	assert.Equal(t, 1, countNew1, "new-timer-1 should be inserted")

	scanErr = store.db.QueryRow(countQuery, shardId, databases.RowTypeTimer, namespace, "new-timer-2").Scan(&countNew2)
	require.NoError(t, scanErr)
	assert.Equal(t, 1, countNew2, "new-timer-2 should be inserted")

	// Verify new timer data is correct
	var dbCallbackUrl, dbPayload, dbRetryPolicy string
	var dbTimeout int32
	selectQuery := `SELECT timer_callback_url, timer_payload, timer_retry_policy, timer_callback_timeout_seconds FROM timers 
	                WHERE timer_namespace = ? AND timer_id = ?`

	scanErr = store.db.QueryRow(selectQuery, namespace, "new-timer-1").
		Scan(&dbCallbackUrl, &dbPayload, &dbRetryPolicy, &dbTimeout)
	require.NoError(t, scanErr)
	assert.Equal(t, "https://example.com/new-callback1", dbCallbackUrl)
	assert.Contains(t, dbPayload, "value1")
	assert.Equal(t, int32(45), dbTimeout)

	scanErr = store.db.QueryRow(selectQuery, namespace, "new-timer-2").
		Scan(&dbCallbackUrl, &dbPayload, &dbRetryPolicy, &dbTimeout)
	require.NoError(t, scanErr)
	assert.Equal(t, "https://example.com/new-callback2", dbCallbackUrl)
	assert.Contains(t, dbRetryPolicy, "maxAttempts")
	assert.Equal(t, int32(60), dbTimeout)
}

func TestDeleteTimersUpToTimestampWithBatchInsert_ShardVersionMismatch(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 4
	namespace := "test_namespace"

	// First, create a shard record
	actualShardVersion, err := store.ClaimShardOwnership(ctx, shardId, "owner-1", nil)
	require.Nil(t, err)

	// Create a timer to be deleted
	now := time.Now()
	timer := &databases.DbTimer{
		Id:                     "timer-version-test",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-version-test"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
	}

	createErr := store.CreateTimer(ctx, shardId, actualShardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Create new timer to insert
	newTimer := &databases.DbTimer{
		Id:                     "new-timer-version-test",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "new-timer-version-test"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(15 * time.Minute),
		CallbackUrl:            "https://example.com/new-callback",
		CallbackTimeoutSeconds: 45,
		CreatedAt:              now,
	}

	deleteRequest := &databases.RangeDeleteTimersRequest{
		StartTimestamp: now.Add(2 * time.Minute),
		StartTimeUuid:  databases.ZeroUUID,
		EndTimestamp:   now.Add(10 * time.Minute),
		EndTimeUuid:    databases.GenerateTimerUUID("max", "max"),
	}

	timersToInsert := []*databases.DbTimer{newTimer}

	// Try to execute with wrong shard version
	wrongShardVersion := actualShardVersion + 1
	_, deleteErr := store.DeleteTimersUpToTimestampWithBatchInsert(ctx, shardId, wrongShardVersion, deleteRequest, timersToInsert)

	// Should fail with shard condition error
	assert.NotNil(t, deleteErr)
	assert.True(t, deleteErr.ShardConditionFail)
	assert.Equal(t, actualShardVersion, deleteErr.ConflictShardVersion)

	// Verify original timer still exists (operation was rolled back)
	var countOriginal int
	countQuery := `SELECT COUNT(*) FROM timers WHERE shard_id = ? AND row_type = ? AND timer_namespace = ? AND timer_id = ?`
	scanErr := store.db.QueryRow(countQuery, shardId, databases.RowTypeTimer, namespace, "timer-version-test").Scan(&countOriginal)
	require.NoError(t, scanErr)
	assert.Equal(t, 1, countOriginal, "original timer should still exist due to rollback")

	// Verify new timer was NOT inserted (operation was rolled back)
	var countNew int
	scanErr = store.db.QueryRow(countQuery, shardId, databases.RowTypeTimer, namespace, "new-timer-version-test").Scan(&countNew)
	require.NoError(t, scanErr)
	assert.Equal(t, 0, countNew, "new timer should NOT be inserted due to rollback")
}

func TestDeleteTimersUpToTimestampWithBatchInsert_InsertInDeleteRange(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 9
	namespace := "test_namespace"

	// First, create a shard record
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, "owner-1", nil)
	require.Nil(t, err)

	// Create timers that will be deleted
	now := time.Now()
	timer1 := &databases.DbTimer{
		Id:                     "timer-to-delete-1",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-to-delete-1"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/delete-me-1",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
	}
	timer2 := &databases.DbTimer{
		Id:                     "timer-to-delete-2",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-to-delete-2"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(8 * time.Minute),
		CallbackUrl:            "https://example.com/delete-me-2",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
	}

	// Insert the timers to be deleted
	createErr1 := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer1)
	require.Nil(t, createErr1)
	createErr2 := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer2)
	require.Nil(t, createErr2)

	// Create new timers to insert - these fall WITHIN the same delete range
	newTimer1 := &databases.DbTimer{
		Id:                     "new-timer-in-range-1",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "new-timer-in-range-1"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(6 * time.Minute), // Within delete range
		CallbackUrl:            "https://example.com/new-callback-1",
		CallbackTimeoutSeconds: 45,
		CreatedAt:              now,
		Payload:                map[string]interface{}{"type": "insert-in-range", "index": 1},
	}
	newTimer2 := &databases.DbTimer{
		Id:                     "new-timer-in-range-2",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "new-timer-in-range-2"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(7 * time.Minute), // Within delete range
		CallbackUrl:            "https://example.com/new-callback-2",
		CallbackTimeoutSeconds: 60,
		CreatedAt:              now,
		RetryPolicy:            map[string]interface{}{"maxAttempts": 3, "strategy": "linear"},
	}

	// Define delete range that encompasses both existing and new timers
	deleteRequest := &databases.RangeDeleteTimersRequest{
		StartTimestamp: now.Add(4 * time.Minute), // Covers timer1, newTimer1, newTimer2, timer2
		StartTimeUuid:  databases.ZeroUUID,
		EndTimestamp:   now.Add(9 * time.Minute), // Covers all timers in range
		EndTimeUuid:    databases.GenerateTimerUUID("max", "max"),
	}

	timersToInsert := []*databases.DbTimer{newTimer1, newTimer2}

	// Execute delete and insert operation
	response, deleteErr := store.DeleteTimersUpToTimestampWithBatchInsert(ctx, shardId, shardVersion, deleteRequest, timersToInsert)
	assert.Nil(t, deleteErr)
	require.NotNil(t, response)
	assert.Equal(t, 2, response.DeletedCount) // MySQL returns actual deleted count

	// Verify original timers were deleted
	var count1, count2 int
	countQuery := `SELECT COUNT(*) FROM timers WHERE shard_id = ? AND row_type = ? AND timer_namespace = ? AND timer_id = ?`

	scanErr := store.db.QueryRow(countQuery, shardId, databases.RowTypeTimer, namespace, "timer-to-delete-1").Scan(&count1)
	require.NoError(t, scanErr)
	assert.Equal(t, 0, count1, "timer-to-delete-1 should be deleted")

	scanErr = store.db.QueryRow(countQuery, shardId, databases.RowTypeTimer, namespace, "timer-to-delete-2").Scan(&count2)
	require.NoError(t, scanErr)
	assert.Equal(t, 0, count2, "timer-to-delete-2 should be deleted")

	// Verify new timers were inserted successfully DESPITE being in the delete range
	var countNew1, countNew2 int
	scanErr = store.db.QueryRow(countQuery, shardId, databases.RowTypeTimer, namespace, "new-timer-in-range-1").Scan(&countNew1)
	require.NoError(t, scanErr)
	assert.Equal(t, 1, countNew1, "new-timer-in-range-1 should be inserted")

	scanErr = store.db.QueryRow(countQuery, shardId, databases.RowTypeTimer, namespace, "new-timer-in-range-2").Scan(&countNew2)
	require.NoError(t, scanErr)
	assert.Equal(t, 1, countNew2, "new-timer-in-range-2 should be inserted")

	// Verify that ONLY the expected timers exist in the range
	var totalInRange int
	rangeQuery := `SELECT COUNT(*) FROM timers WHERE shard_id = ? AND row_type = ? 
	               AND (timer_execute_at, timer_uuid_high, timer_uuid_low) >= (?, ?, ?)
	               AND (timer_execute_at, timer_uuid_high, timer_uuid_low) <= (?, ?, ?)`

	startUuidHigh, startUuidLow := databases.UuidToHighLow(databases.ZeroUUID)
	endUuidHigh, endUuidLow := databases.UuidToHighLow(databases.GenerateTimerUUID("max", "max"))

	scanErr = store.db.QueryRow(rangeQuery, shardId, databases.RowTypeTimer,
		now.Add(4*time.Minute), startUuidHigh, startUuidLow,
		now.Add(9*time.Minute), endUuidHigh, endUuidLow).Scan(&totalInRange)
	require.NoError(t, scanErr)
	assert.Equal(t, 2, totalInRange, "Only the 2 newly inserted timers should exist in the range")
}
