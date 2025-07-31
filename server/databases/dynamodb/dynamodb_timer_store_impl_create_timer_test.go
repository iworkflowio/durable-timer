package dynamodb

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/iworkflowio/durable-timer/databases"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateTimer_Basic(t *testing.T) {
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

	// Create a timer
	timer := &databases.DbTimer{
		Id:                     "timer-1",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-1"),
		Namespace:              namespace,
		ExecuteAt:              time.Now().Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              time.Now(),
	}

	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	assert.Nil(t, createErr)

	// Verify the timer was inserted by reading it back
	timerSortKey := GetTimerSortKey(namespace, timer.Id)
	key := map[string]types.AttributeValue{
		"shard_id": &types.AttributeValueMemberN{Value: "1"},
		"sort_key": &types.AttributeValueMemberS{Value: timerSortKey},
	}

	getItemInput := &dynamodb.GetItemInput{
		TableName: aws.String(store.tableName),
		Key:       key,
	}

	result, scanErr := store.client.GetItem(ctx, getItemInput)
	require.NoError(t, scanErr)
	require.NotNil(t, result.Item)

	// Verify fields
	assert.Equal(t, timer.Id, result.Item["timer_id"].(*types.AttributeValueMemberS).Value)
	assert.Equal(t, timer.Namespace, result.Item["timer_namespace"].(*types.AttributeValueMemberS).Value)
	assert.Equal(t, timer.CallbackUrl, result.Item["timer_callback_url"].(*types.AttributeValueMemberS).Value)
	assert.Equal(t, "30", result.Item["timer_callback_timeout_seconds"].(*types.AttributeValueMemberN).Value)
	assert.Equal(t, "0", result.Item["timer_attempts"].(*types.AttributeValueMemberN).Value)
}

func TestCreateTimer_WithPayload(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 2
	namespace := "test_namespace"

	// First claim the shard
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, "owner-1", nil)
	require.Nil(t, err)

	// Create timer with complex payload
	payload := map[string]interface{}{
		"userId":   12345,
		"message":  "Hello World",
		"settings": map[string]bool{"notify": true},
		"metadata": []string{"tag1", "tag2"},
	}

	timer := &databases.DbTimer{
		Id:                     "timer-with-payload",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-with-payload"),
		Namespace:              namespace,
		ExecuteAt:              time.Now().Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		Payload:                payload,
		CallbackTimeoutSeconds: 60,
		CreatedAt:              time.Now(),
	}

	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	assert.Nil(t, createErr)

	// Verify payload was serialized correctly
	timerSortKey := GetTimerSortKey(namespace, timer.Id)
	key := map[string]types.AttributeValue{
		"shard_id": &types.AttributeValueMemberN{Value: "2"},
		"sort_key": &types.AttributeValueMemberS{Value: timerSortKey},
	}

	getItemInput := &dynamodb.GetItemInput{
		TableName: aws.String(store.tableName),
		Key:       key,
	}

	result, scanErr := store.client.GetItem(ctx, getItemInput)
	require.NoError(t, scanErr)
	require.NotNil(t, result.Item)

	dbPayload := result.Item["timer_payload"].(*types.AttributeValueMemberS).Value
	assert.Contains(t, dbPayload, "12345")
	assert.Contains(t, dbPayload, "Hello World")
	assert.Contains(t, dbPayload, "notify")
	assert.Contains(t, dbPayload, "tag1")
}

