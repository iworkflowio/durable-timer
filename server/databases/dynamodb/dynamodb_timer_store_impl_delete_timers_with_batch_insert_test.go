package dynamodb

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
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
	now := time.Now().UTC().Truncate(time.Millisecond) // DynamoDB stores in UTC with millisecond precision
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
	assert.Equal(t, 2, response.DeletedCount) // DynamoDB returns actual deleted count

	// Verify deleted timers are gone
	timerSortKey1 := databases.FormatExecuteAtWithUuid(timer1.ExecuteAt, timer1.TimerUuid.String())
	getInput1 := &dynamodb.GetItemInput{
		TableName: aws.String(store.tableName),
		Key: map[string]types.AttributeValue{
			"shard_id": &types.AttributeValueMemberN{Value: strconv.Itoa(shardId)},
			"sort_key": &types.AttributeValueMemberS{Value: timerSortKey1},
		},
	}
	result1, _ := store.client.GetItem(ctx, getInput1)
	assert.Nil(t, result1.Item, "timer-to-delete-1 should be deleted")

	timerSortKey2 := databases.FormatExecuteAtWithUuid(timer2.ExecuteAt, timer2.TimerUuid.String())
	getInput2 := &dynamodb.GetItemInput{
		TableName: aws.String(store.tableName),
		Key: map[string]types.AttributeValue{
			"shard_id": &types.AttributeValueMemberN{Value: strconv.Itoa(shardId)},
			"sort_key": &types.AttributeValueMemberS{Value: timerSortKey2},
		},
	}
	result2, _ := store.client.GetItem(ctx, getInput2)
	assert.Nil(t, result2.Item, "timer-to-delete-2 should be deleted")

	// Verify timer outside range still exists
	timerSortKeyOutside := databases.FormatExecuteAtWithUuid(timerOutsideRange.ExecuteAt, timerOutsideRange.TimerUuid.String())
	getInputOutside := &dynamodb.GetItemInput{
		TableName: aws.String(store.tableName),
		Key: map[string]types.AttributeValue{
			"shard_id": &types.AttributeValueMemberN{Value: strconv.Itoa(shardId)},
			"sort_key": &types.AttributeValueMemberS{Value: timerSortKeyOutside},
		},
	}
	resultOutside, _ := store.client.GetItem(ctx, getInputOutside)
	assert.NotNil(t, resultOutside.Item, "timer-outside-range should NOT be deleted")

	// Verify new timers were inserted
	newTimerSortKey1 := databases.FormatExecuteAtWithUuid(newTimer1.ExecuteAt, newTimer1.TimerUuid.String())
	getInputNew1 := &dynamodb.GetItemInput{
		TableName: aws.String(store.tableName),
		Key: map[string]types.AttributeValue{
			"shard_id": &types.AttributeValueMemberN{Value: strconv.Itoa(shardId)},
			"sort_key": &types.AttributeValueMemberS{Value: newTimerSortKey1},
		},
	}
	resultNew1, _ := store.client.GetItem(ctx, getInputNew1)
	assert.NotNil(t, resultNew1.Item, "new-timer-1 should be inserted")

	newTimerSortKey2 := databases.FormatExecuteAtWithUuid(newTimer2.ExecuteAt, newTimer2.TimerUuid.String())
	getInputNew2 := &dynamodb.GetItemInput{
		TableName: aws.String(store.tableName),
		Key: map[string]types.AttributeValue{
			"shard_id": &types.AttributeValueMemberN{Value: strconv.Itoa(shardId)},
			"sort_key": &types.AttributeValueMemberS{Value: newTimerSortKey2},
		},
	}
	resultNew2, _ := store.client.GetItem(ctx, getInputNew2)
	assert.NotNil(t, resultNew2.Item, "new-timer-2 should be inserted")

	// Verify new timer data is correct
	assert.Equal(t, "https://example.com/new-callback1", resultNew1.Item["timer_callback_url"].(*types.AttributeValueMemberS).Value)
	assert.Contains(t, resultNew1.Item["timer_payload"].(*types.AttributeValueMemberS).Value, "value1")
	assert.Equal(t, "45", resultNew1.Item["timer_callback_timeout_seconds"].(*types.AttributeValueMemberN).Value)

	assert.Equal(t, "https://example.com/new-callback2", resultNew2.Item["timer_callback_url"].(*types.AttributeValueMemberS).Value)
	assert.Contains(t, resultNew2.Item["timer_retry_policy"].(*types.AttributeValueMemberS).Value, "maxAttempts")
	assert.Equal(t, "60", resultNew2.Item["timer_callback_timeout_seconds"].(*types.AttributeValueMemberN).Value)
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
	now := time.Now().UTC().Truncate(time.Millisecond) // DynamoDB stores in UTC with millisecond precision
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
	// DynamoDB implementation returns 0 for conflict version to avoid expensive reads
	assert.Equal(t, int64(0), deleteErr.ConflictShardVersion)

	// Verify original timer still exists (operation was rolled back)
	timerSortKey := databases.FormatExecuteAtWithUuid(timer.ExecuteAt, timer.TimerUuid.String())
	getInput := &dynamodb.GetItemInput{
		TableName: aws.String(store.tableName),
		Key: map[string]types.AttributeValue{
			"shard_id": &types.AttributeValueMemberN{Value: strconv.Itoa(shardId)},
			"sort_key": &types.AttributeValueMemberS{Value: timerSortKey},
		},
	}
	result, _ := store.client.GetItem(ctx, getInput)
	assert.NotNil(t, result.Item, "original timer should still exist due to rollback")

	// Verify new timer was NOT inserted (operation was rolled back)
	newTimerSortKey := databases.FormatExecuteAtWithUuid(newTimer.ExecuteAt, newTimer.TimerUuid.String())
	getInputNew := &dynamodb.GetItemInput{
		TableName: aws.String(store.tableName),
		Key: map[string]types.AttributeValue{
			"shard_id": &types.AttributeValueMemberN{Value: strconv.Itoa(shardId)},
			"sort_key": &types.AttributeValueMemberS{Value: newTimerSortKey},
		},
	}
	resultNew, _ := store.client.GetItem(ctx, getInputNew)
	assert.Nil(t, resultNew.Item, "new timer should NOT be inserted due to rollback")
}
