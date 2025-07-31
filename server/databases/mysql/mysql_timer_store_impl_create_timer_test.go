package mysql

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

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

	// Verify timer was inserted
	var dbTimerId, dbNamespace string
	var dbExecuteAt, dbCreatedAt time.Time
	var dbCallbackUrl string
	var dbTimeoutSeconds int32

	query := `SELECT timer_id, timer_namespace, timer_execute_at, timer_callback_url, 
	                 timer_callback_timeout_seconds, timer_created_at 
	          FROM timers 
	          WHERE shard_id = ? AND row_type = ? AND timer_namespace = ? AND timer_id = ?`

	scanErr := store.db.QueryRow(query, shardId, databases.RowTypeTimer, timer.Namespace, timer.Id).
		Scan(&dbTimerId, &dbNamespace, &dbExecuteAt, &dbCallbackUrl, &dbTimeoutSeconds, &dbCreatedAt)

	require.NoError(t, scanErr)
	assert.Equal(t, timer.Id, dbTimerId)
	assert.Equal(t, timer.Namespace, dbNamespace)
	assert.Equal(t, timer.CallbackUrl, dbCallbackUrl)
	assert.Equal(t, timer.CallbackTimeoutSeconds, dbTimeoutSeconds)
}

func TestCreateTimer_WithPayload(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// First, create a shard record
	ownerAddr := "owner-1"
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
	require.Nil(t, err)

	// Create a timer with payload
	payload := map[string]interface{}{
		"userId":    123,
		"message":   "Hello World",
		"timestamp": time.Now().Unix(),
	}
	timer := &databases.DbTimer{
		Id:                     "timer-with-payload",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-with-payload"),
		Namespace:              namespace,
		ExecuteAt:              time.Now().Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		Payload:                payload,
		CreatedAt:              time.Now(),
	}

	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	assert.Nil(t, createErr)

	// Verify payload was stored correctly
	var dbPayload string
	query := `SELECT timer_payload FROM timers 
	          WHERE shard_id = ? AND row_type = ? AND timer_namespace = ? AND timer_id = ?`

	scanErr := store.db.QueryRow(query, shardId, databases.RowTypeTimer, timer.Namespace, timer.Id).
		Scan(&dbPayload)

	require.NoError(t, scanErr)
	assert.NotEmpty(t, dbPayload)

	// Verify payload content
	var storedPayload map[string]interface{}
	jsonErr := json.Unmarshal([]byte(dbPayload), &storedPayload)
	require.NoError(t, jsonErr)
	assert.Equal(t, float64(123), storedPayload["userId"]) // JSON numbers are float64
	assert.Equal(t, "Hello World", storedPayload["message"])
	assert.Equal(t, float64(time.Now().Unix()), storedPayload["timestamp"])
}

func TestCreateTimer_WithRetryPolicy(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Create shard record
	ownerAddr := "owner-1"
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
	require.Nil(t, err)

	// Create timer with retry policy
	retryPolicy := map[string]interface{}{
		"maxAttempts":   3,
		"backoffFactor": 2.0,
		"initialDelay":  "1s",
		"maxDelay":      "30s",
	}

	timer := &databases.DbTimer{
		Id:                     "timer-with-retry",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-with-retry"),
		Namespace:              namespace,
		ExecuteAt:              time.Now().Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		RetryPolicy:            retryPolicy,
		CreatedAt:              time.Now(),
	}

	createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
	assert.Nil(t, createErr)

	// Verify retry policy was stored correctly
	var dbRetryPolicy string
	query := `SELECT timer_retry_policy FROM timers 
	          WHERE shard_id = ? AND row_type = ? AND timer_namespace = ? AND timer_id = ?`

	scanErr := store.db.QueryRow(query, shardId, databases.RowTypeTimer, timer.Namespace, timer.Id).
		Scan(&dbRetryPolicy)

	require.NoError(t, scanErr)
	assert.NotEmpty(t, dbRetryPolicy)

	// Verify retry policy content
	var storedRetryPolicy map[string]interface{}
	jsonErr := json.Unmarshal([]byte(dbRetryPolicy), &storedRetryPolicy)
	require.NoError(t, jsonErr)
	assert.Equal(t, float64(3), storedRetryPolicy["maxAttempts"])
	assert.Equal(t, "1s", storedRetryPolicy["initialDelay"])
}