func TestCreateTimer_WithRetryPolicy(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 3
	namespace := "test_namespace"

	// First claim the shard
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, "owner-1", nil)
	require.Nil(t, err)

	// Create timer with retry policy
	retryPolicy := map[string]interface{}{
		"maxAttempts":       3,
		"backoffMultiplier": 2.0,
		"initialInterval":   "30s",
		"maxInterval":       "300s",
	}

	timer := &databases.DbTimer{
		Id:                     "timer-with-retry",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-with-retry"),
		Namespace:              namespace,
		ExecuteAt:              time.Now().Add(15 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		RetryPolicy:            retryPolicy,
		CallbackTimeoutSeconds: 30,
		CreatedAt:              time.Now(),
	}

	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	assert.Nil(t, createErr)

	// Verify retry policy was serialized correctly
	timerSortKey := GetTimerSortKey(namespace, timer.Id)
	key := map[string]types.AttributeValue{
		"shard_id": &types.AttributeValueMemberN{Value: "3"},
		"sort_key": &types.AttributeValueMemberS{Value: timerSortKey},
	}

	getItemInput := &dynamodb.GetItemInput{
		TableName: aws.String(store.tableName),
		Key:       key,
	}

	result, scanErr := store.client.GetItem(ctx, getItemInput)
	require.NoError(t, scanErr)
	require.NotNil(t, result.Item)

	dbRetryPolicy := result.Item["timer_retry_policy"].(*types.AttributeValueMemberS).Value
	assert.Contains(t, dbRetryPolicy, "maxAttempts")
	assert.Contains(t, dbRetryPolicy, "backoffMultiplier")
	assert.Contains(t, dbRetryPolicy, "30s")
}

func TestCreateTimer_ShardVersionMismatch(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 4
	namespace := "test_namespace"

	// First claim the shard
	actualShardVersion, err := store.ClaimShardOwnership(ctx, shardId, "owner-1", nil)
	require.Nil(t, err)

	timer := &databases.DbTimer{
		Id:                     "timer-version-mismatch",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-version-mismatch"),
		Namespace:              namespace,
		ExecuteAt:              time.Now().Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              time.Now(),
	}

	// Try to create timer with wrong shard version
	wrongShardVersion := actualShardVersion + 1
	createErr := store.CreateTimer(ctx, shardId, wrongShardVersion, namespace, timer)

	// Should fail with shard condition error
	assert.NotNil(t, createErr)
	assert.True(t, createErr.ShardConditionFail)
	// Version should be 0 since we don't do expensive reads on conflicts
	assert.Equal(t, int64(0), createErr.ConflictShardVersion)

	// Verify timer was not inserted
	timerSortKey := GetTimerSortKey(namespace, timer.Id)
	key := map[string]types.AttributeValue{
		"shard_id": &types.AttributeValueMemberN{Value: "4"},
		"sort_key": &types.AttributeValueMemberS{Value: timerSortKey},
	}

	getItemInput := &dynamodb.GetItemInput{
		TableName: aws.String(store.tableName),
		Key:       key,
	}

	result, scanErr := store.client.GetItem(ctx, getItemInput)
	require.NoError(t, scanErr)
	assert.Nil(t, result.Item) // Should not exist
}

func TestCreateTimer_ConcurrentCreation(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 5
	namespace := "test_namespace"

	// First claim the shard
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, "owner-1", nil)
	require.Nil(t, err)

	numGoroutines := 10
	var wg sync.WaitGroup
	results := make([]*databases.DbError, numGoroutines)

	// Launch concurrent timer creation
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			timer := &databases.DbTimer{
				Id:                     fmt.Sprintf("concurrent-timer-%d", idx),
				TimerUuid:              databases.GenerateTimerUUID(namespace, fmt.Sprintf("concurrent-timer-%d", idx)),
				Namespace:              namespace,
				ExecuteAt:              time.Now().Add(time.Duration(idx) * time.Minute),
				CallbackUrl:            fmt.Sprintf("https://example.com/callback/%d", idx),
				CallbackTimeoutSeconds: 30,
				CreatedAt:              time.Now(),
			}
			results[idx] = store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
		}(i)
	}

	wg.Wait()

	// All should succeed since we're using the correct shard version
	successCount := 0
	for i, result := range results {
		if result == nil {
			successCount++
		} else {
			t.Logf("Timer creation %d failed: %v", i, result.CustomMessage)
		}
	}

	assert.Equal(t, numGoroutines, successCount, "All concurrent timer creations should succeed")

	// Verify all timers were created by scanning the table
	scanInput := &dynamodb.ScanInput{
		TableName:        aws.String(store.tableName),
		FilterExpression: aws.String("shard_id = :shard_id AND begins_with(sort_key, :timer_prefix)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":shard_id":     &types.AttributeValueMemberN{Value: "5"},
			":timer_prefix": &types.AttributeValueMemberS{Value: timerSortKeyPrefix},
		},
	}

	scanResult, scanErr := store.client.Scan(ctx, scanInput)
	require.NoError(t, scanErr)
	assert.Equal(t, numGoroutines, int(scanResult.Count))
}

