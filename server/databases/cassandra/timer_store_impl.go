package cassandra

import (
	"context"
	"fmt"
	"time"

	"github.com/gocql/gocql"
	genapi "github.com/iworkflowio/durable-timer/genapi/go"
	"github.com/google/uuid"
)

// CassandraTimerStore implements TimerStore interface for Cassandra
type CassandraTimerStore struct {
	session *gocql.Session
}

// NewCassandraTimerStore creates a new Cassandra timer store
func NewCassandraTimerStore(hosts []string, keyspace string) (*CassandraTimerStore, error) {
	cluster := gocql.NewCluster(hosts...)
	cluster.Keyspace = keyspace
	cluster.Consistency = gocql.Quorum
	cluster.Timeout = 10 * time.Second

	session, err := cluster.CreateSession()
	if err != nil {
		return nil, fmt.Errorf("failed to create Cassandra session: %w", err)
	}

	store := &CassandraTimerStore{
		session: session,
	}

	return store, nil
}

// Close closes the Cassandra session
func (c *CassandraTimerStore) Close() error {
	if c.session != nil {
		c.session.Close()
	}
	return nil
}



// CreateTimer creates a new timer
func (c *CassandraTimerStore) CreateTimer(
	ctx context.Context,
	shardId int, timer *genapi.Timer,
) (alreadyExists bool, err error) {
	// Generate UUID for uniqueness
	timerUuid := uuid.New()

	// Check if timer already exists
	existingTimer, notExists, err := c.GetTimer(ctx, shardId, timer.Id)
	if err != nil {
		return false, fmt.Errorf("failed to check existing timer: %w", err)
	}
	if !notExists {
		// Timer already exists - check if it's the same
		if existingTimer.ExecuteAt.Equal(timer.ExecuteAt) &&
			existingTimer.CallbackUrl == timer.CallbackUrl {
			return true, nil // Same timer, return success
		}
		return true, fmt.Errorf("timer already exists with different parameters")
	}

	// Convert retry policy and payload to JSON strings
	retryPolicyJSON := ""
	if timer.RetryPolicy != nil {
		// In a real implementation, you'd marshal the retry policy to JSON
		retryPolicyJSON = fmt.Sprintf(`{"maxRetries":%d,"initialInterval":"%s"}`, 
			3, "30s") // placeholder
	}

	payloadJSON := ""
	if timer.Payload != nil {
		// In a real implementation, you'd marshal the payload to JSON
		payloadJSON = `{}` // placeholder
	}

	// Insert new timer
	query := `INSERT INTO timers (
		shard_id, execute_at, timer_uuid, timer_id, group_id, 
		callback_url, payload, retry_policy, callback_timeout, 
		created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	now := time.Now()
	callbackTimeout := "30s"
	if timer.CallbackTimeout != nil {
		callbackTimeout = *timer.CallbackTimeout
	}

	err = c.session.Query(query,
		shardId,
		timer.ExecuteAt,
		timerUuid,
		timer.Id,
		timer.GroupId,
		timer.CallbackUrl,
		payloadJSON,
		retryPolicyJSON,
		callbackTimeout,
		now,
		now,
	).WithContext(ctx).Exec()

	if err != nil {
		return false, fmt.Errorf("failed to insert timer: %w", err)
	}

	return false, nil
}

// GetTimersUpToTimestamp retrieves timers ready for execution
func (c *CassandraTimerStore) GetTimersUpToTimestamp(
	ctx context.Context,
	shardId int, request *RangeGetTimersRequest,
) (*RangeGetTimersResponse, error) {

	query := `SELECT shard_id, execute_at, timer_uuid, timer_id, group_id, 
		callback_url, payload, retry_policy, callback_timeout, 
		created_at, updated_at 
		FROM timers 
		WHERE shard_id = ? AND execute_at <= ? 
		ORDER BY execute_at ASC`

	if request.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", request.Limit)
	}

	iter := c.session.Query(query, shardId, request.UpToTimestamp).WithContext(ctx).Iter()
	defer iter.Close()

	var timers []*genapi.Timer
	
	var (
		dbShardId       int
		dbExecuteAt     time.Time
		dbTimerUuid     gocql.UUID
		dbTimerId       string
		dbGroupId       string
		dbCallbackUrl   string
		dbPayload       string
		dbRetryPolicy   string
		dbCallbackTimeout string
		dbCreatedAt     time.Time
		dbUpdatedAt     time.Time
	)

	for iter.Scan(&dbShardId, &dbExecuteAt, &dbTimerUuid, &dbTimerId, &dbGroupId,
		&dbCallbackUrl, &dbPayload, &dbRetryPolicy, &dbCallbackTimeout,
		&dbCreatedAt, &dbUpdatedAt) {
		
		timer := &genapi.Timer{
			Id:              dbTimerId,
			GroupId:         dbGroupId,
			ExecuteAt:       dbExecuteAt,
			CallbackUrl:     dbCallbackUrl,
			CreatedAt:       dbCreatedAt,
			UpdatedAt:       dbUpdatedAt,
		}

		if dbCallbackTimeout != "" {
			timer.CallbackTimeout = &dbCallbackTimeout
		}

		// TODO: Parse JSON payload and retry policy
		// timer.Payload = parseJSON(dbPayload)
		// timer.RetryPolicy = parseRetryPolicy(dbRetryPolicy)

		timers = append(timers, timer)
	}

	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("failed to iterate timers: %w", err)
	}

	return &RangeGetTimersResponse{
		Timers: timers,
	}, nil
}

// DeleteTimersUpToTimestamp deletes timers in the specified range
func (c *CassandraTimerStore) DeleteTimersUpToTimestamp(
	ctx context.Context,
	shardId int, request *RangeDeleteTimersRequest,
) (*RangeDeleteTimersResponse, error) {
	// First, get the timers to delete
	getRequest := &RangeGetTimersRequest{
		UpToTimestamp: request.EndTimestamp,
		Limit:         0, // No limit
	}

	response, err := c.GetTimersUpToTimestamp(ctx, shardId, getRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to get timers for deletion: %w", err)
	}

	deletedCount := 0
	
	// Delete each timer individually
	for _, timer := range response.Timers {
		if timer.ExecuteAt.After(request.StartTimestamp) || timer.ExecuteAt.Equal(request.StartTimestamp) {
			err := c.DeleteTimer(ctx, shardId, timer.Id)
			if err != nil {
				return nil, fmt.Errorf("failed to delete timer %s: %w", timer.Id, err)
			}
			deletedCount++
		}
	}

	return &RangeDeleteTimersResponse{
		DeletedCount: deletedCount,
	}, nil
}

// UpdateTimer updates an existing timer
func (c *CassandraTimerStore) UpdateTimer(
	ctx context.Context,
	shardId int, timerId string,
	request *genapi.UpdateTimerRequest,
) (notExists bool, err error) {
	// Check if timer exists
	_, notExists, err = c.GetTimer(ctx, shardId, timerId)
	if err != nil {
		return false, fmt.Errorf("failed to check timer existence: %w", err)
	}
	if notExists {
		return true, nil
	}

	// TODO: Implement update logic
	// This requires deleting the old timer and inserting a new one
	// because execute_at might change (which is part of the primary key)
	
	return false, fmt.Errorf("UpdateTimer not yet implemented")
}

// GetTimer retrieves a specific timer by shardId and timerId
func (c *CassandraTimerStore) GetTimer(
	ctx context.Context,
	shardId int, timerId string,
) (timer *genapi.Timer, notExists bool, err error) {
	// We need to use the secondary index since we don't have execute_at
	query := `SELECT shard_id, execute_at, timer_uuid, timer_id, group_id, 
		callback_url, payload, retry_policy, callback_timeout, 
		created_at, updated_at 
		FROM timers 
		WHERE shard_id = ? AND timer_id = ?`

	var (
		dbShardId       int
		dbExecuteAt     time.Time
		dbTimerUuid     gocql.UUID
		dbTimerId       string
		dbGroupId       string
		dbCallbackUrl   string
		dbPayload       string
		dbRetryPolicy   string
		dbCallbackTimeout string
		dbCreatedAt     time.Time
		dbUpdatedAt     time.Time
	)

	err = c.session.Query(query, shardId, timerId).WithContext(ctx).
		Scan(&dbShardId, &dbExecuteAt, &dbTimerUuid, &dbTimerId, &dbGroupId,
			&dbCallbackUrl, &dbPayload, &dbRetryPolicy, &dbCallbackTimeout,
			&dbCreatedAt, &dbUpdatedAt)

	if err != nil {
		if err == gocql.ErrNotFound {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("failed to get timer: %w", err)
	}

	timer = &genapi.Timer{
		Id:              dbTimerId,
		GroupId:         dbGroupId,
		ExecuteAt:       dbExecuteAt,
		CallbackUrl:     dbCallbackUrl,
		CreatedAt:       dbCreatedAt,
		UpdatedAt:       dbUpdatedAt,
	}

	if dbCallbackTimeout != "" {
		timer.CallbackTimeout = &dbCallbackTimeout
	}

	// TODO: Parse JSON payload and retry policy
	// timer.Payload = parseJSON(dbPayload)
	// timer.RetryPolicy = parseRetryPolicy(dbRetryPolicy)

	return timer, false, nil
}

// DeleteTimer deletes a specific timer
func (c *CassandraTimerStore) DeleteTimer(
	ctx context.Context,
	shardId int, timerId string,
) error {
	// First get the timer to find its complete primary key
	timer, notExists, err := c.GetTimer(ctx, shardId, timerId)
	if err != nil {
		return fmt.Errorf("failed to get timer for deletion: %w", err)
	}
	if notExists {
		return nil // Timer doesn't exist, nothing to delete
	}

	// We need the timer_uuid to delete, but we don't have it from GetTimer
	// This is a limitation of our current design - we'd need to store timer_uuid
	// or restructure the delete operation
	
	// For now, delete by shard_id and timer_id using the index
	query := `DELETE FROM timers WHERE shard_id = ? AND timer_id = ?`
	
	err = c.session.Query(query, shardId, timerId).WithContext(ctx).Exec()
	if err != nil {
		return fmt.Errorf("failed to delete timer: %w", err)
	}

	return nil
} 