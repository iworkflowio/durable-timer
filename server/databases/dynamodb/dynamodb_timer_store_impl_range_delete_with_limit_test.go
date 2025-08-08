package dynamodb

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/iworkflowio/durable-timer/databases"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRangeDeleteWithLimit_Basic(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test-namespace"
	now := time.Now().UTC().Truncate(time.Millisecond)

	// Create 5 timers at different times
	for i := 0; i < 5; i++ {
		timer := &databases.DbTimer{
			TimerUuid:   databases.GenerateTimerUUID(namespace, fmt.Sprintf("timer-%d", i)),
			Namespace:   namespace,
			Id:          fmt.Sprintf("timer-%d", i),
			ExecuteAt:   now.Add(time.Duration(i) * time.Minute),
			CallbackUrl: fmt.Sprintf("http://test-%d.com", i),
			Attempts:    int32(i),
		}

		createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
		require.Nil(t, createErr)
	}

	// Delete with limit 3 - should delete only first 3 timers (DynamoDB now respects limit)
	deleteRequest := &databases.RangeDeleteTimersRequest{
		StartTimestamp: now,
		StartTimeUuid:  databases.ZeroUUID,
		EndTimestamp:   now.Add(10 * time.Minute),
		EndTimeUuid:    databases.GenerateTimerUUID("max", "max"),
	}

	response, deleteErr := store.RangeDeleteWithLimit(ctx, shardId, deleteRequest, 3)
	assert.Nil(t, deleteErr)
	require.NotNil(t, response)
	assert.Equal(t, 3, response.DeletedCount) // Only 3 timers should be deleted

	// Verify only first 3 timers are deleted (DynamoDB now respects limit)
	for i := 0; i < 5; i++ {
		timer, getErr := store.GetTimer(ctx, shardId, namespace, fmt.Sprintf("timer-%d", i))

		if i < 3 {
			assert.NotNil(t, getErr, fmt.Sprintf("Timer %d should be deleted", i))
			assert.True(t, databases.IsDbErrorNotExists(getErr), fmt.Sprintf("Timer %d should not exist", i))
			assert.Nil(t, timer)
		} else {
			assert.Nil(t, getErr, fmt.Sprintf("Timer %d should still exist", i))
			assert.Equal(t, fmt.Sprintf("timer-%d", i), timer.Id)
		}
	}
}

func TestRangeDeleteWithLimit_NoLimit(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 2
	namespace := "test-namespace"
	now := time.Now().UTC().Truncate(time.Millisecond)

	// Create 3 timers
	for i := 0; i < 3; i++ {
		timer := &databases.DbTimer{
			TimerUuid:   databases.GenerateTimerUUID(namespace, fmt.Sprintf("timer-%d", i)),
			Namespace:   namespace,
			Id:          fmt.Sprintf("timer-%d", i),
			ExecuteAt:   now.Add(time.Duration(i) * time.Minute),
			CallbackUrl: fmt.Sprintf("http://test-%d.com", i),
			Attempts:    int32(i),
		}

		createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
		require.Nil(t, createErr)
	}

	// Delete with limit 0 (no limit) - should delete all timers in range
	deleteRequest := &databases.RangeDeleteTimersRequest{
		StartTimestamp: now,
		StartTimeUuid:  databases.ZeroUUID,
		EndTimestamp:   now.Add(10 * time.Minute),
		EndTimeUuid:    databases.GenerateTimerUUID("max", "max"),
	}

	response, deleteErr := store.RangeDeleteWithLimit(ctx, shardId, deleteRequest, 0)
	assert.Nil(t, deleteErr)
	require.NotNil(t, response)
	assert.Equal(t, 3, response.DeletedCount)

	// Verify all timers are deleted
	for i := 0; i < 3; i++ {
		timer, getErr := store.GetTimer(ctx, shardId, namespace, fmt.Sprintf("timer-%d", i))
		assert.NotNil(t, getErr)
		assert.True(t, databases.IsDbErrorNotExists(getErr))
		assert.Nil(t, timer)
	}
}