func TestCreateTimer_NoShardRecord(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 6
	namespace := "test_namespace"

	// Don't claim the shard first - no shard record exists
	timer := &databases.DbTimer{
		Id:                     "timer-no-shard",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-no-shard"),
		Namespace:              namespace,
		ExecuteAt:              time.Now().Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              time.Now(),
	}

	// Try to create timer without shard record - should fail
	createErr := store.CreateTimer(ctx, shardId, 1, namespace, timer)

	// Should fail since shard doesn't exist
	assert.NotNil(t, createErr)
	assert.True(t, createErr.ShardConditionFail)
}

func TestCreateTimerNoLock_Basic(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 10
	namespace := "test_namespace-nolock"

	// Create a basic timer without needing shard ownership
	timer := &databases.DbTimer{
		Id:                     "timer-nolock-1",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-nolock-1"),
		Namespace:              namespace,
		ExecuteAt:              time.Now().Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              time.Now(),
	}

	createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
	assert.Nil(t, createErr)

	// Verify the timer was inserted by reading it back
	timerSortKey := GetTimerSortKey(namespace, timer.Id)
	key := map[string]types.AttributeValue{
		"shard_id": &types.AttributeValueMemberN{Value: "10"},
		"sort_key": &types.AttributeValueMemberS{Value: timerSortKey},
	}

	getItemInput := &dynamodb.GetItemInput{
		TableName: aws.String(store.tableName),
		Key:       key,
	}

	result, scanErr := store.client.GetItem(ctx, getItemInput)
	require.NoError(t, scanErr)
	require.NotNil(t, result.Item)

	// Verify fields
	assert.Equal(t, timer.Id, result.Item["timer_id"].(*types.AttributeValueMemberS).Value)
	assert.Equal(t, timer.Namespace, result.Item["timer_namespace"].(*types.AttributeValueMemberS).Value)
	assert.Equal(t, timer.CallbackUrl, result.Item["timer_callback_url"].(*types.AttributeValueMemberS).Value)
	assert.Equal(t, "30", result.Item["timer_callback_timeout_seconds"].(*types.AttributeValueMemberN).Value)
	assert.Equal(t, "0", result.Item["timer_attempts"].(*types.AttributeValueMemberN).Value)
}

func TestCreateTimerNoLock_WithPayload(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 11
	namespace := "test_namespace-nolock"

	// Create timer with complex payload
	payload := map[string]interface{}{
		"userId":   54321,
		"message":  "NoLock Timer Message",
		"settings": map[string]bool{"enabled": true},
		"tags":     []string{"nolock", "test"},
	}

	timer := &databases.DbTimer{
		Id:                     "timer-nolock-payload",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-nolock-payload"),
		Namespace:              namespace,
		ExecuteAt:              time.Now().Add(10 * time.Minute),
		CallbackUrl:            "https://example.com/nolock/callback",
		Payload:                payload,
		CallbackTimeoutSeconds: 60,
		CreatedAt:              time.Now(),
	}

	createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
	assert.Nil(t, createErr)

	// Verify payload was serialized correctly
	timerSortKey := GetTimerSortKey(namespace, timer.Id)
	key := map[string]types.AttributeValue{
		"shard_id": &types.AttributeValueMemberN{Value: "11"},
		"sort_key": &types.AttributeValueMemberS{Value: timerSortKey},
	}

	getItemInput := &dynamodb.GetItemInput{
		TableName: aws.String(store.tableName),
		Key:       key,
	}

	result, scanErr := store.client.GetItem(ctx, getItemInput)
	require.NoError(t, scanErr)
	require.NotNil(t, result.Item)

	dbPayload := result.Item["timer_payload"].(*types.AttributeValueMemberS).Value
	assert.Contains(t, dbPayload, "54321")
	assert.Contains(t, dbPayload, "NoLock Timer Message")
	assert.Contains(t, dbPayload, "enabled")
	assert.Contains(t, dbPayload, "nolock")
}

