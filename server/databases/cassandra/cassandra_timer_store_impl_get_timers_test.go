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

func TestGetTimersUpToTimestamp_Basic(t *testing.T) {
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

	// Create multiple timers with different execution times
	baseTime := time.Now().Truncate(time.Second) // Remove nanoseconds for consistent comparison
	timers := []*databases.DbTimer{
		{
			Id:                     "timer-1",
			TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-1"),
			Namespace:              namespace,
			ExecuteAt:              baseTime.Add(1 * time.Minute),
			CallbackUrl:            "https://example.com/callback1",
			CallbackTimeoutSeconds: 30,
			CreatedAt:              baseTime,
		},
		{
			Id:                     "timer-2",
			TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-2"),
			Namespace:              namespace,
			ExecuteAt:              baseTime.Add(2 * time.Minute),
			CallbackUrl:            "https://example.com/callback2",
			CallbackTimeoutSeconds: 30,
			CreatedAt:              baseTime,
		},
		{
			Id:                     "timer-3",
			TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-3"),
			Namespace:              namespace,
			ExecuteAt:              baseTime.Add(3 * time.Minute),
			CallbackUrl:            "https://example.com/callback3",
			CallbackTimeoutSeconds: 30,
			CreatedAt:              baseTime,
		},
	}

	// Insert all timers
	for _, timer := range timers {
		createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
		require.Nil(t, createErr)
	}

	// Test: Get timers up to 2 minutes from base time (should return timer-1 and timer-2)
	request := &databases.RangeGetTimersRequest{
		UpToTimestamp: baseTime.Add(2 * time.Minute),
		Limit:         10,
	}

	response, getErr := store.GetTimersUpToTimestamp(ctx, shardId, request)
	require.Nil(t, getErr)
	require.NotNil(t, response)
	require.Len(t, response.Timers, 2)

	// Verify timers are returned in execution order
	assert.Equal(t, "timer-1", response.Timers[0].Id)
	assert.Equal(t, "timer-2", response.Timers[1].Id)
	assert.True(t, response.Timers[0].ExecuteAt.Before(response.Timers[1].ExecuteAt) ||
		response.Timers[0].ExecuteAt.Equal(response.Timers[1].ExecuteAt))
}

func TestGetTimersUpToTimestamp_WithLimit(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Create shard record
	ownerAddr := "owner-1"
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
	require.Nil(t, err)

	// Create 5 timers
	baseTime := time.Now().Truncate(time.Second)
	for i := 1; i <= 5; i++ {
		timer := &databases.DbTimer{
			Id:                     fmt.Sprintf("timer-%d", i),
			TimerUuid:              databases.GenerateTimerUUID(namespace, fmt.Sprintf("timer-%d", i)),
			Namespace:              namespace,
			ExecuteAt:              baseTime.Add(time.Duration(i) * time.Minute),
			CallbackUrl:            fmt.Sprintf("https://example.com/callback%d", i),
			CallbackTimeoutSeconds: 30,
			CreatedAt:              baseTime,
		}
		createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
		require.Nil(t, createErr)
	}

	// Test: Get timers with limit of 3
	request := &databases.RangeGetTimersRequest{
		UpToTimestamp: baseTime.Add(10 * time.Minute), // All timers should be within this range
		Limit:         3,
	}

	response, getErr := store.GetTimersUpToTimestamp(ctx, shardId, request)
	require.Nil(t, getErr)
	require.NotNil(t, response)
	assert.Len(t, response.Timers, 3)

	// Verify first 3 timers are returned in order
	assert.Equal(t, "timer-1", response.Timers[0].Id)
	assert.Equal(t, "timer-2", response.Timers[1].Id)
	assert.Equal(t, "timer-3", response.Timers[2].Id)
}