func TestCreateTimer_ShardVersionMismatch(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Create shard record
	ownerAddr := "owner-1"
	actualShardVersion, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
	require.Nil(t, err)

	// Create timer with wrong shard version
	timer := &databases.DbTimer{
		Id:                     "timer-version-mismatch",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-version-mismatch"),
		Namespace:              namespace,
		ExecuteAt:              time.Now().Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              time.Now(),
	}

	wrongShardVersion := actualShardVersion + 1
	createErr := store.CreateTimer(ctx, shardId, wrongShardVersion, namespace, timer)

	// Should fail with shard condition error
	assert.NotNil(t, createErr)
	assert.True(t, createErr.ShardConditionFail)
	// With transactions, we can return the actual conflicting version
	assert.Equal(t, actualShardVersion, createErr.ConflictShardVersion)

	// Verify timer was not inserted
	var count int
	query := `SELECT COUNT(*) FROM timers 
	          WHERE shard_id = ? AND row_type = ? AND timer_namespace = ? AND timer_id = ?`

	scanErr := store.db.QueryRow(query, shardId, databases.RowTypeTimer, timer.Namespace, timer.Id).
		Scan(&count)

	require.NoError(t, scanErr)
	assert.Equal(t, 0, count, "Timer should not be inserted when shard version mismatches")
}

func TestCreateTimer_ConcurrentCreation(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Create shard record
	ownerAddr := "owner-1"
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr, nil)
	require.Nil(t, err)

	// Create multiple timers concurrently
	numTimers := 10
	var wg sync.WaitGroup
	errors := make([]error, numTimers)
	successes := make([]bool, numTimers)

	for i := 0; i < numTimers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			timer := &databases.DbTimer{
				Id:                     fmt.Sprintf("concurrent-timer-%d", index),
				TimerUuid:              databases.GenerateTimerUUID(namespace, fmt.Sprintf("concurrent-timer-%d", index)),
				Namespace:              namespace,
				ExecuteAt:              time.Now().Add(5 * time.Minute),
				CallbackUrl:            fmt.Sprintf("https://example.com/callback/%d", index),
				CallbackTimeoutSeconds: 30,
				CreatedAt:              time.Now(),
			}

			createErr := store.CreateTimer(ctx, shardId, shardVersion, namespace, timer)
			if createErr != nil {
				errors[index] = createErr
				t.Logf("Timer creation %d failed: %v", index, createErr.CustomMessage)
			} else {
				successes[index] = true
			}
		}(i)
	}

	wg.Wait()

	// Count successful creations
	successCount := 0
	for _, success := range successes {
		if success {
			successCount++
		}
	}

	assert.Equal(t, numTimers, successCount, "All concurrent timer creations should succeed")

	// Verify all timers were inserted
	var dbCount int
	query := `SELECT COUNT(*) FROM timers 
	          WHERE shard_id = ? AND row_type = ? AND timer_namespace = ?`

	scanErr := store.db.QueryRow(query, shardId, databases.RowTypeTimer, namespace).Scan(&dbCount)
	require.NoError(t, scanErr)
	assert.Equal(t, numTimers, dbCount)
}

func TestCreateTimer_NoShardRecord(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 999 // Non-existent shard
	namespace := "test_namespace"

	timer := &databases.DbTimer{
		Id:                     "timer-no-shard",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-no-shard"),
		Namespace:              namespace,
		ExecuteAt:              time.Now().Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              time.Now(),
	}

	createErr := store.CreateTimer(ctx, shardId, 1, namespace, timer)

	// Should fail with shard condition error
	assert.NotNil(t, createErr)
	assert.True(t, createErr.ShardConditionFail)
	assert.Equal(t, int64(0), createErr.ConflictShardVersion)

	// Verify timer was not inserted
	var count int
	query := `SELECT COUNT(*) FROM timers 
	          WHERE shard_id = ? AND row_type = ? AND timer_namespace = ? AND timer_id = ?`

	scanErr := store.db.QueryRow(query, shardId, databases.RowTypeTimer, timer.Namespace, timer.Id).
		Scan(&count)

	require.NoError(t, scanErr)
	assert.Equal(t, 0, count, "Timer should not be inserted when shard doesn't exist")
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
	var count1 int
	countQuery := `SELECT COUNT(*) FROM timers 
	               WHERE shard_id = ? AND row_type = ? AND timer_namespace = ? AND timer_id = ?`
	scanErr := store.db.QueryRow(countQuery, shardId, databases.RowTypeTimer, namespace, timerId).Scan(&count1)
	require.NoError(t, scanErr)
	assert.Equal(t, 1, count1, "After same UUID + same execute_at: MySQL should have 1 timer")

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

	// 5. Verify count = 1 (MySQL should still have 1 timer due to unique constraint)
	var count2 int
	scanErr = store.db.QueryRow(countQuery, shardId, databases.RowTypeTimer, namespace, timerId).Scan(&count2)
	require.NoError(t, scanErr)
	assert.Equal(t, 1, count2, "After same UUID + different execute_at: MySQL should have 1 timer")

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

	// 7. Verify count = 1 (MySQL should still have 1 timer due to unique constraint)
	var count3 int
	scanErr = store.db.QueryRow(countQuery, shardId, databases.RowTypeTimer, namespace, timerId).Scan(&count3)
	require.NoError(t, scanErr)
	assert.Equal(t, 1, count3, "After different UUID + different execute_at: MySQL should have 1 timer")

	// Verify final timer has the latest data
	var finalCallbackUrl string
	var finalPayload string
	selectQuery := `SELECT timer_callback_url, timer_payload FROM timers 
	                WHERE shard_id = ? AND row_type = ? AND timer_namespace = ? AND timer_id = ?`
	scanErr = store.db.QueryRow(selectQuery, shardId, databases.RowTypeTimer, namespace, timerId).
		Scan(&finalCallbackUrl, &finalPayload)
	require.NoError(t, scanErr)
	assert.Equal(t, "https://updated3.com/callback", finalCallbackUrl)
	assert.Contains(t, finalPayload, "4")
}

