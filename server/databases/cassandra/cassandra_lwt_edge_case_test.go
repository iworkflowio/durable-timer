package cassandra

import (
	"context"
	"testing"
	"time"

	"github.com/gocql/gocql"
	"github.com/iworkflowio/durable-timer/databases"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCassandraLWTBatchEdgeCase tests the specific edge case where we combine:
// 1. A shard version check (which should succeed)
// 2. A delete operation on a non-existing timer (which should fail with IF EXISTS)
// This test verifies our understanding of how Cassandra returns the [applied] field in such cases.
func TestCassandraLWTBatchEdgeCase(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	shardId := 1

	// Step 1: Create a shard record first
	ownerAddr := "owner-1"
	_, shardInfo, err := store.ClaimShardOwnership(ctx, shardId, ownerAddr)
	require.Nil(t, err)
	shardVersion := shardInfo.ShardVersion
	require.Nil(t, err)
	require.Equal(t, int64(1), shardVersion)

	// Step 2: Set up the exact same batch pattern as DeleteTimer uses
	zeroUuidHigh, zeroUuidLow := databases.UuidToHighLow(databases.ZeroUUID)

	// Create fake timer coordinates for a non-existing timer
	fakeExecuteAt := time.Now().UTC().Add(5 * time.Minute)
	fakeUuidHigh := int64(12345)
	fakeUuidLow := int64(67890)

	// Create the same batch pattern as DeleteTimer method
	batch := store.session.NewBatch(gocql.LoggedBatch).WithContext(ctx)

	// Add shard version check (this should succeed since shard exists with correct version)
	checkVersionQuery := `UPDATE timers SET shard_version = ? WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid_high = ? AND timer_uuid_low = ? IF shard_version = ?`
	batch.Query(checkVersionQuery, shardVersion, shardId, databases.RowTypeShard, databases.ZeroTimestamp, zeroUuidHigh, zeroUuidLow, shardVersion)

	// Add DELETE statement for a non-existing timer with IF EXISTS (this should fail)
	deleteQuery := `DELETE FROM timers WHERE shard_id = ? AND row_type = ? AND timer_execute_at = ? AND timer_uuid_high = ? AND timer_uuid_low = ? IF EXISTS`
	batch.Query(deleteQuery, shardId, databases.RowTypeTimer, fakeExecuteAt, fakeUuidHigh, fakeUuidLow)

	// Execute the batch and capture results
	previous := make(map[string]interface{})
	applied, iter, batchErr := store.session.MapExecuteBatchCAS(batch, previous)
	if iter != nil {
		iter.Close()
	}

	// Verify no execution error
	require.Nil(t, batchErr, "Batch execution should not return an error")

	// The batch should not be applied because the DELETE IF EXISTS failed
	assert.False(t, applied, "Batch should not be applied due to non-existing timer")

	// Debug: Print all returned values for analysis
	t.Logf("Batch applied: %v", applied)
	t.Logf("Previous map contents:")
	for key, value := range previous {
		t.Logf("  %s: %v (type: %T)", key, value, value)
	}

	// Also check for shard_version field to ensure shard check logic works
	if shardVersionValue, exists := previous["shard_version"]; exists {
		t.Logf("Found shard_version field: %v", shardVersionValue)
		if shardVersionValue != nil {
			// This means shard version check succeeded but returned the current value
			currentVersion := shardVersionValue.(int64)
			assert.Equal(t, shardVersion, currentVersion, "Returned shard version should match expected")
		}
	} else {
		t.Logf("No shard_version field found in previous map")
	}
}