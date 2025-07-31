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
	namespace := "test_namespace"

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

	// Verify all timers were created by counting timer records in the shard
	var totalCount int
	countQuery := `SELECT COUNT(*) FROM timers WHERE shard_id = ? AND row_type = ? ALLOW FILTERING`
	scanErr := store.session.Query(countQuery, shardId, databases.RowTypeTimer).Scan(&totalCount)

	require.NoError(t, scanErr)

	assert.Equal(t, numGoroutines, successCount, "All concurrent timer creations should succeed")
	assert.Equal(t, numGoroutines, totalCount, "All timers should be found in database")
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
	               WHERE shard_id = ? AND row_type = ? AND timer_namespace = ? AND timer_id = ? ALLOW FILTERING`
	scanErr := store.session.Query(countQuery, shardId, databases.RowTypeTimer, namespace, timerId).Scan(&count1)
	require.NoError(t, scanErr)
	assert.Equal(t, 1, count1, "After same UUID + same execute_at: Cassandra should have 1 timer")

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

	// 5. Verify count = 2 (Cassandra allows different execute_at with same timer_id)
	var count2 int
	scanErr = store.session.Query(countQuery, shardId, databases.RowTypeTimer, namespace, timerId).Scan(&count2)
	require.NoError(t, scanErr)
	assert.Equal(t, 2, count2, "After same UUID + different execute_at: Cassandra should have 2 timers (clustering key allows this)")

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

	// 7. Verify count = 3 (Cassandra allows different UUID + execute_at combinations)
	var count3 int
	scanErr = store.session.Query(countQuery, shardId, databases.RowTypeTimer, namespace, timerId).Scan(&count3)
	require.NoError(t, scanErr)
	assert.Equal(t, 3, count3, "After different UUID + different execute_at: Cassandra should have 3 timers (clustering key allows this)")

	// Verify we can find all timers with their different callbacks
	selectQuery := `SELECT timer_callback_url, timer_payload, timer_execute_at FROM timers 
	                WHERE shard_id = ? AND row_type = ? AND timer_namespace = ? AND timer_id = ? ALLOW FILTERING`
	iter := store.session.Query(selectQuery, shardId, databases.RowTypeTimer, namespace, timerId).Iter()

	foundCallbacks := make(map[string]bool)
	var callbackUrl, payload string
	var executeAt time.Time
	for iter.Scan(&callbackUrl, &payload, &executeAt) {
		foundCallbacks[callbackUrl] = true
	}
	require.NoError(t, iter.Close())

	// Should find all three different callback URLs
	assert.True(t, foundCallbacks["https://updated1.com/callback"], "Should find updated1 callback")
	assert.True(t, foundCallbacks["https://updated2.com/callback"], "Should find updated2 callback")
	assert.True(t, foundCallbacks["https://updated3.com/callback"], "Should find updated3 callback")
	assert.Equal(t, 3, len(foundCallbacks), "Should have exactly 3 different callbacks")
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
	          WHERE shard_id = ? AND row_type = ? AND timer_namespace = ? AND timer_id = ? ALLOW FILTERING`

	scanErr := store.session.Query(query, shardId, databases.RowTypeTimer, originalTimer.Namespace, originalTimer.Id).
		Scan(&dbCallbackUrl, &dbRetryPolicy)

	require.NoError(t, scanErr)
	assert.Equal(t, "https://original-nolock.com/callback", dbCallbackUrl)
	assert.Contains(t, dbRetryPolicy, "maxAttempts")

	// Create updated timer with same ID (should create new record due to different UUID)
	updatedTimer := &databases.DbTimer{
		Id:                     "duplicate-timer-nolock",                                                 // Same ID
		TimerUuid:              databases.GenerateTimerUUID(namespace, "duplicate-timer-nolock-updated"), // Different UUID
		Namespace:              namespace,
		ExecuteAt:              time.Now().Add(15 * time.Minute),                                    // Different execution time
		CallbackUrl:            "https://updated-nolock.com/callback",                               // Different callback
		CallbackTimeoutSeconds: 60,                                                                  // Different timeout
		RetryPolicy:            map[string]interface{}{"maxAttempts": 5, "strategy": "exponential"}, // Different retry policy
		CreatedAt:              time.Now(),
	}

	updateErr := store.CreateTimerNoLock(ctx, shardId, namespace, updatedTimer)
	assert.Nil(t, updateErr, "CreateTimerNoLock should succeed (Cassandra allows multiple UUIDs)")

	// Cassandra behavior: multiple records with different UUIDs are allowed
	var count int
	countQuery := `SELECT COUNT(*) FROM timers 
	               WHERE shard_id = ? AND row_type = ? AND timer_namespace = ? AND timer_id = ? ALLOW FILTERING`
	scanErr = store.session.Query(countQuery, shardId, databases.RowTypeTimer, updatedTimer.Namespace, updatedTimer.Id).
		Scan(&count)

	require.NoError(t, scanErr)
	assert.GreaterOrEqual(t, count, 1, "Should have at least one timer record")

	// Verify we can find the updated timer
	iter := store.session.Query(query, shardId, databases.RowTypeTimer, updatedTimer.Namespace, updatedTimer.Id).Iter()
	var foundUpdated bool
	for iter.Scan(&dbCallbackUrl, &dbRetryPolicy) {
		if dbCallbackUrl == "https://updated-nolock.com/callback" {
			foundUpdated = true
			assert.Contains(t, dbRetryPolicy, "exponential", "Should find updated timer with new retry policy")
		}
	}
	require.NoError(t, iter.Close())
	assert.True(t, foundUpdated, "Should find the updated timer in Cassandra")
}
