package mongodb

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/iworkflowio/durable-timer/databases"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
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
	filter := bson.M{
		"shard_id":        shardId,
		"row_type":        databases.RowTypeTimer,
		"timer_namespace": timer.Namespace,
		"timer_id":        timer.Id,
	}

	var result bson.M
	scanErr := store.collection.FindOne(ctx, filter).Decode(&result)
	require.NoError(t, scanErr)

	// Verify fields
	assert.Equal(t, timer.Id, getStringFromBSON(result, "timer_id"))
	assert.Equal(t, timer.Namespace, getStringFromBSON(result, "timer_namespace"))
	assert.Equal(t, timer.CallbackUrl, getStringFromBSON(result, "timer_callback_url"))
	assert.Equal(t, int64(30), getInt64FromBSON(result, "timer_callback_timeout_seconds"))
	assert.Equal(t, int64(0), getInt64FromBSON(result, "timer_attempts"))
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
	filter := bson.M{
		"shard_id":        shardId,
		"row_type":        databases.RowTypeTimer,
		"timer_namespace": timer.Namespace,
		"timer_id":        timer.Id,
	}

	var result bson.M
	scanErr := store.collection.FindOne(ctx, filter).Decode(&result)
	require.NoError(t, scanErr)

	dbPayload := getStringFromBSON(result, "timer_payload")
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
	filter := bson.M{
		"shard_id":        shardId,
		"row_type":        databases.RowTypeTimer,
		"timer_namespace": timer.Namespace,
		"timer_id":        timer.Id,
	}

	var result bson.M
	scanErr := store.collection.FindOne(ctx, filter).Decode(&result)
	require.NoError(t, scanErr)

	dbRetryPolicy := getStringFromBSON(result, "timer_retry_policy")
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
	// With transactions, we can return the actual conflicting version without additional cost
	assert.Equal(t, actualShardVersion, createErr.ConflictShardVersion)

	// Verify timer was not inserted
	filter := bson.M{
		"shard_id":        shardId,
		"row_type":        databases.RowTypeTimer,
		"timer_namespace": timer.Namespace,
		"timer_id":        timer.Id,
	}

	var result bson.M
	scanErr := store.collection.FindOne(ctx, filter).Decode(&result)
	assert.Error(t, scanErr) // Should not be found
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

	// Verify all timers were created by counting timer records in the shard
	filter := bson.M{
		"shard_id": shardId,
		"row_type": databases.RowTypeTimer,
	}

	totalCount, countErr := store.collection.CountDocuments(ctx, filter)
	require.NoError(t, countErr)
	assert.Equal(t, int64(numGoroutines), totalCount)
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
	namespace := "test-namespace-nolock"

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
	filter := bson.M{
		"shard_id":        shardId,
		"row_type":        databases.RowTypeTimer,
		"timer_namespace": timer.Namespace,
		"timer_id":        timer.Id,
	}

	var result bson.M
	scanErr := store.collection.FindOne(ctx, filter).Decode(&result)
	require.NoError(t, scanErr)

	// Verify fields
	assert.Equal(t, timer.Id, getStringFromBSON(result, "timer_id"))
	assert.Equal(t, timer.Namespace, getStringFromBSON(result, "timer_namespace"))
	assert.Equal(t, timer.CallbackUrl, getStringFromBSON(result, "timer_callback_url"))
	assert.Equal(t, int64(30), getInt64FromBSON(result, "timer_callback_timeout_seconds"))
	assert.Equal(t, int64(0), getInt64FromBSON(result, "timer_attempts"))
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
	filter := bson.M{
		"shard_id":        shardId,
		"row_type":        databases.RowTypeTimer,
		"timer_namespace": timer.Namespace,
		"timer_id":        timer.Id,
	}

	var result bson.M
	scanErr := store.collection.FindOne(ctx, filter).Decode(&result)
	require.NoError(t, scanErr)

	dbPayload := getStringFromBSON(result, "timer_payload")
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
	filter := bson.M{
		"shard_id":        shardId,
		"row_type":        databases.RowTypeTimer,
		"timer_namespace": timer.Namespace,
		"timer_id":        timer.Id,
	}

	var result bson.M
	scanErr := store.collection.FindOne(ctx, filter).Decode(&result)
	require.NoError(t, scanErr)

	dbRetryPolicy := getStringFromBSON(result, "timer_retry_policy")
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

	// Verify timer was created (nil fields should be absent from MongoDB document)
	filter := bson.M{
		"shard_id":        shardId,
		"row_type":        databases.RowTypeTimer,
		"timer_namespace": timer.Namespace,
		"timer_id":        timer.Id,
	}

	var result bson.M
	scanErr := store.collection.FindOne(ctx, filter).Decode(&result)
	require.NoError(t, scanErr)

	// Verify fields are created without payload and retry policy
	assert.Equal(t, timer.Id, getStringFromBSON(result, "timer_id"))
	_, hasPayload := result["timer_payload"]
	_, hasRetryPolicy := result["timer_retry_policy"]
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

	// Verify all timers were created by counting timer records in the shard
	filter := bson.M{
		"shard_id": shardId,
		"row_type": databases.RowTypeTimer,
	}

	totalCount, countErr := store.collection.CountDocuments(ctx, filter)
	require.NoError(t, countErr)
	assert.Equal(t, int64(numGoroutines), totalCount)
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
	filter := bson.M{
		"shard_id":        shardId,
		"row_type":        databases.RowTypeTimer,
		"timer_namespace": namespace,
		"timer_id":        timerId,
	}
	count1, countErr := store.collection.CountDocuments(ctx, filter)
	require.NoError(t, countErr)
	assert.Equal(t, int64(1), count1, "After same UUID + same execute_at: MongoDB should have 1 timer")

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

	// 5. Verify count = 1 (MongoDB should still have 1 timer due to unique constraint)
	count2, countErr := store.collection.CountDocuments(ctx, filter)
	require.NoError(t, countErr)
	assert.Equal(t, int64(1), count2, "After same UUID + different execute_at: MongoDB should have 1 timer")

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

	// 7. Verify count = 1 (MongoDB should still have 1 timer due to unique constraint)
	count3, countErr := store.collection.CountDocuments(ctx, filter)
	require.NoError(t, countErr)
	assert.Equal(t, int64(1), count3, "After different UUID + different execute_at: MongoDB should have 1 timer")

	// Verify final timer has the latest data
	var finalResult bson.M
	findErr := store.collection.FindOne(ctx, filter).Decode(&finalResult)
	require.NoError(t, findErr)
	assert.Equal(t, "https://updated3.com/callback", finalResult["timer_callback_url"])
	assert.Contains(t, finalResult["timer_payload"], "4")
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
	filter := bson.M{
		"shard_id":        shardId,
		"row_type":        databases.RowTypeTimer,
		"timer_namespace": originalTimer.Namespace,
		"timer_id":        originalTimer.Id,
	}

	var result bson.M
	findErr := store.collection.FindOne(ctx, filter).Decode(&result)
	require.NoError(t, findErr)
	assert.Equal(t, "https://original-nolock.com/callback", result["timer_callback_url"])
	assert.Contains(t, result["timer_retry_policy"], "maxAttempts")

	// Create updated timer with same ID (should overwrite)
	updatedTimer := &databases.DbTimer{
		Id:                     "duplicate-timer-nolock", // Same ID
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
	count, countErr := store.collection.CountDocuments(ctx, filter)
	require.NoError(t, countErr)
	assert.Equal(t, int64(1), count, "Should have exactly one timer record (overwritten, not duplicated)")

	// Verify the timer has updated values
	var updatedResult bson.M
	findErr = store.collection.FindOne(ctx, filter).Decode(&updatedResult)
	require.NoError(t, findErr)
	assert.Equal(t, "https://updated-nolock.com/callback", updatedResult["timer_callback_url"], "Timer should be overwritten with new callback URL")
	assert.Contains(t, updatedResult["timer_retry_policy"], "exponential", "Timer should be overwritten with new retry policy")
	assert.Contains(t, updatedResult["timer_retry_policy"], "5", "Timer should have updated maxAttempts")
}
