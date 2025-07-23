package dynamodb

import (
	"context"
	"fmt"
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
	namespace := "test-namespace"

	// First claim the shard to set up shard version
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, "owner-1", nil)
	require.Nil(t, err)
	require.Equal(t, int64(1), shardVersion)

	// Create a basic timer
	timer := &databases.DbTimer{
		Id:                     "timer-1",
		Namespace:              namespace,
		ExecuteAt:              time.Now().Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              time.Now(),
	}

	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	assert.Nil(t, createErr)

	// Verify the timer was inserted by reading it back
	timerSortKey := fmt.Sprintf("%s%s#%s", timerSortKeyPrefix, namespace, timer.Id)
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
	namespace := "test-namespace"

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
	timerSortKey := fmt.Sprintf("%s%s#%s", timerSortKeyPrefix, namespace, timer.Id)
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
	namespace := "test-namespace"

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
	timerSortKey := fmt.Sprintf("%s%s#%s", timerSortKeyPrefix, namespace, timer.Id)
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
	namespace := "test-namespace"

	// First claim the shard
	actualShardVersion, err := store.ClaimShardOwnership(ctx, shardId, "owner-1", nil)
	require.Nil(t, err)

	timer := &databases.DbTimer{
		Id:                     "timer-version-mismatch",
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
	timerSortKey := fmt.Sprintf("%s%s#%s", timerSortKeyPrefix, namespace, timer.Id)
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
	namespace := "test-namespace"

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
	namespace := "test-namespace"

	// Don't claim the shard first - no shard record exists
	timer := &databases.DbTimer{
		Id:                     "timer-no-shard",
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
	namespace := "test-namespace-nolock"

	// Create a basic timer without needing shard ownership
	timer := &databases.DbTimer{
		Id:                     "timer-nolock-1",
		Namespace:              namespace,
		ExecuteAt:              time.Now().Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              time.Now(),
	}

	createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
	assert.Nil(t, createErr)

	// Verify the timer was inserted by reading it back
	timerSortKey := fmt.Sprintf("%s%s#%s", timerSortKeyPrefix, namespace, timer.Id)
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
	namespace := "test-namespace-nolock"

	// Create timer with complex payload
	payload := map[string]interface{}{
		"userId":   54321,
		"message":  "NoLock Timer Message",
		"settings": map[string]bool{"enabled": true},
		"tags":     []string{"nolock", "test"},
	}

	timer := &databases.DbTimer{
		Id:                     "timer-nolock-payload",
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
	timerSortKey := fmt.Sprintf("%s%s#%s", timerSortKeyPrefix, namespace, timer.Id)
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
	namespace := "test-namespace-nolock"

	// Create timer with retry policy
	retryPolicy := map[string]interface{}{
		"maxAttempts":       5,
		"backoffMultiplier": 1.5,
		"initialInterval":   "60s",
		"maxInterval":       "600s",
	}

	timer := &databases.DbTimer{
		Id:                     "timer-nolock-retry",
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
	timerSortKey := fmt.Sprintf("%s%s#%s", timerSortKeyPrefix, namespace, timer.Id)
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
	namespace := "test-namespace-nolock"

	// Create timer with nil payload and retry policy
	timer := &databases.DbTimer{
		Id:                     "timer-nolock-nil-fields",
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
	timerSortKey := fmt.Sprintf("%s%s#%s", timerSortKeyPrefix, namespace, timer.Id)
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
	namespace := "test-namespace-nolock"

	// Create timer with non-serializable payload (function type)
	timer := &databases.DbTimer{
		Id:                     "timer-nolock-invalid-payload",
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
	namespace := "test-namespace-nolock"

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