func TestCreateTimerNoLock_WithRetryPolicy(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 12
	namespace := "test_namespace-nolock"

	// Create timer with retry policy
	retryPolicy := map[string]interface{}{
		"maxAttempts":       5,
		"backoffMultiplier": 1.5,
		"initialInterval":   "60s",
		"maxInterval":       "600s",
	}

	timer := &databases.DbTimer{
		Id:                     "timer-nolock-retry",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-nolock-retry"),
		Namespace:              namespace,
		ExecuteAt:              time.Now().Add(15 * time.Minute),
		CallbackUrl:            "https://example.com/nolock/retry",
		RetryPolicy:            retryPolicy,
		CallbackTimeoutSeconds: 45,
		CreatedAt:              time.Now(),
	}

	createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
	assert.Nil(t, createErr)

	// Verify retry policy was serialized correctly
	timerSortKey := GetTimerSortKey(namespace, timer.Id)
	key := map[string]types.AttributeValue{
		"shard_id": &types.AttributeValueMemberN{Value: "12"},
		"sort_key": &types.AttributeValueMemberS{Value: timerSortKey},
	}

	getItemInput := &dynamodb.GetItemInput{
		TableName: aws.String(store.tableName),
		Key:       key,
	}

	result, scanErr := store.client.GetItem(ctx, getItemInput)
	require.NoError(t, scanErr)
	require.NotNil(t, result.Item)

	dbRetryPolicy := result.Item["timer_retry_policy"].(*types.AttributeValueMemberS).Value
	assert.Contains(t, dbRetryPolicy, "maxAttempts")
	assert.Contains(t, dbRetryPolicy, "backoffMultiplier")
	assert.Contains(t, dbRetryPolicy, "60s")
}

func TestCreateTimerNoLock_NilPayloadAndRetryPolicy(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 13
	namespace := "test_namespace-nolock"

	// Create timer with nil payload and retry policy
	timer := &databases.DbTimer{
		Id:                     "timer-nolock-nil-fields",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-nolock-nil-fields"),
		Namespace:              namespace,
		ExecuteAt:              time.Now().Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/nolock/nil",
		Payload:                nil,
		RetryPolicy:            nil,
		CallbackTimeoutSeconds: 30,
		CreatedAt:              time.Now(),
	}

	createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
	assert.Nil(t, createErr)

	// Verify timer was created (nil fields should be absent from DynamoDB item)
	timerSortKey := GetTimerSortKey(namespace, timer.Id)
	key := map[string]types.AttributeValue{
		"shard_id": &types.AttributeValueMemberN{Value: "13"},
		"sort_key": &types.AttributeValueMemberS{Value: timerSortKey},
	}

	getItemInput := &dynamodb.GetItemInput{
		TableName: aws.String(store.tableName),
		Key:       key,
	}

	result, scanErr := store.client.GetItem(ctx, getItemInput)
	require.NoError(t, scanErr)
	require.NotNil(t, result.Item)

	// Verify fields are created without payload and retry policy
	assert.Equal(t, timer.Id, result.Item["timer_id"].(*types.AttributeValueMemberS).Value)
	_, hasPayload := result.Item["timer_payload"]
	_, hasRetryPolicy := result.Item["timer_retry_policy"]
	assert.False(t, hasPayload, "Should not have payload field")
	assert.False(t, hasRetryPolicy, "Should not have retry policy field")
}

func TestCreateTimerNoLock_InvalidPayloadSerialization(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 14
	namespace := "test_namespace-nolock"

	// Create timer with non-serializable payload (function type)
	timer := &databases.DbTimer{
		Id:                     "timer-nolock-invalid-payload",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-nolock-invalid-payload"),
		Namespace:              namespace,
		ExecuteAt:              time.Now().Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/nolock/invalid",
		Payload:                func() {}, // Functions can't be JSON serialized
		CallbackTimeoutSeconds: 30,
		CreatedAt:              time.Now(),
	}

	createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)

	// Should fail with marshaling error
	assert.NotNil(t, createErr)
	assert.Contains(t, createErr.CustomMessage, "failed to marshal timer payload")
}