func TestRangeDeleteWithLimit_EmptyRange(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 3
	namespace := "test-namespace"
	now := time.Now().UTC().Truncate(time.Millisecond)

	// Create a timer at 20 minutes from now
	timer := &databases.DbTimer{
		TimerUuid:   databases.GenerateTimerUUID(namespace, "timer-far"),
		Namespace:   namespace,
		Id:          "timer-far",
		ExecuteAt:   now.Add(20 * time.Minute),
		CallbackUrl: "http://test-far.com",
		Attempts:    0,
	}

	createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
	require.Nil(t, createErr)

	// Delete in range that doesn't include the timer (timer is at 20 minutes)
	deleteRequest := &databases.RangeDeleteTimersRequest{
		StartTimestamp: now.Add(2 * time.Minute),
		StartTimeUuid:  databases.ZeroUUID,
		EndTimestamp:   now.Add(10 * time.Minute), // Timer is at 20 minutes, so outside range
		EndTimeUuid:    databases.GenerateTimerUUID("max", "max"),
	}

	response, deleteErr := store.RangeDeleteWithLimit(ctx, shardId, deleteRequest, 5)
	assert.Nil(t, deleteErr)
	require.NotNil(t, response)
	assert.Equal(t, 0, response.DeletedCount)

	// Verify timer still exists
	retrievedTimer, getErr := store.GetTimer(ctx, shardId, namespace, "timer-far")
	assert.Nil(t, getErr)
	assert.Equal(t, "timer-far", retrievedTimer.Id)
}

func TestRangeDeleteWithLimit_ExactLimit(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 4
	namespace := "test-namespace"
	now := time.Now().UTC().Truncate(time.Millisecond)

	// Create 3 timers
	for i := 0; i < 3; i++ {
		timer := &databases.DbTimer{
			TimerUuid:   databases.GenerateTimerUUID(namespace, fmt.Sprintf("timer-%d", i)),
			Namespace:   namespace,
			Id:          fmt.Sprintf("timer-%d", i),
			ExecuteAt:   now.Add(time.Duration(i) * time.Minute),
			CallbackUrl: fmt.Sprintf("http://test-%d.com", i),
			Attempts:    int32(i),
		}

		createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
		require.Nil(t, createErr)
	}

	// Delete with limit exactly equal to number of timers (limit ignored for DynamoDB)
	deleteRequest := &databases.RangeDeleteTimersRequest{
		StartTimestamp: now,
		StartTimeUuid:  databases.ZeroUUID,
		EndTimestamp:   now.Add(10 * time.Minute),
		EndTimeUuid:    databases.GenerateTimerUUID("max", "max"),
	}

	response, deleteErr := store.RangeDeleteWithLimit(ctx, shardId, deleteRequest, 3)
	assert.Nil(t, deleteErr)
	require.NotNil(t, response)
	assert.Equal(t, 3, response.DeletedCount)

	// Verify all timers are deleted
	for i := 0; i < 3; i++ {
		timer, getErr := store.GetTimer(ctx, shardId, namespace, fmt.Sprintf("timer-%d", i))
		assert.NotNil(t, getErr)
		assert.True(t, databases.IsDbErrorNotExists(getErr))
		assert.Nil(t, timer)
	}
}

func TestRangeDeleteWithLimit_LimitExceedsAvailable(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 5
	namespace := "test-namespace"
	now := time.Now().UTC().Truncate(time.Millisecond)

	// Create 2 timers
	for i := 0; i < 2; i++ {
		timer := &databases.DbTimer{
			TimerUuid:   databases.GenerateTimerUUID(namespace, fmt.Sprintf("timer-%d", i)),
			Namespace:   namespace,
			Id:          fmt.Sprintf("timer-%d", i),
			ExecuteAt:   now.Add(time.Duration(i) * time.Minute),
			CallbackUrl: fmt.Sprintf("http://test-%d.com", i),
			Attempts:    int32(i),
		}

		createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
		require.Nil(t, createErr)
	}

	// Delete with limit higher than available timers (limit ignored for DynamoDB)
	deleteRequest := &databases.RangeDeleteTimersRequest{
		StartTimestamp: now,
		StartTimeUuid:  databases.ZeroUUID,
		EndTimestamp:   now.Add(10 * time.Minute),
		EndTimeUuid:    databases.GenerateTimerUUID("max", "max"),
	}

	response, deleteErr := store.RangeDeleteWithLimit(ctx, shardId, deleteRequest, 5)
	assert.Nil(t, deleteErr)
	require.NotNil(t, response)
	assert.Equal(t, 2, response.DeletedCount) // Should delete all available (2)

	// Verify all available timers are deleted
	for i := 0; i < 2; i++ {
		timer, getErr := store.GetTimer(ctx, shardId, namespace, fmt.Sprintf("timer-%d", i))
		assert.NotNil(t, getErr)
		assert.True(t, databases.IsDbErrorNotExists(getErr))
		assert.Nil(t, timer)
	}
}

