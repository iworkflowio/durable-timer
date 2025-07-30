package mongodb

import (
	"context"
	"testing"
	"time"

	"github.com/iworkflowio/durable-timer/databases"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
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
	assert.Equal(t, 2, response.DeletedCount) // MongoDB returns actual deleted count

	// Verify deleted timers are gone
	count1, _ := store.collection.CountDocuments(ctx, bson.M{
		"shard_id":        shardId,
		"row_type":        databases.RowTypeTimer,
		"timer_namespace": namespace,
		"timer_id":        "timer-to-delete-1",
	})
	assert.Equal(t, int64(0), count1, "timer-to-delete-1 should be deleted")

	count2, _ := store.collection.CountDocuments(ctx, bson.M{
		"shard_id":        shardId,
		"row_type":        databases.RowTypeTimer,
		"timer_namespace": namespace,
		"timer_id":        "timer-to-delete-2",
	})
	assert.Equal(t, int64(0), count2, "timer-to-delete-2 should be deleted")

	// Verify timer outside range still exists
	countOutside, _ := store.collection.CountDocuments(ctx, bson.M{
		"shard_id":        shardId,
		"row_type":        databases.RowTypeTimer,
		"timer_namespace": namespace,
		"timer_id":        "timer-outside-range",
	})
	assert.Equal(t, int64(1), countOutside, "timer-outside-range should NOT be deleted")

	// Verify new timers were inserted
	countNew1, _ := store.collection.CountDocuments(ctx, bson.M{
		"shard_id":        shardId,
		"row_type":        databases.RowTypeTimer,
		"timer_namespace": namespace,
		"timer_id":        "new-timer-1",
	})
	assert.Equal(t, int64(1), countNew1, "new-timer-1 should be inserted")

	countNew2, _ := store.collection.CountDocuments(ctx, bson.M{
		"shard_id":        shardId,
		"row_type":        databases.RowTypeTimer,
		"timer_namespace": namespace,
		"timer_id":        "new-timer-2",
	})
	assert.Equal(t, int64(1), countNew2, "new-timer-2 should be inserted")

	// Verify new timer data is correct
	var timerDoc bson.M
	findErr := store.collection.FindOne(ctx, bson.M{
		"timer_namespace": namespace,
		"timer_id":        "new-timer-1",
	}).Decode(&timerDoc)
	require.NoError(t, findErr)
	assert.Equal(t, "https://example.com/new-callback1", timerDoc["timer_callback_url"])
	assert.Contains(t, timerDoc["timer_payload"], "value1")
	assert.Equal(t, int32(45), timerDoc["timer_callback_timeout_seconds"])

	findErr = store.collection.FindOne(ctx, bson.M{
		"timer_namespace": namespace,
		"timer_id":        "new-timer-2",
	}).Decode(&timerDoc)
	require.NoError(t, findErr)
	assert.Equal(t, "https://example.com/new-callback2", timerDoc["timer_callback_url"])
	assert.Contains(t, timerDoc["timer_retry_policy"], "maxAttempts")
	assert.Equal(t, int32(60), timerDoc["timer_callback_timeout_seconds"])
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
	countOriginal, _ := store.collection.CountDocuments(ctx, bson.M{
		"shard_id":        shardId,
		"row_type":        databases.RowTypeTimer,
		"timer_namespace": namespace,
		"timer_id":        "timer-version-test",
	})
	assert.Equal(t, int64(1), countOriginal, "original timer should still exist due to rollback")

	// Verify new timer was NOT inserted (operation was rolled back)
	countNew, _ := store.collection.CountDocuments(ctx, bson.M{
		"shard_id":        shardId,
		"row_type":        databases.RowTypeTimer,
		"timer_namespace": namespace,
		"timer_id":        "new-timer-version-test",
	})
	assert.Equal(t, int64(0), countNew, "new timer should NOT be inserted due to rollback")
}