func TestCreateTimerNoLock_ConcurrentCreation(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 15
	namespace := "test_namespace-nolock"

	numGoroutines := 10
	var wg sync.WaitGroup
	results := make([]*databases.DbError, numGoroutines)

	// Launch concurrent timer creation (no shard ownership needed)
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			timer := &databases.DbTimer{
				Id:                     fmt.Sprintf("concurrent-nolock-timer-%d", idx),
				TimerUuid:              databases.GenerateTimerUUID(namespace, fmt.Sprintf("concurrent-nolock-timer-%d", idx)),
				Namespace:              namespace,
				ExecuteAt:              time.Now().Add(time.Duration(idx) * time.Minute),
				CallbackUrl:            fmt.Sprintf("https://example.com/nolock/callback/%d", idx),
				CallbackTimeoutSeconds: 30,
				CreatedAt:              time.Now(),
			}
			results[idx] = store.CreateTimerNoLock(ctx, shardId, namespace, timer)
		}(i)
	}

	wg.Wait()

	// All should succeed since there's no locking
	successCount := 0
	for i, result := range results {
		if result == nil {
			successCount++
		} else {
			t.Logf("Timer creation %d failed: %v", i, result.CustomMessage)
		}
	}

	assert.Equal(t, numGoroutines, successCount, "All concurrent timer creations should succeed")

	// Verify all timers were created by scanning the table
	scanInput := &dynamodb.ScanInput{
		TableName:        aws.String(store.tableName),
		FilterExpression: aws.String("shard_id = :shard_id AND begins_with(sort_key, :timer_prefix)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":shard_id":     &types.AttributeValueMemberN{Value: "15"},
			":timer_prefix": &types.AttributeValueMemberS{Value: timerSortKeyPrefix},
		},
	}

	scanResult, scanErr := store.client.Scan(ctx, scanInput)
	require.NoError(t, scanErr)
	assert.Equal(t, numGoroutines, int(scanResult.Count))
}