func TestRangeDeleteWithLimit_WithPayload(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 6
	namespace := "test-namespace"
	now := time.Now().UTC().Truncate(time.Millisecond)

	// Create 4 timers with payloads
	for i := 0; i < 4; i++ {
		payload := map[string]interface{}{
			"key": fmt.Sprintf("payload-%d", i),
		}

		timer := &databases.DbTimer{
			TimerUuid:   databases.GenerateTimerUUID(namespace, fmt.Sprintf("timer-payload-%d", i)),
			Namespace:   namespace,
			Id:          fmt.Sprintf("timer-payload-%d", i),
			ExecuteAt:   now.Add(time.Duration(i) * time.Minute),
			CallbackUrl: fmt.Sprintf("http://test-%d.com", i),
			Payload:     payload,
			Attempts:    int32(i),
		}

		createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
		require.Nil(t, createErr)
	}

	// Delete with limit 2 (DynamoDB now respects limit)
	deleteRequest := &databases.RangeDeleteTimersRequest{
		StartTimestamp: now,
		StartTimeUuid:  databases.ZeroUUID,
		EndTimestamp:   now.Add(10 * time.Minute),
		EndTimeUuid:    databases.GenerateTimerUUID("max", "max"),
	}

	response, deleteErr := store.RangeDeleteWithLimit(ctx, shardId, deleteRequest, 2)
	assert.Nil(t, deleteErr)
	require.NotNil(t, response)
	assert.Equal(t, 2, response.DeletedCount) // Only 2 timers should be deleted

	// Verify first 2 timers are deleted, last 2 remain (DynamoDB now respects limit)
	for i := 0; i < 4; i++ {
		timer, getErr := store.GetTimer(ctx, shardId, namespace, fmt.Sprintf("timer-payload-%d", i))

		if i < 2 {
			assert.NotNil(t, getErr, fmt.Sprintf("Timer %d should be deleted", i))
			assert.True(t, databases.IsDbErrorNotExists(getErr))
			assert.Nil(t, timer)
		} else {
			assert.Nil(t, getErr, fmt.Sprintf("Timer %d should still exist", i))
			assert.Equal(t, fmt.Sprintf("timer-payload-%d", i), timer.Id)

			// Verify payload is preserved
			if timer.Payload != nil {
				payloadMap, ok := timer.Payload.(map[string]interface{})
				require.True(t, ok)
				assert.Equal(t, fmt.Sprintf("payload-%d", i), payloadMap["key"])
			}
		}
	}
}

