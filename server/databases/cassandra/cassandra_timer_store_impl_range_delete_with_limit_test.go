package cassandra

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
	namespace := "test_namespace"

	// First, create a shard record
	ownerAddr := "owner-1"
	_, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
	require.Nil(t, err)

	// Create 5 timers to test limit functionality
	now := time.Now().UTC().Truncate(time.Millisecond)
	var timers []*databases.DbTimer
	for i := 0; i < 5; i++ {
		timer := &databases.DbTimer{
			Id:                     fmt.Sprintf("timer-%d", i),
			TimerUuid:              databases.GenerateTimerUUID(namespace, fmt.Sprintf("timer-%d", i)),
			Namespace:              namespace,
			ExecuteAt:              now.Add(time.Duration(i+1) * time.Minute),
			CallbackUrl:            fmt.Sprintf("https://example.com/callback-%d", i),
			CallbackTimeoutSeconds: 30,
			CreatedAt:              now,
		}
		timers = append(timers, timer)

		createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
		require.Nil(t, createErr)
	}

	// Delete with limit 3 - should delete all timers in range (limit ignored for Cassandra)
	deleteRequest := &databases.RangeDeleteTimersRequest{
		StartTimestamp: now,
		StartTimeUuid:  databases.ZeroUUID,
		EndTimestamp:   now.Add(10 * time.Minute),
		EndTimeUuid:    databases.GenerateTimerUUID("max", "max"),
	}

	response, deleteErr := store.RangeDeleteWithLimit(ctx, shardId, deleteRequest, 3)
	assert.Nil(t, deleteErr)
	require.NotNil(t, response)
	assert.Equal(t, 0, response.DeletedCount) // Count not available in Cassandra

	// Verify all timers in range are deleted (limit ignored for Cassandra)
	for i := 0; i < 5; i++ {
		var count int
		countQuery := `SELECT COUNT(*) FROM timers WHERE shard_id = ? AND row_type = ? AND timer_namespace = ? AND timer_id = ? ALLOW FILTERING`
		scanErr := store.session.Query(countQuery, shardId, databases.RowTypeTimer, namespace, fmt.Sprintf("timer-%d", i)).Scan(&count)
		require.NoError(t, scanErr)

		// All timers should be deleted since they're all in the range
		assert.Equal(t, 0, count, "timer-%d should be deleted", i)
	}
}