// CreateTimerNoLock Tests

func TestCreateTimerNoLock_Basic(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	timer := &databases.DbTimer{
		Id:                     "timer-no-lock",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-no-lock"),
		Namespace:              namespace,
		ExecuteAt:              time.Now().Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		CreatedAt:              time.Now(),
	}

	createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
	assert.Nil(t, createErr)

	// Verify timer was inserted
	var dbTimerId string
	query := `SELECT timer_id FROM timers 
	          WHERE shard_id = ? AND row_type = ? AND timer_namespace = ? AND timer_id = ?`

	scanErr := store.db.QueryRow(query, shardId, databases.RowTypeTimer, timer.Namespace, timer.Id).
		Scan(&dbTimerId)

	require.NoError(t, scanErr)
	assert.Equal(t, timer.Id, dbTimerId)
}

func TestCreateTimerNoLock_WithPayload(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	payload := map[string]interface{}{
		"data": "test-payload-no-lock",
	}

	timer := &databases.DbTimer{
		Id:                     "timer-no-lock-payload",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-no-lock-payload"),
		Namespace:              namespace,
		ExecuteAt:              time.Now().Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		Payload:                payload,
		CreatedAt:              time.Now(),
	}

	createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
	assert.Nil(t, createErr)

	// Verify payload was stored
	var dbPayload string
	query := `SELECT timer_payload FROM timers 
	          WHERE shard_id = ? AND row_type = ? AND timer_namespace = ? AND timer_id = ?`

	scanErr := store.db.QueryRow(query, shardId, databases.RowTypeTimer, timer.Namespace, timer.Id).
		Scan(&dbPayload)

	require.NoError(t, scanErr)
	assert.Contains(t, dbPayload, "test-payload-no-lock")
}

func TestCreateTimerNoLock_WithRetryPolicy(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	retryPolicy := map[string]interface{}{
		"maxAttempts": 5,
		"strategy":    "exponential",
	}

	timer := &databases.DbTimer{
		Id:                     "timer-no-lock-retry",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-no-lock-retry"),
		Namespace:              namespace,
		ExecuteAt:              time.Now().Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		RetryPolicy:            retryPolicy,
		CreatedAt:              time.Now(),
	}

	createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
	assert.Nil(t, createErr)

	// Verify retry policy was stored
	var dbRetryPolicy string
	query := `SELECT timer_retry_policy FROM timers 
	          WHERE shard_id = ? AND row_type = ? AND timer_namespace = ? AND timer_id = ?`

	scanErr := store.db.QueryRow(query, shardId, databases.RowTypeTimer, timer.Namespace, timer.Id).
		Scan(&dbRetryPolicy)

	require.NoError(t, scanErr)
	assert.Contains(t, dbRetryPolicy, "exponential")
}

func TestCreateTimerNoLock_NilPayloadAndRetryPolicy(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	timer := &databases.DbTimer{
		Id:                     "timer-no-lock-nil",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-no-lock-nil"),
		Namespace:              namespace,
		ExecuteAt:              time.Now().Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		Payload:                nil,
		RetryPolicy:            nil,
		CreatedAt:              time.Now(),
	}

	createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
	assert.Nil(t, createErr)

	// Verify timer was inserted with NULL payload and retry policy
	var dbPayload, dbRetryPolicy interface{}
	query := `SELECT timer_payload, timer_retry_policy FROM timers 
	          WHERE shard_id = ? AND row_type = ? AND timer_namespace = ? AND timer_id = ?`

	scanErr := store.db.QueryRow(query, shardId, databases.RowTypeTimer, timer.Namespace, timer.Id).
		Scan(&dbPayload, &dbRetryPolicy)

	require.NoError(t, scanErr)
	assert.Nil(t, dbPayload)
	assert.Nil(t, dbRetryPolicy)
}