func TestCreateTimer_DuplicateTimerOverwrite(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"
	timerId := "duplicate-timer"
	baseUuid := databases.GenerateTimerUUID(namespace, timerId)
	alternateUuid := databases.GenerateTimerUUID(namespace, timerId+"_alt")

	// Create shard record
	ownerAddr := "owner-1"
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
	require.Nil(t, err)

	// 1. Create original timer
	originalExecuteAt := time.Now().Add(5 * time.Minute)
	originalTimer := &databases.DbTimer{
		Id:                     timerId,
		TimerUuid:              baseUuid,
		Namespace:              namespace,
		ExecuteAt:              originalExecuteAt,
		CallbackUrl:            "https://original.com/callback",
		CallbackTimeoutSeconds: 30,
		Payload:                map[string]interface{}{"version": "1"},
		CreatedAt:              time.Now(),
	}

	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, originalTimer)
	assert.Nil(t, createErr)

	// 2. Overwrite with same UUID and same execute_at
	updatedTimer1 := &databases.DbTimer{
		Id:                     timerId,
		TimerUuid:              baseUuid, // Same UUID
		Namespace:              namespace,
		ExecuteAt:              originalExecuteAt, // Same execute_at
		CallbackUrl:            "https://updated1.com/callback",
		CallbackTimeoutSeconds: 45,
		Payload:                map[string]interface{}{"version": "2"},
		CreatedAt:              time.Now(),
	}

	updateErr1 := store.CreateTimer(ctx, shardId, shardVersion, namespace, updatedTimer1)
	assert.Nil(t, updateErr1)

	// 3. Verify count = 1 (all databases should have only one timer)
	timerSortKey := GetTimerSortKey(namespace, timerId)
	scanInput1 := &dynamodb.ScanInput{
		TableName:        aws.String(store.tableName),
		FilterExpression: aws.String("shard_id = :shard_id AND begins_with(sort_key, :timer_prefix) AND timer_namespace = :namespace AND timer_id = :timer_id"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":shard_id":     &types.AttributeValueMemberN{Value: strconv.Itoa(shardId)},
			":timer_prefix": &types.AttributeValueMemberS{Value: "TIMER#"},
			":namespace":    &types.AttributeValueMemberS{Value: namespace},
			":timer_id":     &types.AttributeValueMemberS{Value: timerId},
		},
	}

	scanResult1, scanErr := store.client.Scan(ctx, scanInput1)
	require.NoError(t, scanErr)
	assert.Equal(t, int32(1), scanResult1.Count, "After same UUID + same execute_at: DynamoDB should have 1 timer")

	// 4. Overwrite with same UUID but different execute_at
	differentExecuteAt := time.Now().Add(10 * time.Minute)
	updatedTimer2 := &databases.DbTimer{
		Id:                     timerId,
		TimerUuid:              baseUuid, // Same UUID
		Namespace:              namespace,
		ExecuteAt:              differentExecuteAt, // Different execute_at
		CallbackUrl:            "https://updated2.com/callback",
		CallbackTimeoutSeconds: 60,
		Payload:                map[string]interface{}{"version": "3"},
		CreatedAt:              time.Now(),
	}

	updateErr2 := store.CreateTimer(ctx, shardId, shardVersion, namespace, updatedTimer2)
	assert.Nil(t, updateErr2)

	// 5. Verify count = 1 (DynamoDB overwrites based on primary key shard_id + sort_key)
	scanResult2, scanErr := store.client.Scan(ctx, scanInput1)
	require.NoError(t, scanErr)
	assert.Equal(t, int32(1), scanResult2.Count, "After same UUID + different execute_at: DynamoDB should have 1 timer")

	// 6. Overwrite with different UUID and different execute_at
	anotherExecuteAt := time.Now().Add(15 * time.Minute)
	updatedTimer3 := &databases.DbTimer{
		Id:                     timerId,
		TimerUuid:              alternateUuid, // Different UUID
		Namespace:              namespace,
		ExecuteAt:              anotherExecuteAt, // Different execute_at
		CallbackUrl:            "https://updated3.com/callback",
		CallbackTimeoutSeconds: 90,
		Payload:                map[string]interface{}{"version": "4"},
		CreatedAt:              time.Now(),
	}

	updateErr3 := store.CreateTimer(ctx, shardId, shardVersion, namespace, updatedTimer3)
	assert.Nil(t, updateErr3)

	// 7. Verify count = 1 (DynamoDB still overwrites based on primary key)
	scanResult3, scanErr := store.client.Scan(ctx, scanInput1)
	require.NoError(t, scanErr)
	assert.Equal(t, int32(1), scanResult3.Count, "After different UUID + different execute_at: DynamoDB should have 1 timer")

	// Verify final timer has the latest data
	getItemInput := &dynamodb.GetItemInput{
		TableName: aws.String(store.tableName),
		Key: map[string]types.AttributeValue{
			"shard_id": &types.AttributeValueMemberN{Value: strconv.Itoa(shardId)},
			"sort_key": &types.AttributeValueMemberS{Value: timerSortKey},
		},
	}

	finalResult, getErr := store.client.GetItem(ctx, getItemInput)
	require.NoError(t, getErr)
	require.NotNil(t, finalResult.Item)

	finalCallbackUrl := finalResult.Item["timer_callback_url"].(*types.AttributeValueMemberS).Value
	finalPayload := finalResult.Item["timer_payload"].(*types.AttributeValueMemberS).Value
	assert.Equal(t, "https://updated3.com/callback", finalCallbackUrl)
	assert.Contains(t, finalPayload, "4")
}

