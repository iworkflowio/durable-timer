package cassandra

import (
	"context"
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

	// Verify the timer was inserted by counting timers in the shard
	var count int
	countQuery := `SELECT COUNT(*) FROM timers WHERE shard_id = ? AND row_type = ? ALLOW FILTERING`
	scanErr := store.session.Query(countQuery, shardId, databases.RowTypeTimer).Scan(&count)
	require.NoError(t, scanErr)
	assert.Equal(t, 1, count)

	// Verify using the index to find the timer
	var dbTimerId, dbNamespace, dbCallbackUrl string
	var dbExecuteAt, dbCreatedAt time.Time
	var dbCallbackTimeout int32
	var dbAttempts int

	indexQuery := `SELECT timer_id, timer_namespace, timer_execute_at, timer_callback_url, timer_callback_timeout_seconds, timer_created_at, timer_attempts 
	               FROM timers WHERE timer_namespace = ? AND timer_id = ? ALLOW FILTERING`
	scanErr = store.session.Query(indexQuery, timer.Namespace, timer.Id).
		Scan(&dbTimerId, &dbNamespace, &dbExecuteAt, &dbCallbackUrl, &dbCallbackTimeout, &dbCreatedAt, &dbAttempts)

	require.NoError(t, scanErr)
	assert.Equal(t, timer.Id, dbTimerId)
	assert.Equal(t, timer.Namespace, dbNamespace)
	assert.Equal(t, timer.CallbackUrl, dbCallbackUrl)
	assert.Equal(t, timer.CallbackTimeoutSeconds, dbCallbackTimeout)
	assert.Equal(t, 0, dbAttempts) // Should start at 0
	assert.WithinDuration(t, timer.ExecuteAt, dbExecuteAt, time.Second)
	assert.WithinDuration(t, timer.CreatedAt, dbCreatedAt, time.Second)
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

	// Verify payload was serialized correctly using the index
	var dbPayload string
	query := `SELECT timer_payload FROM timers WHERE timer_namespace = ? AND timer_id = ? ALLOW FILTERING`
	scanErr := store.session.Query(query, timer.Namespace, timer.Id).Scan(&dbPayload)

	require.NoError(t, scanErr)
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

	// Verify retry policy was serialized correctly using the index
	var dbRetryPolicy string
	query := `SELECT timer_retry_policy FROM timers WHERE timer_namespace = ? AND timer_id = ? ALLOW FILTERING`
	scanErr := store.session.Query(query, timer.Namespace, timer.Id).Scan(&dbRetryPolicy)

	require.NoError(t, scanErr)
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
	assert.Equal(t, actualShardVersion, createErr.ConflictShardVersion)

	// Verify timer was not inserted by checking it doesn't exist in the index
	var dbTimerId string
	countQuery := `SELECT timer_id FROM timers WHERE timer_namespace = ? AND timer_id = ? ALLOW FILTERING`
	scanErr := store.session.Query(countQuery, timer.Namespace, timer.Id).Scan(&dbTimerId)

	// Should get "not found" error
	assert.Error(t, scanErr)
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

	numGoroutines := 10 // Back to original count
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

	// Check results
	successCount := 0
	for i, result := range results {
		if result == nil {
			successCount++
		} else {
			t.Logf("Timer creation %d failed: %v", i, result.CustomMessage)
		}
	}

	// Verify all timers were created by counting timer records in the shard
	var totalCount int
	countQuery := `SELECT COUNT(*) FROM timers WHERE shard_id = ? AND row_type = ? ALLOW FILTERING`
	scanErr := store.session.Query(countQuery, shardId, databases.RowTypeTimer).Scan(&totalCount)

	require.NoError(t, scanErr)

	assert.Equal(t, numGoroutines, successCount, "All concurrent timer creations should succeed")
	assert.Equal(t, numGoroutines, totalCount, "All timers should be found in database")
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

	// Verify the timer was inserted
	var count int
	countQuery := `SELECT COUNT(*) FROM timers WHERE shard_id = ? AND row_type = ? ALLOW FILTERING`
	scanErr := store.session.Query(countQuery, shardId, databases.RowTypeTimer).Scan(&count)
	require.NoError(t, scanErr)
	assert.Equal(t, 1, count)

	// Verify using the index to find the timer
	var dbTimerId, dbNamespace, dbCallbackUrl string
	var dbExecuteAt, dbCreatedAt time.Time
	var dbCallbackTimeout int32
	var dbAttempts int

	indexQuery := `SELECT timer_id, timer_namespace, timer_execute_at, timer_callback_url, timer_callback_timeout_seconds, timer_created_at, timer_attempts 
	               FROM timers WHERE timer_namespace = ? AND timer_id = ? ALLOW FILTERING`
	scanErr = store.session.Query(indexQuery, timer.Namespace, timer.Id).
		Scan(&dbTimerId, &dbNamespace, &dbExecuteAt, &dbCallbackUrl, &dbCallbackTimeout, &dbCreatedAt, &dbAttempts)

	require.NoError(t, scanErr)
	assert.Equal(t, timer.Id, dbTimerId)
	assert.Equal(t, timer.Namespace, dbNamespace)
	assert.Equal(t, timer.CallbackUrl, dbCallbackUrl)
	assert.Equal(t, timer.CallbackTimeoutSeconds, dbCallbackTimeout)
	assert.Equal(t, 0, dbAttempts) // Should start at 0
	assert.WithinDuration(t, timer.ExecuteAt, dbExecuteAt, time.Second)
	assert.WithinDuration(t, timer.CreatedAt, dbCreatedAt, time.Second)
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

	// Verify payload was serialized correctly using the index
	var dbPayload string
	query := `SELECT timer_payload FROM timers WHERE timer_namespace = ? AND timer_id = ? ALLOW FILTERING`
	scanErr := store.session.Query(query, timer.Namespace, timer.Id).Scan(&dbPayload)

	require.NoError(t, scanErr)
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

	// Verify retry policy was serialized correctly using the index
	var dbRetryPolicy string
	query := `SELECT timer_retry_policy FROM timers WHERE timer_namespace = ? AND timer_id = ? ALLOW FILTERING`
	scanErr := store.session.Query(query, timer.Namespace, timer.Id).Scan(&dbRetryPolicy)

	require.NoError(t, scanErr)
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

	// Verify nil fields are handled correctly using the index
	var dbPayload, dbRetryPolicy *string
	query := `SELECT timer_payload, timer_retry_policy FROM timers WHERE timer_namespace = ? AND timer_id = ? ALLOW FILTERING`
	scanErr := store.session.Query(query, timer.Namespace, timer.Id).Scan(&dbPayload, &dbRetryPolicy)

	require.NoError(t, scanErr)

	// Should be empty strings or null
	if dbPayload != nil {
		assert.Equal(t, "", *dbPayload)
	}
	if dbRetryPolicy != nil {
		assert.Equal(t, "", *dbRetryPolicy)
	}
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

	// Verify all timers were created by counting timer records in the shard
	var totalCount int
	countQuery := `SELECT COUNT(*) FROM timers WHERE shard_id = ? AND row_type = ? ALLOW FILTERING`
	scanErr := store.session.Query(countQuery, shardId, databases.RowTypeTimer).Scan(&totalCount)

	require.NoError(t, scanErr)

	assert.Equal(t, numGoroutines, successCount, "All concurrent timer creations should succeed")
	assert.Equal(t, numGoroutines, totalCount, "All timers should be found in database")
}