func TestRangeDeleteWithLimit_TimeOrdering(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 7
	namespace := "test-namespace"
	now := time.Now().UTC().Truncate(time.Millisecond)

	// Create 4 timers with different execution times (not in creation order)
	timers := []struct {
		id        string
		executeAt time.Time
	}{
		{"timer-c", now.Add(3 * time.Minute)}, // Third
		{"timer-a", now.Add(1 * time.Minute)}, // First
		{"timer-d", now.Add(4 * time.Minute)}, // Fourth
		{"timer-b", now.Add(2 * time.Minute)}, // Second
	}

	for _, timerInfo := range timers {
		timer := &databases.DbTimer{
			TimerUuid:   databases.GenerateTimerUUID(namespace, timerInfo.id),
			Namespace:   namespace,
			Id:          timerInfo.id,
			ExecuteAt:   timerInfo.executeAt,
			CallbackUrl: fmt.Sprintf("http://%s.com", timerInfo.id),
			Attempts:    0,
		}

		createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
		require.Nil(t, createErr)
	}

	// Delete with limit 2 - should delete earliest 2 by ExecuteAt (timer-a, timer-b)
	deleteRequest := &databases.RangeDeleteTimersRequest{
		StartTimestamp: now,
		StartTimeUuid:  databases.ZeroUUID,
		EndTimestamp:   now.Add(10 * time.Minute),
		EndTimeUuid:    databases.GenerateTimerUUID("max", "max"),
	}

	response, deleteErr := store.RangeDeleteWithLimit(ctx, shardId, deleteRequest, 2)
	assert.Nil(t, deleteErr)
	require.NotNil(t, response)
	assert.Equal(t, 2, response.DeletedCount) // Only 2 timers should be deleted

	// Verify timer-a and timer-b are deleted (earliest ExecuteAt times)
	expectedDeleted := []string{"timer-a", "timer-b"}
	expectedRemaining := []string{"timer-c", "timer-d"}

	for _, timerId := range expectedDeleted {
		timer, getErr := store.GetTimer(ctx, shardId, namespace, timerId)
		assert.NotNil(t, getErr, fmt.Sprintf("%s should be deleted", timerId))
		assert.True(t, databases.IsDbErrorNotExists(getErr))
		assert.Nil(t, timer)
	}

	for _, timerId := range expectedRemaining {
		timer, getErr := store.GetTimer(ctx, shardId, namespace, timerId)
		assert.Nil(t, getErr, fmt.Sprintf("%s should remain", timerId))
		assert.Equal(t, timerId, timer.Id)
	}
}

func TestRangeDeleteWithLimit_PreciseRange(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 8
	namespace := "test-namespace"
	now := time.Now().UTC().Truncate(time.Millisecond)

	// Create 6 timers at 1-minute intervals
	for i := 0; i < 6; i++ {
		timer := &databases.DbTimer{
			TimerUuid:   databases.GenerateTimerUUID(namespace, fmt.Sprintf("timer-precise-%d", i)),
			Namespace:   namespace,
			Id:          fmt.Sprintf("timer-precise-%d", i),
			ExecuteAt:   now.Add(time.Duration(i) * time.Minute),
			CallbackUrl: fmt.Sprintf("http://test-%d.com", i),
			Attempts:    int32(i),
		}

		createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
		require.Nil(t, createErr)
	}

	// Delete in a precise range (timers 1, 2, 3, 4) with limit 2 (DynamoDB now respects limit)
	deleteRequest := &databases.RangeDeleteTimersRequest{
		StartTimestamp: now.Add(30 * time.Second), // Before timer-1 (1 minute)
		StartTimeUuid:  databases.ZeroUUID,
		EndTimestamp:   now.Add(4*time.Minute + 30*time.Second), // After timer-4 (4 minutes)
		EndTimeUuid:    databases.GenerateTimerUUID("max", "max"),
	}

	response, deleteErr := store.RangeDeleteWithLimit(ctx, shardId, deleteRequest, 2)
	assert.Nil(t, deleteErr)
	require.NotNil(t, response)
	assert.Equal(t, 2, response.DeletedCount) // Only 2 timers should be deleted

	// Verify timers 1, 2 are deleted (first 2 in range), timers 0, 3, 4, 5 remain
	expectedStates := map[int]bool{
		0: true,  // should exist (before range)
		1: false, // should be deleted (first in range, within limit)
		2: false, // should be deleted (second in range, within limit)
		3: true,  // should exist (third in range, beyond limit)
		4: true,  // should exist (fourth in range, beyond limit)
		5: true,  // should exist (after range)
	}

	for i, shouldExist := range expectedStates {
		timer, getErr := store.GetTimer(ctx, shardId, namespace, fmt.Sprintf("timer-precise-%d", i))

		if shouldExist {
			assert.Nil(t, getErr, fmt.Sprintf("Timer %d should exist", i))
			assert.Equal(t, fmt.Sprintf("timer-precise-%d", i), timer.Id)
		} else {
			assert.NotNil(t, getErr, fmt.Sprintf("Timer %d should be deleted", i))
			assert.True(t, databases.IsDbErrorNotExists(getErr))
		}
	}
}