func TestCreateTimerNoLock_DuplicateTimerOverwrite(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Create initial timer
	originalTimer := &databases.DbTimer{
		Id:                     "duplicate-timer-nolock",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "duplicate-timer-nolock"),
		Namespace:              namespace,
		ExecuteAt:              time.Now().Add(5 * time.Minute),
		CallbackUrl:            "https://original-nolock.com/callback",
		CallbackTimeoutSeconds: 30,
		RetryPolicy:            map[string]interface{}{"maxAttempts": 3},
		CreatedAt:              time.Now(),
	}

	createErr := store.CreateTimerNoLock(ctx, shardId, namespace, originalTimer)
	assert.Nil(t, createErr)

	// Verify original timer was created
	timerSortKey := GetTimerSortKey(namespace, originalTimer.Id)
	getItemInput := &dynamodb.GetItemInput{
		TableName: aws.String(store.tableName),
		Key: map[string]types.AttributeValue{
			"shard_id": &types.AttributeValueMemberN{Value: strconv.Itoa(shardId)},
			"sort_key": &types.AttributeValueMemberS{Value: timerSortKey},
		},
	}

	result, getErr := store.client.GetItem(ctx, getItemInput)
	require.NoError(t, getErr)
	require.NotNil(t, result.Item)

	callbackUrl := result.Item["timer_callback_url"].(*types.AttributeValueMemberS).Value
	retryPolicy := result.Item["timer_retry_policy"].(*types.AttributeValueMemberS).Value
	assert.Equal(t, "https://original-nolock.com/callback", callbackUrl)
	assert.Contains(t, retryPolicy, "maxAttempts")

	// Create updated timer with same ID (should overwrite due to DynamoDB's PutItem behavior)
	updatedTimer := &databases.DbTimer{
		Id:                     "duplicate-timer-nolock",                                         // Same ID
		TimerUuid:              databases.GenerateTimerUUID(namespace, "duplicate-timer-nolock"), // Same UUID
		Namespace:              namespace,
		ExecuteAt:              time.Now().Add(15 * time.Minute),                                    // Different execution time
		CallbackUrl:            "https://updated-nolock.com/callback",                               // Different callback
		CallbackTimeoutSeconds: 60,                                                                  // Different timeout
		RetryPolicy:            map[string]interface{}{"maxAttempts": 5, "strategy": "exponential"}, // Different retry policy
		CreatedAt:              time.Now(),
	}

	updateErr := store.CreateTimerNoLock(ctx, shardId, namespace, updatedTimer)
	assert.Nil(t, updateErr, "CreateTimerNoLock should overwrite existing timer instead of failing")

	// Verify timer was overwritten (not duplicated)
	updatedResult, getErr := store.client.GetItem(ctx, getItemInput)
	require.NoError(t, getErr)
	require.NotNil(t, updatedResult.Item)

	updatedCallbackUrl := updatedResult.Item["timer_callback_url"].(*types.AttributeValueMemberS).Value
	updatedRetryPolicy := updatedResult.Item["timer_retry_policy"].(*types.AttributeValueMemberS).Value
	assert.Equal(t, "https://updated-nolock.com/callback", updatedCallbackUrl, "Timer should be overwritten with new callback URL")
	assert.Contains(t, updatedRetryPolicy, "exponential", "Timer should be overwritten with new retry policy")
	assert.Contains(t, updatedRetryPolicy, "5", "Timer should have updated maxAttempts")

	// Verify there's exactly one record (overwritten, not duplicated)
	scanInput := &dynamodb.ScanInput{
		TableName:        aws.String(store.tableName),
		FilterExpression: aws.String("shard_id = :shard_id AND begins_with(sort_key, :timer_prefix) AND timer_namespace = :namespace AND timer_id = :timer_id"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":shard_id":     &types.AttributeValueMemberN{Value: strconv.Itoa(shardId)},
			":timer_prefix": &types.AttributeValueMemberS{Value: "TIMER#"},
			":namespace":    &types.AttributeValueMemberS{Value: namespace},
			":timer_id":     &types.AttributeValueMemberS{Value: updatedTimer.Id},
		},
	}

	scanResult, scanErr := store.client.Scan(ctx, scanInput)
	require.NoError(t, scanErr)
	assert.Equal(t, int32(1), scanResult.Count, "Should have exactly one timer record (overwritten, not duplicated)")
}
