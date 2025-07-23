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
	ownerId := "owner-1"
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, ownerId, nil)
	require.Nil(t, err)
	require.Equal(t, int64(1), shardVersion)

	// Create a timer
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

	// Create shard record
	ownerId := "owner-1"
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, ownerId, nil)
	require.Nil(t, err)

	// Create timer with payload
	payload := map[string]interface{}{
		"userId":   12345,
		"action":   "send_email",
		"metadata": map[string]interface{}{"template": "welcome"},
	}

	timer := &databases.DbTimer{
		Id:                     "timer-with-payload",
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
	assert.Equal(t, float64(12345), storedPayload["userId"]) // JSON numbers are float64
	assert.Equal(t, "send_email", storedPayload["action"])
}

func TestCreateTimer_WithRetryPolicy(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	// Create shard record
	ownerId := "owner-1"
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, ownerId, nil)
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
	ownerId := "owner-1"
	actualShardVersion, err := store.ClaimShardOwnership(ctx, shardId, ownerId, nil)
	require.Nil(t, err)

	// Create timer with wrong shard version
	timer := &databases.DbTimer{
		Id:                     "timer-version-mismatch",
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
	ownerId := "owner-1"
	shardVersion, err := store.ClaimShardOwnership(ctx, shardId, ownerId, nil)
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

// CreateTimerNoLock Tests

func TestCreateTimerNoLock_Basic(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1
	namespace := "test_namespace"

	timer := &databases.DbTimer{
		Id:                     "timer-no-lock",
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