func TestRangeDeleteWithLimit_NoLimit(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 2
	namespace := "test_namespace"

	// First, create a shard record
	ownerAddr := "owner-1"
	_, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
	require.Nil(t, err)

	// Create 3 timers
	now := time.Now().UTC().Truncate(time.Millisecond)
	for i := 0; i < 3; i++ {
		timer := &databases.DbTimer{
			Id:                     fmt.Sprintf("timer-no-limit-%d", i),
			TimerUuid:              databases.GenerateTimerUUID(namespace, fmt.Sprintf("timer-no-limit-%d", i)),
			Namespace:              namespace,
			ExecuteAt:              now.Add(time.Duration(i+1) * time.Minute),
			CallbackUrl:            fmt.Sprintf("https://example.com/callback-%d", i),
			CallbackTimeoutSeconds: 30,
			CreatedAt:              now,
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

	// Verify all timers are deleted
	for i := 0; i < 3; i++ {
		var count int
		countQuery := `SELECT COUNT(*) FROM timers WHERE shard_id = ? AND row_type = ? AND timer_namespace = ? AND timer_id = ? ALLOW FILTERING`
		scanErr := store.session.Query(countQuery, shardId, databases.RowTypeTimer, namespace, fmt.Sprintf("timer-no-limit-%d", i)).Scan(&count)
		require.NoError(t, scanErr)
		assert.Equal(t, 0, count, "timer-no-limit-%d should be deleted", i)
	}
}

func TestRangeDeleteWithLimit_EmptyRange(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 3
	namespace := "test_namespace"

	// First, create a shard record
	ownerAddr := "owner-1"
	_, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
	require.Nil(t, err)

	// Create a timer outside the delete range
	now := time.Now().UTC().Truncate(time.Millisecond)
	timer := &databases.DbTimer{
		Id:                     "timer-outside-range",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-outside-range"),
		Namespace:              namespace,
		ExecuteAt:              now.Add(20 * time.Minute), // Outside delete range
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              now,
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
	var count int
	countQuery := `SELECT COUNT(*) FROM timers WHERE shard_id = ? AND row_type = ? AND timer_namespace = ? AND timer_id = ? ALLOW FILTERING`
	scanErr := store.session.Query(countQuery, shardId, databases.RowTypeTimer, namespace, "timer-outside-range").Scan(&count)
	require.NoError(t, scanErr)
	assert.Equal(t, 1, count, "timer-outside-range should still exist")
}

func TestRangeDeleteWithLimit_ExactLimit(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 4
	namespace := "test_namespace"

	// First, create a shard record
	ownerAddr := "owner-1"
	_, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
	require.Nil(t, err)

	// Create exactly 3 timers
	now := time.Now().UTC().Truncate(time.Millisecond)
	for i := 0; i < 3; i++ {
		timer := &databases.DbTimer{
			Id:                     fmt.Sprintf("timer-exact-%d", i),
			TimerUuid:              databases.GenerateTimerUUID(namespace, fmt.Sprintf("timer-exact-%d", i)),
			Namespace:              namespace,
			ExecuteAt:              now.Add(time.Duration(i+1) * time.Minute),
			CallbackUrl:            fmt.Sprintf("https://example.com/callback-%d", i),
			CallbackTimeoutSeconds: 30,
			CreatedAt:              now,
		}

		createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
		require.Nil(t, createErr)
	}

	// Delete with limit exactly equal to number of timers (limit ignored for Cassandra)
	deleteRequest := &databases.RangeDeleteTimersRequest{
		StartTimestamp: now,
		StartTimeUuid:  databases.ZeroUUID,
		EndTimestamp:   now.Add(10 * time.Minute),
		EndTimeUuid:    databases.GenerateTimerUUID("max", "max"),

	}

	response, deleteErr := store.RangeDeleteWithLimit(ctx, shardId, deleteRequest, 3)
	assert.Nil(t, deleteErr)
	require.NotNil(t, response)

	// Verify all timers are deleted
	for i := 0; i < 3; i++ {
		var count int
		countQuery := `SELECT COUNT(*) FROM timers WHERE shard_id = ? AND row_type = ? AND timer_namespace = ? AND timer_id = ? ALLOW FILTERING`
		scanErr := store.session.Query(countQuery, shardId, databases.RowTypeTimer, namespace, fmt.Sprintf("timer-exact-%d", i)).Scan(&count)
		require.NoError(t, scanErr)
		assert.Equal(t, 0, count, "timer-exact-%d should be deleted", i)
	}
}

func TestRangeDeleteWithLimit_LimitExceedsAvailable(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 5
	namespace := "test_namespace"

	// First, create a shard record
	ownerAddr := "owner-1"
	_, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
	require.Nil(t, err)

	// Create only 2 timers
	now := time.Now().UTC().Truncate(time.Millisecond)
	for i := 0; i < 2; i++ {
		timer := &databases.DbTimer{
			Id:                     fmt.Sprintf("timer-exceed-%d", i),
			TimerUuid:              databases.GenerateTimerUUID(namespace, fmt.Sprintf("timer-exceed-%d", i)),
			Namespace:              namespace,
			ExecuteAt:              now.Add(time.Duration(i+1) * time.Minute),
			CallbackUrl:            fmt.Sprintf("https://example.com/callback-%d", i),
			CallbackTimeoutSeconds: 30,
			CreatedAt:              now,
		}

		createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
		require.Nil(t, createErr)
	}

	// Delete with limit higher than available timers (limit ignored for Cassandra)
	deleteRequest := &databases.RangeDeleteTimersRequest{
		StartTimestamp: now,
		StartTimeUuid:  databases.ZeroUUID,
		EndTimestamp:   now.Add(10 * time.Minute),
		EndTimeUuid:    databases.GenerateTimerUUID("max", "max"),

	}

	response, deleteErr := store.RangeDeleteWithLimit(ctx, shardId, deleteRequest, 5)
	assert.Nil(t, deleteErr)
	require.NotNil(t, response)

	// Verify all available timers are deleted
	for i := 0; i < 2; i++ {
		var count int
		countQuery := `SELECT COUNT(*) FROM timers WHERE shard_id = ? AND row_type = ? AND timer_namespace = ? AND timer_id = ? ALLOW FILTERING`
		scanErr := store.session.Query(countQuery, shardId, databases.RowTypeTimer, namespace, fmt.Sprintf("timer-exceed-%d", i)).Scan(&count)
		require.NoError(t, scanErr)
		assert.Equal(t, 0, count, "timer-exceed-%d should be deleted", i)
	}
}

func TestRangeDeleteWithLimit_WithPayload(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 6
	namespace := "test_namespace"

	// First, create a shard record
	ownerAddr := "owner-1"
	_, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
	require.Nil(t, err)

	// Create timers with different payloads
	now := time.Now().UTC().Truncate(time.Millisecond)
	for i := 0; i < 4; i++ {
		timer := &databases.DbTimer{
			Id:                     fmt.Sprintf("timer-payload-%d", i),
			TimerUuid:              databases.GenerateTimerUUID(namespace, fmt.Sprintf("timer-payload-%d", i)),
			Namespace:              namespace,
			ExecuteAt:              now.Add(time.Duration(i+1) * time.Minute),
			CallbackUrl:            fmt.Sprintf("https://example.com/callback-%d", i),
			CallbackTimeoutSeconds: 30,
			CreatedAt:              now,
			Payload:                map[string]interface{}{"index": i, "data": fmt.Sprintf("payload-%d", i)},
		}

		createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
		require.Nil(t, createErr)
	}

	// Delete with limit 2 (limit ignored for Cassandra)
	deleteRequest := &databases.RangeDeleteTimersRequest{
		StartTimestamp: now,
		StartTimeUuid:  databases.ZeroUUID,
		EndTimestamp:   now.Add(10 * time.Minute),
		EndTimeUuid:    databases.GenerateTimerUUID("max", "max"),

	}

	response, deleteErr := store.RangeDeleteWithLimit(ctx, shardId, deleteRequest, 2)
	assert.Nil(t, deleteErr)
	require.NotNil(t, response)

	// Verify all timers in range are deleted (limit ignored for Cassandra)
	for i := 0; i < 4; i++ {
		var count int
		countQuery := `SELECT COUNT(*) FROM timers WHERE shard_id = ? AND row_type = ? AND timer_namespace = ? AND timer_id = ? ALLOW FILTERING`
		scanErr := store.session.Query(countQuery, shardId, databases.RowTypeTimer, namespace, fmt.Sprintf("timer-payload-%d", i)).Scan(&count)
		require.NoError(t, scanErr)

		// All timers should be deleted since they're all in the range
		assert.Equal(t, 0, count, "timer-payload-%d should be deleted", i)
	}
}

func TestRangeDeleteWithLimit_TimeOrdering(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 7
	namespace := "test_namespace"

	// First, create a shard record
	ownerAddr := "owner-1"
	_, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
	require.Nil(t, err)

	// Create timers with non-sequential creation (to test ordering by ExecuteAt)
	now := time.Now().UTC().Truncate(time.Millisecond)
	executeAtTimes := []time.Duration{3 * time.Minute, 1 * time.Minute, 4 * time.Minute, 2 * time.Minute}
	timerIds := []string{"timer-c", "timer-a", "timer-d", "timer-b"}

	for i, execOffset := range executeAtTimes {
		timer := &databases.DbTimer{
			Id:                     timerIds[i],
			TimerUuid:              databases.GenerateTimerUUID(namespace, timerIds[i]),
			Namespace:              namespace,
			ExecuteAt:              now.Add(execOffset),
			CallbackUrl:            fmt.Sprintf("https://example.com/callback-%s", timerIds[i]),
			CallbackTimeoutSeconds: 30,
			CreatedAt:              now,
		}

		createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
		require.Nil(t, createErr)
	}

	// Delete with limit 2 - should delete all timers in range (limit ignored for Cassandra)
	deleteRequest := &databases.RangeDeleteTimersRequest{
		StartTimestamp: now,
		StartTimeUuid:  databases.ZeroUUID,
		EndTimestamp:   now.Add(10 * time.Minute),
		EndTimeUuid:    databases.GenerateTimerUUID("max", "max"),

	}

	response, deleteErr := store.RangeDeleteWithLimit(ctx, shardId, deleteRequest, 2)
	assert.Nil(t, deleteErr)
	require.NotNil(t, response)

	// Verify all timers in range are deleted (limit ignored for Cassandra)
	allTimers := []string{"timer-a", "timer-b", "timer-c", "timer-d"}

	for _, timerId := range allTimers {
		var count int
		countQuery := `SELECT COUNT(*) FROM timers WHERE shard_id = ? AND row_type = ? AND timer_namespace = ? AND timer_id = ? ALLOW FILTERING`
		scanErr := store.session.Query(countQuery, shardId, databases.RowTypeTimer, namespace, timerId).Scan(&count)
		require.NoError(t, scanErr)
		assert.Equal(t, 0, count, "%s should be deleted", timerId)
	}
}

func TestRangeDeleteWithLimit_PreciseRange(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 8
	namespace := "test_namespace"

	// First, create a shard record
	ownerAddr := "owner-1"
	_, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
	require.Nil(t, err)

	// Create timers at precise times
	now := time.Now().UTC().Truncate(time.Millisecond)
	for i := 0; i < 6; i++ {
		timer := &databases.DbTimer{
			Id:                     fmt.Sprintf("timer-precise-%d", i),
			TimerUuid:              databases.GenerateTimerUUID(namespace, fmt.Sprintf("timer-precise-%d", i)),
			Namespace:              namespace,
			ExecuteAt:              now.Add(time.Duration(i+1) * time.Minute),
			CallbackUrl:            fmt.Sprintf("https://example.com/callback-%d", i),
			CallbackTimeoutSeconds: 30,
			CreatedAt:              now,
		}

		createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
		require.Nil(t, createErr)
	}

	// Delete in precise range (timers 1, 2, 3, 4) with limit 2 (limit ignored for Cassandra)
	deleteRequest := &databases.RangeDeleteTimersRequest{
		StartTimestamp: now.Add(2 * time.Minute), // After timer-0
		StartTimeUuid:  databases.ZeroUUID,
		EndTimestamp:   now.Add(5 * time.Minute), // Before timer-5
		EndTimeUuid:    databases.GenerateTimerUUID("max", "max"),

	}

	response, deleteErr := store.RangeDeleteWithLimit(ctx, shardId, deleteRequest, 2)
	assert.Nil(t, deleteErr)
	require.NotNil(t, response)

	// Verify timers 1, 2, 3, 4 are deleted (all in range), timers 0, 5 remain (outside range)
	expectedStates := map[int]bool{
		0: true,  // should exist (before range)
		1: false, // should be deleted (in range)
		2: false, // should be deleted (in range)
		3: false, // should be deleted (in range)
		4: false, // should be deleted (in range)
		5: true,  // should exist (after range)
	}

	for i, shouldExist := range expectedStates {
		var count int
		countQuery := `SELECT COUNT(*) FROM timers WHERE shard_id = ? AND row_type = ? AND timer_namespace = ? AND timer_id = ? ALLOW FILTERING`
		scanErr := store.session.Query(countQuery, shardId, databases.RowTypeTimer, namespace, fmt.Sprintf("timer-precise-%d", i)).Scan(&count)
		require.NoError(t, scanErr)

		if shouldExist {
			assert.Equal(t, 1, count, "timer-precise-%d should exist", i)
		} else {
			assert.Equal(t, 0, count, "timer-precise-%d should be deleted", i)
		}
	}
}
