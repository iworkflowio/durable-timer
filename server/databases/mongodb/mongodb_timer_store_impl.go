package mongodb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/iworkflowio/durable-timer/config"
	"github.com/iworkflowio/durable-timer/databases"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// MongoDBTimerStore implements TimerStore interface for MongoDB
type MongoDBTimerStore struct {
	client     *mongo.Client
	database   *mongo.Database
	collection *mongo.Collection
}

// NewMongoDBTimerStore creates a new MongoDB timer store
func NewMongoDBTimerStore(config *config.MongoDBConnectConfig) (databases.TimerStore, error) {
	// Build connection URI - use replica set config only for non-localhost connections
	var uri string

	// Check if authentication is configured
	hasAuth := config.Username != "" && config.Password != "" && config.AuthDatabase != ""

	if config.Host == "localhost" || config.Host == "127.0.0.1" {
		// For localhost connections (testing), use direct connection to bypass replica set discovery
		if hasAuth {
			uri = fmt.Sprintf("mongodb://%s:%s@%s:%d/%s?authSource=%s&directConnection=true",
				config.Username, config.Password, config.Host, config.Port, config.Database, config.AuthDatabase)
		} else {
			uri = fmt.Sprintf("mongodb://%s:%d/%s?directConnection=true",
				config.Host, config.Port, config.Database)
		}
	} else {
		// For production, use replica set configuration for transactions
		if hasAuth {
			uri = fmt.Sprintf("mongodb://%s:%s@%s:%d/%s?authSource=%s&replicaSet=timer-rs&readConcern=majority&w=majority",
				config.Username, config.Password, config.Host, config.Port, config.Database, config.AuthDatabase)
		} else {
			uri = fmt.Sprintf("mongodb://%s:%d/%s?replicaSet=timer-rs&readConcern=majority&w=majority",
				config.Host, config.Port, config.Database)
		}
	}

	// Configure client options
	clientOptions := options.Client().
		ApplyURI(uri).
		SetMaxPoolSize(config.MaxPoolSize).
		SetMinPoolSize(config.MinPoolSize).
		SetMaxConnIdleTime(config.ConnMaxIdleTime).
		SetConnectTimeout(10 * time.Second)

	// Create client and connect
	client, err := mongo.Connect(context.Background(), clientOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to MongoDB: %w", err)
	}

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = client.Ping(ctx, nil)
	if err != nil {
		client.Disconnect(context.Background())
		return nil, fmt.Errorf("failed to ping MongoDB: %w", err)
	}

	database := client.Database(config.Database)
	collection := database.Collection("timers")

	store := &MongoDBTimerStore{
		client:     client,
		database:   database,
		collection: collection,
	}

	return store, nil
}

// Close closes the MongoDB connection
func (m *MongoDBTimerStore) Close() error {
	if m.client != nil {
		return m.client.Disconnect(context.Background())
	}
	return nil
}