func TestCreateTimerNoLock_InvalidPayloadSerialization(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Use a payload that can't be serialized to JSON (function)
	invalidPayload := map[string]interface{}{
		"validField":   "value",
		"invalidField": func() {}, // Functions can't be serialized to JSON
	}

	timer := &databases.DbTimer{
		Id:                     "timer-invalid-payload",
		TimerUuid:              databases.GenerateTimerUUID(namespace, "timer-invalid-payload"),
		Namespace:              namespace,
		ExecuteAt:              time.Now().Add(5 * time.Minute),
		CallbackUrl:            "https://example.com/callback",
		CallbackTimeoutSeconds: 30,
		Payload:                invalidPayload,
		CreatedAt:              time.Now(),
	}

	createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)

	// Should fail with serialization error
	assert.NotNil(t, createErr)
	assert.Contains(t, createErr.CustomMessage, "failed to marshal timer payload")
	assert.False(t, createErr.ShardConditionFail)
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
	var dbCallbackUrl string
	var dbRetryPolicy string
	query := `SELECT timer_callback_url, timer_retry_policy FROM timers 
	          WHERE shard_id = ? AND row_type = ? AND timer_namespace = ? AND timer_id = ?`

	scanErr := store.db.QueryRow(query, shardId, databases.RowTypeTimer, originalTimer.Namespace, originalTimer.Id).
		Scan(&dbCallbackUrl, &dbRetryPolicy)

	require.NoError(t, scanErr)
	assert.Equal(t, "https://original-nolock.com/callback", dbCallbackUrl)
	assert.Contains(t, dbRetryPolicy, "maxAttempts")

	// Create updated timer with same ID (should overwrite)
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
	var count int
	var updatedCallbackUrl string
	var updatedRetryPolicy string

	countQuery := `SELECT COUNT(*) FROM timers 
	               WHERE shard_id = ? AND row_type = ? AND timer_namespace = ? AND timer_id = ?`
	scanErr = store.db.QueryRow(countQuery, shardId, databases.RowTypeTimer, updatedTimer.Namespace, updatedTimer.Id).
		Scan(&count)

	require.NoError(t, scanErr)
	assert.Equal(t, 1, count, "Should have exactly one timer record (overwritten, not duplicated)")

	// Verify the timer has updated values
	scanErr = store.db.QueryRow(query, shardId, databases.RowTypeTimer, updatedTimer.Namespace, updatedTimer.Id).
		Scan(&updatedCallbackUrl, &updatedRetryPolicy)

	require.NoError(t, scanErr)
	assert.Equal(t, "https://updated-nolock.com/callback", updatedCallbackUrl, "Timer should be overwritten with new callback URL")
	assert.Contains(t, updatedRetryPolicy, "exponential", "Timer should be overwritten with new retry policy")
	assert.Contains(t, updatedRetryPolicy, "5", "Timer should have updated maxAttempts")
}

func TestCreateTimerNoLock_ConcurrentCreation(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	numTimers := 10
	var wg sync.WaitGroup
	errors := make([]error, numTimers)
	successes := make([]bool, numTimers)

	for i := 0; i < numTimers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			timer := &databases.DbTimer{
				Id:                     fmt.Sprintf("concurrent-nolock-timer-%d", index),
				TimerUuid:              databases.GenerateTimerUUID(namespace, fmt.Sprintf("concurrent-nolock-timer-%d", index)),
				Namespace:              namespace,
				ExecuteAt:              time.Now().Add(5 * time.Minute),
				CallbackUrl:            fmt.Sprintf("https://example.com/callback/%d", index),
				CallbackTimeoutSeconds: 30,
				CreatedAt:              time.Now(),
			}

			createErr := store.CreateTimerNoLock(ctx, shardId, namespace, timer)
			if createErr != nil {
				errors[index] = createErr
			} else {
				successes[index] = true
			}
		}(i)
	}

	wg.Wait()

	// Count successful creations
	successCount := 0
	for _, success := range successes {
		if success {
			successCount++
		}
	}

	assert.Equal(t, numTimers, successCount, "All concurrent timer creations should succeed")

	// Verify all timers were inserted
	var dbCount int
	query := `SELECT COUNT(*) FROM timers 
	          WHERE shard_id = ? AND row_type = ? AND timer_namespace = ? AND timer_id LIKE ?`

	scanErr := store.db.QueryRow(query, shardId, databases.RowTypeTimer, namespace, "concurrent-nolock-timer-%").
		Scan(&dbCount)

	require.NoError(t, scanErr)
	assert.Equal(t, numTimers, dbCount)
}