func TestGetTimersUpToTimestamp_WithPayloadAndRetryPolicy(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Create shard record
	ownerAddr := "owner-1"
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
	require.Nil(t, err)

	// Create timer with payload and retry policy
	baseTime := time.Now().Truncate(time.Second)
	payload := map[string]interface{}{
		"message": "test payload",
		"number":  42,
	}
	retryPolicy := map[string]interface{}{
		"maxAttempts":       3,
		"backoffMultiplier": 2.0,
	}

	timer := &databases.DbTimer{
		Id:                     "timer-with-data",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-with-data"),
		Namespace:              namespace,
		ExecuteAt:              baseTime.Add(1 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		Payload:                payload,
		RetryPolicy:            retryPolicy,
		CallbackTimeoutSeconds: 30,
		CreatedAt:              baseTime,
	}

	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	require.Nil(t, createErr)

	// Retrieve the timer
	request := &databases.RangeGetTimersRequest{
		UpToTimestamp: baseTime.Add(5 * time.Minute),
		Limit:         10,
	}

	response, getErr := store.GetTimersUpToTimestamp(ctx, shardId, request)
	require.Nil(t, getErr)
	require.NotNil(t, response)
	require.Len(t, response.Timers, 1)

	retrievedTimer := response.Timers[0]
	assert.Equal(t, "timer-with-data", retrievedTimer.Id)
	assert.Equal(t, timer.TimerUuid, retrievedTimer.TimerUuid)
	assert.Equal(t, namespace, retrievedTimer.Namespace)
	assert.True(t, timer.ExecuteAt.Equal(retrievedTimer.ExecuteAt))
	assert.Equal(t, "https://example.com/callback", retrievedTimer.CallbackUrl)

	// Verify payload
	assert.NotNil(t, retrievedTimer.Payload)
	payloadMap, ok := retrievedTimer.Payload.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "test payload", payloadMap["message"])
	assert.Equal(t, float64(42), payloadMap["number"]) // JSON unmarshals numbers as float64

	// Verify retry policy
	assert.NotNil(t, retrievedTimer.RetryPolicy)
	retryMap, ok := retrievedTimer.RetryPolicy.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, float64(3), retryMap["maxAttempts"])
	assert.Equal(t, 2.0, retryMap["backoffMultiplier"])
}

func TestGetTimersUpToTimestamp_EmptyResult(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1

	// Create shard record but no timers
	ownerAddr := "owner-1"
	_, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
	require.Nil(t, err)

	// Query for timers
	request := &databases.RangeGetTimersRequest{
		UpToTimestamp: time.Now().Add(5 * time.Minute),
		Limit:         10,
	}

	response, getErr := store.GetTimersUpToTimestamp(ctx, shardId, request)
	require.Nil(t, getErr)
	require.NotNil(t, response)
	assert.Len(t, response.Timers, 0)
}

func TestGetTimersUpToTimestamp_TimeOrdering(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Create shard record
	ownerAddr := "owner-1"
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
	require.Nil(t, err)

	// Create timers in non-sequential order
	baseTime := time.Now().Truncate(time.Second)
	timers := []*databases.DbTimer{
		{
			Id:                     "timer-3",
			TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-3"),
			Namespace:              namespace,
			ExecuteAt:              baseTime.Add(3 * time.Minute),
			CallbackUrl:            "https://example.com/callback3",
			CallbackTimeoutSeconds: 30,
			CreatedAt:              baseTime,
		},
		{
			Id:                     "timer-1",
			TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-1"),
			Namespace:              namespace,
			ExecuteAt:              baseTime.Add(1 * time.Minute),
			CallbackUrl:            "https://example.com/callback1",
			CallbackTimeoutSeconds: 30,
			CreatedAt:              baseTime,
		},
		{
			Id:                     "timer-2",
			TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-2"),
			Namespace:              namespace,
			ExecuteAt:              baseTime.Add(2 * time.Minute),
			CallbackUrl:            "https://example.com/callback2",
			CallbackTimeoutSeconds: 30,
			CreatedAt:              baseTime,
		},
	}

	// Insert timers in non-sequential order
	for _, timer := range timers {
		createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
		require.Nil(t, createErr)
	}

	// Query all timers
	request := &databases.RangeGetTimersRequest{
		UpToTimestamp: baseTime.Add(5 * time.Minute),
		Limit:         10,
	}

	response, getErr := store.GetTimersUpToTimestamp(ctx, shardId, request)
	require.Nil(t, getErr)
	require.NotNil(t, response)
	require.Len(t, response.Timers, 3)

	// Verify timers are returned in execution order
	assert.Equal(t, "timer-1", response.Timers[0].Id)
	assert.Equal(t, "timer-2", response.Timers[1].Id)
	assert.Equal(t, "timer-3", response.Timers[2].Id)

	// Verify time ordering
	for i := 1; i < len(response.Timers); i++ {
		assert.True(t, response.Timers[i-1].ExecuteAt.Before(response.Timers[i].ExecuteAt) ||
			response.Timers[i-1].ExecuteAt.Equal(response.Timers[i].ExecuteAt))
	}
}