func (m *MongoDBTimerStore) ClaimShardOwnership(
	ctx context.Context, shardId int, ownerAddr string, metadata interface{},
) (shardVersion int64, retErr *databases.DbError) {
	// Convert ZeroUUID to high/low format for shard records
	zeroUuidHigh, zeroUuidLow := databases.UuidToHighLow(databases.ZeroUUID)

	// Serialize metadata to JSON
	var metadataJSON interface{}
	if metadata != nil {
		metadataBytes, err := json.Marshal(metadata)
		if err != nil {
			return 0, databases.NewGenericDbError("failed to marshal metadata", err)
		}
		metadataJSON = string(metadataBytes)
	} else {
		metadataJSON = nil
	}

	now := time.Now().UTC()

	// MongoDB document filter for shard record
	filter := bson.M{
		"shard_id":         shardId,
		"row_type":         databases.RowTypeShard,
		"timer_execute_at": databases.ZeroTimestamp,
		"timer_uuid_high":  zeroUuidHigh,
		"timer_uuid_low":   zeroUuidLow,
	}

	// First, try to read the current shard record
	var existingDoc bson.M
	err := m.collection.FindOne(ctx, filter).Decode(&existingDoc)

	if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
		return 0, databases.NewGenericDbError("failed to read shard record", err)
	}

	if errors.Is(err, mongo.ErrNoDocuments) {
		// Shard doesn't exist, create it with version 1
		newVersion := int64(1)

		shardDoc := bson.M{
			"shard_id":         shardId,
			"row_type":         databases.RowTypeShard,
			"timer_execute_at": databases.ZeroTimestamp,
			"timer_uuid_high":  zeroUuidHigh,
			"timer_uuid_low":   zeroUuidLow,
			"shard_version":    newVersion,
			"shard_owner_addr": ownerAddr,
			"shard_claimed_at": now,
			"shard_metadata":   metadataJSON,
		}

		_, err = m.collection.InsertOne(ctx, shardDoc)
		if err != nil {
			// Check if it's a duplicate key error (another instance created it concurrently)
			if isDuplicateKeyError(err) {
				// Try to read the existing record to return conflict info
				var conflictDoc bson.M
				conflictErr := m.collection.FindOne(ctx, filter).Decode(&conflictDoc)

				if conflictErr == nil {
					conflictInfo := &databases.ShardInfo{
						ShardId:      int64(shardId),
						OwnerAddr:    getStringFromBSON(conflictDoc, "shard_owner_addr"),
						ClaimedAt:    getTimeFromBSON(conflictDoc, "shard_claimed_at"),
						Metadata:     getStringFromBSON(conflictDoc, "shard_metadata"),
						ShardVersion: getInt64FromBSON(conflictDoc, "shard_version"),
					}
					return 0, databases.NewDbErrorOnShardConditionFail("failed to insert shard record due to concurrent insert", nil, conflictInfo)
				}
			}
			return 0, databases.NewGenericDbError("failed to insert new shard record", err)
		}

		// Successfully created new record
		return newVersion, nil
	}

	// Extract current version and update with optimistic concurrency control
	currentVersion := getInt64FromBSON(existingDoc, "shard_version")
	newVersion := currentVersion + 1

	// Update with version check for optimistic concurrency
	updateFilter := bson.M{
		"shard_id":         shardId,
		"row_type":         databases.RowTypeShard,
		"timer_execute_at": databases.ZeroTimestamp,
		"timer_uuid_high":  zeroUuidHigh,
		"timer_uuid_low":   zeroUuidLow,
		"shard_version":    currentVersion, // Only update if version matches
	}

	update := bson.M{
		"$set": bson.M{
			"shard_version":    newVersion,
			"shard_owner_addr": ownerAddr,
			"shard_claimed_at": now,
			"shard_metadata":   metadataJSON,
		},
	}

	result, err := m.collection.UpdateOne(ctx, updateFilter, update)
	if err != nil {
		return 0, databases.NewGenericDbError("failed to update shard record", err)
	}

	if result.MatchedCount == 0 {
		// Version changed concurrently, return conflict info
		conflictInfo := &databases.ShardInfo{
			ShardVersion: currentVersion, // We know it was at least this version
		}
		return 0, databases.NewDbErrorOnShardConditionFail("shard ownership claim failed due to concurrent modification", nil, conflictInfo)
	}

	return newVersion, nil
}

// Helper functions for BSON value extraction
func getStringFromBSON(doc bson.M, key string) string {
	if val, ok := doc[key]; ok && val != nil {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

func getInt64FromBSON(doc bson.M, key string) int64 {
	if val, ok := doc[key]; ok && val != nil {
		switch v := val.(type) {
		case int64:
			return v
		case int32:
			return int64(v)
		case int:
			return int64(v)
		}
	}
	return 0
}

func getTimeFromBSON(doc bson.M, key string) time.Time {
	if val, ok := doc[key]; ok && val != nil {
		if t, ok := val.(primitive.DateTime); ok {
			return t.Time()
		}
	}
	return time.Time{}
}

// isDuplicateKeyError checks if the error is a MongoDB duplicate key error
func isDuplicateKeyError(err error) bool {
	var writeErr mongo.WriteException
	if errors.As(err, &writeErr) {
		for _, writeError := range writeErr.WriteErrors {
			if writeError.Code == 11000 { // E11000 duplicate key error
				return true
			}
		}
	}
	return false
}

func (m *MongoDBTimerStore) CreateTimer(ctx context.Context, shardId int, shardVersion int64, namespace string, timer *databases.DbTimer) (err *databases.DbError) {
	// Convert the provided timer UUID to high/low format for predictable pagination
	timerUuidHigh, timerUuidLow := databases.UuidToHighLow(timer.TimerUuid)

	// Convert ZeroUUID to high/low format for shard records
	zeroUuidHigh, zeroUuidLow := databases.UuidToHighLow(databases.ZeroUUID)

	// Serialize payload and retry policy to JSON
	var payloadJSON, retryPolicyJSON interface{}

	if timer.Payload != nil {
		payloadBytes, marshalErr := json.Marshal(timer.Payload)
		if marshalErr != nil {
			return databases.NewGenericDbError("failed to marshal timer payload", marshalErr)
		}
		payloadJSON = string(payloadBytes)
	}

	if timer.RetryPolicy != nil {
		retryPolicyBytes, marshalErr := json.Marshal(timer.RetryPolicy)
		if marshalErr != nil {
			return databases.NewGenericDbError("failed to marshal timer retry policy", marshalErr)
		}
		retryPolicyJSON = string(retryPolicyBytes)
	}

	// Use MongoDB transaction to atomically check shard version and insert timer
	session, sessionErr := m.client.StartSession()
	if sessionErr != nil {
		return databases.NewGenericDbError("failed to start MongoDB session", sessionErr)
	}
	defer session.EndSession(ctx)

	var dbErr *databases.DbError
	transactionErr := mongo.WithSession(ctx, session, func(sc mongo.SessionContext) error {
		// Start transaction
		if startErr := session.StartTransaction(); startErr != nil {
			return startErr
		}

		// Read the shard to get current version
		shardFilter := bson.M{
			"shard_id":         shardId,
			"row_type":         databases.RowTypeShard,
			"timer_execute_at": databases.ZeroTimestamp,
			"timer_uuid_high":  zeroUuidHigh,
			"timer_uuid_low":   zeroUuidLow,
		}

		var shardDoc bson.M
		shardErr := m.collection.FindOne(sc, shardFilter).Decode(&shardDoc)
		if shardErr != nil {
			if errors.Is(shardErr, mongo.ErrNoDocuments) {
				// Shard doesn't exist
				conflictInfo := &databases.ShardInfo{
					ShardVersion: 0,
				}
				dbErr = databases.NewDbErrorOnShardConditionFail("shard not found during timer creation", nil, conflictInfo)
				return nil // Return from transaction function, will abort transaction
			}
			dbErr = databases.NewGenericDbError("failed to read shard", shardErr)
			return nil
		}

		// Get actual shard version and compare
		actualShardVersion := getInt64FromBSON(shardDoc, "shard_version")
		if actualShardVersion != shardVersion {
			// Version mismatch
			conflictInfo := &databases.ShardInfo{
				ShardVersion: actualShardVersion,
			}
			dbErr = databases.NewDbErrorOnShardConditionFail("shard version mismatch during timer creation", nil, conflictInfo)
			return nil // Return from transaction function, will abort transaction
		}

		// Shard version matches, proceed to upsert timer
		timerDoc := m.buildTimerDocumentForUpsert(shardId, timer, timerUuidHigh, timerUuidLow, payloadJSON, retryPolicyJSON)

		// Use UpdateOne with upsert to overwrite existing timer if it exists
		timerFilter := bson.M{
			"shard_id":        shardId,
			"row_type":        databases.RowTypeTimer,
			"timer_namespace": timer.Namespace,
			"timer_id":        timer.Id,
		}

		update := bson.M{"$set": timerDoc}
		opts := options.Update().SetUpsert(true)
		_, updateErr := m.collection.UpdateOne(sc, timerFilter, update, opts)
		if updateErr != nil {
			dbErr = databases.NewGenericDbError("failed to upsert timer", updateErr)
			return nil
		}

		// Commit transaction
		if commitErr := session.CommitTransaction(sc); commitErr != nil {
			dbErr = databases.NewGenericDbError("failed to commit transaction", commitErr)
			return nil
		}

		return nil
	})

	if transactionErr != nil {
		session.AbortTransaction(ctx)
		return databases.NewGenericDbError("transaction failed", transactionErr)
	}

	return dbErr
}

// buildTimerDocumentForUpsert creates a timer document for upsert operations (without _id field)
func (m *MongoDBTimerStore) buildTimerDocumentForUpsert(shardId int, timer *databases.DbTimer, timerUuidHigh, timerUuidLow int64, payloadJSON, retryPolicyJSON interface{}) bson.M {
	timerDoc := bson.M{
		"shard_id":                       shardId,
		"row_type":                       databases.RowTypeTimer,
		"timer_execute_at":               timer.ExecuteAt,
		"timer_uuid_high":                timerUuidHigh,
		"timer_uuid_low":                 timerUuidLow,
		"timer_id":                       timer.Id,
		"timer_namespace":                timer.Namespace,
		"timer_callback_url":             timer.CallbackUrl,
		"timer_callback_timeout_seconds": timer.CallbackTimeoutSeconds,
		"timer_created_at":               timer.CreatedAt,
		"timer_attempts":                 0,
	}

	if payloadJSON != nil {
		timerDoc["timer_payload"] = payloadJSON
	}

	if retryPolicyJSON != nil {
		timerDoc["timer_retry_policy"] = retryPolicyJSON
	}

	return timerDoc
}

func (m *MongoDBTimerStore) CreateTimerNoLock(ctx context.Context, shardId int, namespace string, timer *databases.DbTimer) (err *databases.DbError) {
	// Convert the provided timer UUID to high/low format for predictable pagination
	timerUuidHigh, timerUuidLow := databases.UuidToHighLow(timer.TimerUuid)

	// Serialize payload and retry policy to JSON
	var payloadJSON, retryPolicyJSON interface{}

	if timer.Payload != nil {
		payloadBytes, marshalErr := json.Marshal(timer.Payload)
		if marshalErr != nil {
			return databases.NewGenericDbError("failed to marshal timer payload", marshalErr)
		}
		payloadJSON = string(payloadBytes)
	}

	if timer.RetryPolicy != nil {
		retryPolicyBytes, marshalErr := json.Marshal(timer.RetryPolicy)
		if marshalErr != nil {
			return databases.NewGenericDbError("failed to marshal timer retry policy", marshalErr)
		}
		retryPolicyJSON = string(retryPolicyBytes)
	}

	// Create timer document without _id for upsert
	timerDoc := m.buildTimerDocumentForUpsert(shardId, timer, timerUuidHigh, timerUuidLow, payloadJSON, retryPolicyJSON)

	// Use UpdateOne with upsert to overwrite existing timer if it exists (no locking or version checking)
	timerFilter := bson.M{
		"shard_id":        shardId,
		"row_type":        databases.RowTypeTimer,
		"timer_namespace": timer.Namespace,
		"timer_id":        timer.Id,
	}

	update := bson.M{"$set": timerDoc}
	opts := options.Update().SetUpsert(true)
	_, updateErr := m.collection.UpdateOne(ctx, timerFilter, update, opts)
	if updateErr != nil {
		return databases.NewGenericDbError("failed to upsert timer", updateErr)
	}

	return nil
}

func (c *MongoDBTimerStore) GetTimersUpToTimestamp(ctx context.Context, shardId int, request *databases.RangeGetTimersRequest) (*databases.RangeGetTimersResponse, *databases.DbError) {
	// Query timers up to the specified timestamp, ordered by execution time
	filter := bson.M{
		"shard_id":         shardId,
		"row_type":         databases.RowTypeTimer,
		"timer_execute_at": bson.M{"$lte": request.UpToTimestamp},
	}

	// Create find options with sorting and limit
	findOptions := options.Find().
		SetSort(bson.D{
			{"timer_execute_at", 1},
			{"timer_uuid_high", 1},
			{"timer_uuid_low", 1},
		}).
		SetLimit(int64(request.Limit))

	cursor, err := c.collection.Find(ctx, filter, findOptions)
	if err != nil {
		return nil, databases.NewGenericDbError("failed to query timers", err)
	}
	defer cursor.Close(ctx)

	var timers []*databases.DbTimer

	for cursor.Next(ctx) {
		var doc bson.M
		if err := cursor.Decode(&doc); err != nil {
			return nil, databases.NewGenericDbError("failed to decode timer document", err)
		}

		// Convert UUID high/low back to UUID
		timerUuidHigh := doc["timer_uuid_high"].(int64)
		timerUuidLow := doc["timer_uuid_low"].(int64)
		timerUuid := databases.HighLowToUuid(timerUuidHigh, timerUuidLow)

		// Parse JSON payload and retry policy
		var payload interface{}
		var retryPolicy interface{}

		if payloadStr, ok := doc["timer_payload"].(string); ok && payloadStr != "" {
			if err := json.Unmarshal([]byte(payloadStr), &payload); err != nil {
				return nil, databases.NewGenericDbError("failed to unmarshal timer payload", err)
			}
		}

		if retryPolicyStr, ok := doc["timer_retry_policy"].(string); ok && retryPolicyStr != "" {
			if err := json.Unmarshal([]byte(retryPolicyStr), &retryPolicy); err != nil {
				return nil, databases.NewGenericDbError("failed to unmarshal timer retry policy", err)
			}
		}

		timer := &databases.DbTimer{
			Id:                     doc["timer_id"].(string),
			TimerUuid:              timerUuid,
			Namespace:              doc["timer_namespace"].(string),
			ExecuteAt:              doc["timer_execute_at"].(primitive.DateTime).Time(),
			CallbackUrl:            doc["timer_callback_url"].(string),
			Payload:                payload,
			RetryPolicy:            retryPolicy,
			CallbackTimeoutSeconds: doc["timer_callback_timeout_seconds"].(int32),
			CreatedAt:              doc["timer_created_at"].(primitive.DateTime).Time(),
		}

		timers = append(timers, timer)
	}

	if err := cursor.Err(); err != nil {
		return nil, databases.NewGenericDbError("cursor iteration failed", err)
	}

	return &databases.RangeGetTimersResponse{
		Timers: timers,
	}, nil
}

func (c *MongoDBTimerStore) DeleteTimersUpToTimestampWithBatchInsert(ctx context.Context, shardId int, shardVersion int64, request *databases.RangeDeleteTimersRequest, TimersToInsert []*databases.DbTimer) (*databases.RangeDeleteTimersResponse, *databases.DbError) {
	// Convert start and end UUIDs to high/low format for precise range selection
	startUuidHigh, startUuidLow := databases.UuidToHighLow(request.StartTimeUuid)
	endUuidHigh, endUuidLow := databases.UuidToHighLow(request.EndTimeUuid)

	// Convert ZeroUUID to high/low format for shard records
	zeroUuidHigh, zeroUuidLow := databases.UuidToHighLow(databases.ZeroUUID)

	// Use MongoDB transaction to atomically check shard version, delete timers, and insert new ones
	session, sessionErr := c.client.StartSession()
	if sessionErr != nil {
		return nil, databases.NewGenericDbError("failed to start MongoDB session", sessionErr)
	}
	defer session.EndSession(ctx)

	var result *databases.RangeDeleteTimersResponse
	var dbErr *databases.DbError

	transactionErr := mongo.WithSession(ctx, session, func(sc mongo.SessionContext) error {
		// Start transaction
		if startErr := session.StartTransaction(); startErr != nil {
			return startErr
		}

		// Read the shard to verify version
		shardFilter := bson.M{
			"shard_id":         shardId,
			"row_type":         databases.RowTypeShard,
			"timer_execute_at": databases.ZeroTimestamp,
			"timer_uuid_high":  zeroUuidHigh,
			"timer_uuid_low":   zeroUuidLow,
		}

		var shardDoc bson.M
		shardErr := c.collection.FindOne(sc, shardFilter).Decode(&shardDoc)
		if shardErr != nil {
			if errors.Is(shardErr, mongo.ErrNoDocuments) {
				// Shard doesn't exist
				dbErr = databases.NewGenericDbError("shard record does not exist", nil)
				return nil
			}
			dbErr = databases.NewGenericDbError("failed to read shard", shardErr)
			return nil
		}

		// Get actual shard version and compare
		actualShardVersion := getInt64FromBSON(shardDoc, "shard_version")
		if actualShardVersion != shardVersion {
			// Version mismatch
			conflictInfo := &databases.ShardInfo{
				ShardVersion: actualShardVersion,
			}
			dbErr = databases.NewDbErrorOnShardConditionFail("shard version mismatch during delete and insert operation", nil, conflictInfo)
			return nil
		}

		// Count timers to be deleted
		deleteFilter := bson.M{
			"shard_id": shardId,
			"row_type": databases.RowTypeTimer,
			"$or": []bson.M{
				{
					"timer_execute_at": bson.M{"$gt": request.StartTimestamp},
				},
				{
					"timer_execute_at": request.StartTimestamp,
					"timer_uuid_high":  bson.M{"$gt": startUuidHigh},
				},
				{
					"timer_execute_at": request.StartTimestamp,
					"timer_uuid_high":  startUuidHigh,
					"timer_uuid_low":   bson.M{"$gte": startUuidLow},
				},
			},
		}

		// Add upper bound constraint
		deleteFilter["$and"] = []bson.M{
			{
				"$or": []bson.M{
					{
						"timer_execute_at": bson.M{"$lt": request.EndTimestamp},
					},
					{
						"timer_execute_at": request.EndTimestamp,
						"timer_uuid_high":  bson.M{"$lt": endUuidHigh},
					},
					{
						"timer_execute_at": request.EndTimestamp,
						"timer_uuid_high":  endUuidHigh,
						"timer_uuid_low":   bson.M{"$lte": endUuidLow},
					},
				},
			},
		}

		deleteResult, deleteErr := c.collection.DeleteMany(sc, deleteFilter)
		if deleteErr != nil {
			dbErr = databases.NewGenericDbError("failed to delete timers", deleteErr)
			return nil
		}

		// Insert new timers
		for _, timer := range TimersToInsert {
			timerUuidHigh, timerUuidLow := databases.UuidToHighLow(timer.TimerUuid)

			// Serialize payload and retry policy to JSON
			var payloadJSON, retryPolicyJSON interface{}

			if timer.Payload != nil {
				payloadBytes, marshalErr := json.Marshal(timer.Payload)
				if marshalErr != nil {
					dbErr = databases.NewGenericDbError("failed to marshal timer payload", marshalErr)
					return nil
				}
				payloadJSON = string(payloadBytes)
			}

			if timer.RetryPolicy != nil {
				retryPolicyBytes, marshalErr := json.Marshal(timer.RetryPolicy)
				if marshalErr != nil {
					dbErr = databases.NewGenericDbError("failed to marshal timer retry policy", marshalErr)
					return nil
				}
				retryPolicyJSON = string(retryPolicyBytes)
			}

			timerDoc := c.buildTimerDocumentForUpsert(shardId, timer, timerUuidHigh, timerUuidLow, payloadJSON, retryPolicyJSON)

			_, insertErr := c.collection.InsertOne(sc, timerDoc)
			if insertErr != nil {
				dbErr = databases.NewGenericDbError("failed to insert timer", insertErr)
				return nil
			}
		}

		// Commit transaction
		if commitErr := session.CommitTransaction(sc); commitErr != nil {
			dbErr = databases.NewGenericDbError("failed to commit transaction", commitErr)
			return nil
		}

		result = &databases.RangeDeleteTimersResponse{
			DeletedCount: int(deleteResult.DeletedCount),
		}

		return nil
	})

	if transactionErr != nil {
		session.AbortTransaction(ctx)
		return nil, databases.NewGenericDbError("transaction failed", transactionErr)
	}

	if dbErr != nil {
		return nil, dbErr
	}

	return result, nil
}

func (c *MongoDBTimerStore) BatchInsertTimers(ctx context.Context, shardId int, shardVersion int64, TimersToInsert []*databases.DbTimer) *databases.DbError {
	//TODO implement me
	panic("implement me")
}

func (c *MongoDBTimerStore) UpdateTimer(ctx context.Context, shardId int, shardVersion int64, namespace string, request *databases.UpdateDbTimerRequest) (err *databases.DbError) {
	//TODO implement me
	panic("implement me")
}

func (c *MongoDBTimerStore) GetTimer(ctx context.Context, shardId int, namespace string, timerId string) (timer *databases.DbTimer, err *databases.DbError) {
	//TODO implement me
	panic("implement me")
}

func (c *MongoDBTimerStore) DeleteTimer(ctx context.Context, shardId int, shardVersion int64, namespace string, timerId string) *databases.DbError {
	//TODO implement me
	panic("implement me")
}
