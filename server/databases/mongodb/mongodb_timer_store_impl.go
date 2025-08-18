package mongodb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
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
	ctx context.Context,
	shardId int,
	ownerAddr string,
) (prevShardInfo, currentShardInfo *databases.ShardInfo, err *databases.DbError) {
	// Use ZeroUUID for shard records
	zeroUuid := databases.ZeroUUID

	now := time.Now().UTC()

	// MongoDB document filter for shard record
	filter := bson.M{
		"shard_id":         shardId,
		"row_type":         databases.RowTypeShard,
		"timer_execute_at": databases.ZeroTimestamp,
		"timer_uuid":       zeroUuid,
	}

	// First, try to read the current shard record
	var existingDoc bson.M
	findErr := m.collection.FindOne(ctx, filter).Decode(&existingDoc)

	if findErr != nil && !errors.Is(findErr, mongo.ErrNoDocuments) {
		return nil, nil, databases.NewGenericDbError("failed to read shard record", findErr)
	}

	if errors.Is(findErr, mongo.ErrNoDocuments) {
		// Shard doesn't exist, create it with version 1
		newVersion := int64(1)

		// Initialize with default metadata
		defaultMetadata := databases.ShardMetadata{}
		metadataJSON, marshalErr := json.Marshal(defaultMetadata)
		if marshalErr != nil {
			return nil, nil, databases.NewGenericDbError("failed to marshal default metadata", marshalErr)
		}

		shardDoc := bson.M{
			"shard_id":         shardId,
			"row_type":         databases.RowTypeShard,
			"timer_execute_at": databases.ZeroTimestamp,
			"timer_uuid":       zeroUuid,
			"shard_version":    newVersion,
			"shard_owner_addr": ownerAddr,
			"shard_claimed_at": now,
			"shard_metadata":   string(metadataJSON),
		}

		_, insertErr := m.collection.InsertOne(ctx, shardDoc)
		if insertErr != nil {
			// Check if it's a duplicate key error (another instance created it concurrently)
			if isDuplicateKeyError(insertErr) {
				return nil, nil, databases.NewDbErrorOnShardConditionFail("failed to insert shard record due to concurrent insert", insertErr)
			}
			return nil, nil, databases.NewGenericDbError("failed to insert new shard record", insertErr)
		}

		// Successfully created new record, return nil for prevShardInfo and current info
		currentShardInfo = &databases.ShardInfo{
			ShardId:      int64(shardId),
			OwnerAddr:    ownerAddr,
			ShardVersion: newVersion,
			Metadata:     defaultMetadata,
			ClaimedAt:    now,
		}
		return nil, currentShardInfo, nil
	}

	// Extract current shard info for prevShardInfo
	prevShardInfo = extractShardInfoFromMongoDoc(existingDoc, int64(shardId))

	// Extract current version and update with optimistic concurrency control
	currentVersion := getInt64FromBSON(existingDoc, "shard_version")
	newVersion := currentVersion + 1

	// Update with version check for optimistic concurrency (preserve existing metadata)
	updateFilter := bson.M{
		"shard_id":         shardId,
		"row_type":         databases.RowTypeShard,
		"timer_execute_at": databases.ZeroTimestamp,
		"timer_uuid":       zeroUuid,
		"shard_version":    currentVersion, // Only update if version matches
	}

	update := bson.M{
		"$set": bson.M{
			"shard_version":    newVersion,
			"shard_owner_addr": ownerAddr,
			"shard_claimed_at": now,
			// Note: shard_metadata is NOT updated - preserved from existing record
		},
	}

	result, updateErr := m.collection.UpdateOne(ctx, updateFilter, update)
	if updateErr != nil {
		return nil, nil, databases.NewGenericDbError("failed to update shard record", updateErr)
	}

	if result.MatchedCount == 0 {
		// Don't read conflict info to avoid expensive extra query
		return nil, nil, databases.NewDbErrorOnShardConditionFail("shard ownership claim failed due to concurrent modification", nil)
	}

	// Successfully updated, return both previous and current shard info
	currentShardInfo = &databases.ShardInfo{
		ShardId:      int64(shardId),
		OwnerAddr:    ownerAddr,
		ShardVersion: newVersion,
		Metadata:     prevShardInfo.Metadata, // Preserve existing metadata
		ClaimedAt:    now,
	}

	return prevShardInfo, currentShardInfo, nil
}

func (m *MongoDBTimerStore) UpdateShardMetadata(
	ctx context.Context,
	shardId int,
	shardVersion int64,
	metadata databases.ShardMetadata,
) (err *databases.DbError) {
	// Use ZeroUUID for shard records
	zeroUuid := databases.ZeroUUID

	// Marshal metadata to JSON
	metadataJSON, marshalErr := json.Marshal(metadata)
	if marshalErr != nil {
		return databases.NewGenericDbError("failed to marshal shard metadata", marshalErr)
	}

	// MongoDB document filter for shard record with version check
	filter := bson.M{
		"shard_id":         shardId,
		"row_type":         databases.RowTypeShard,
		"timer_execute_at": databases.ZeroTimestamp,
		"timer_uuid":       zeroUuid,
		"shard_version":    shardVersion, // Only update if version matches
	}

	// Update shard metadata using optimistic concurrency control
	update := bson.M{
		"$set": bson.M{
			"shard_metadata": string(metadataJSON),
		},
	}

	result, updateErr := m.collection.UpdateOne(ctx, filter, update)
	if updateErr != nil {
		return databases.NewGenericDbError("failed to update shard metadata", updateErr)
	}

	if result.MatchedCount == 0 {
		// No documents matched means either shard doesn't exist or version mismatch
		return databases.NewDbErrorOnShardConditionFail("shard version mismatch during metadata update", nil)
	}

	return nil
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

// extractShardInfoFromMongoDoc extracts ShardInfo from MongoDB document
func extractShardInfoFromMongoDoc(doc bson.M, shardId int64) *databases.ShardInfo {
	info := &databases.ShardInfo{
		ShardId: shardId,
	}

	info.ShardVersion = getInt64FromBSON(doc, "shard_version")
	info.OwnerAddr = getStringFromBSON(doc, "shard_owner_addr")
	info.ClaimedAt = getTimeFromBSON(doc, "shard_claimed_at")

	// Parse metadata from JSON string to ShardMetadata struct
	metadataStr := getStringFromBSON(doc, "shard_metadata")
	if metadataStr != "" {
		var shardMetadata databases.ShardMetadata
		if err := json.Unmarshal([]byte(metadataStr), &shardMetadata); err == nil {
			info.Metadata = shardMetadata
		}
		// If parsing fails, use default metadata (zero values)
	}

	return info
}

func (m *MongoDBTimerStore) CreateTimer(ctx context.Context, shardId int, shardVersion int64, namespace string, timer *databases.DbTimer) (err *databases.DbError) {
	// Use the provided timer UUID
	timerUuid := timer.TimerUuid

	// Use ZeroUUID for shard records
	zeroUuid := databases.ZeroUUID

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
			"timer_uuid":       zeroUuid,
		}

		var shardDoc bson.M
		shardErr := m.collection.FindOne(sc, shardFilter).Decode(&shardDoc)
		if shardErr != nil {
			if errors.Is(shardErr, mongo.ErrNoDocuments) {
				// Shard doesn't exist
				dbErr = databases.NewDbErrorOnShardConditionFail("shard not found during timer creation", nil)
				return nil // Return from transaction function, will abort transaction
			}
			dbErr = databases.NewGenericDbError("failed to read shard", shardErr)
			return nil
		}

		// Get actual shard version and compare
		actualShardVersion := getInt64FromBSON(shardDoc, "shard_version")
		if actualShardVersion != shardVersion {
			// Version mismatch
			dbErr = databases.NewDbErrorOnShardConditionFail("shard version mismatch during timer creation", nil)
			return nil // Return from transaction function, will abort transaction
		}

		// Shard version matches, proceed to upsert timer
		timerDoc := m.buildTimerDocumentForUpsert(shardId, timer, timerUuid, payloadJSON, retryPolicyJSON)

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
func (m *MongoDBTimerStore) buildTimerDocumentForUpsert(shardId int, timer *databases.DbTimer, timerUuid uuid.UUID, payloadJSON, retryPolicyJSON interface{}) bson.M {
	timerDoc := bson.M{
		"shard_id":                       shardId,
		"row_type":                       databases.RowTypeTimer,
		"timer_execute_at":               timer.ExecuteAt,
		"timer_uuid":                     timerUuid,
		"timer_id":                       timer.Id,
		"timer_namespace":                timer.Namespace,
		"timer_callback_url":             timer.CallbackUrl,
		"timer_callback_timeout_seconds": timer.CallbackTimeoutSeconds,
		"timer_created_at":               timer.CreatedAt,
		"timer_attempts":                 timer.Attempts,
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
	// Use the provided timer UUID
	timerUuid := timer.TimerUuid

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
	timerDoc := m.buildTimerDocumentForUpsert(shardId, timer, timerUuid, payloadJSON, retryPolicyJSON)

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

// buildRangeCondition creates a MongoDB condition for tuple comparison (timestamp, uuid)
// operator should be "$gte" for start boundary or "$lte" for end boundary
func buildRangeCondition(operator string, timestamp time.Time, uuid uuid.UUID) bson.M {
	switch operator {
	case "$gte":
		// For >= comparison: (timestamp, uuid) >= (target_timestamp, target_uuid)
		return bson.M{
			"$or": []bson.M{
				// Case 1: timestamp > target_timestamp (clearly after)
				{"timer_execute_at": bson.M{"$gt": timestamp}},
				// Case 2: timestamp == target_timestamp, check UUID using binary comparison
				{
					"$and": []bson.M{
						{"timer_execute_at": timestamp},
						{"timer_uuid": bson.M{operator: uuid}},
					},
				},
			},
		}
	case "$lte":
		// For <= comparison: (timestamp, uuid) <= (target_timestamp, target_uuid)
		return bson.M{
			"$or": []bson.M{
				// Case 1: timestamp < target_timestamp (clearly before)
				{"timer_execute_at": bson.M{"$lt": timestamp}},
				// Case 2: timestamp == target_timestamp, check UUID using binary comparison
				{
					"$and": []bson.M{
						{"timer_execute_at": timestamp},
						{"timer_uuid": bson.M{operator: uuid}},
					},
				},
			},
		}
	default:
		// should not happen
		panic("unsupported operator")
	}
}

func (c *MongoDBTimerStore) RangeGetTimers(ctx context.Context, shardId int, request *databases.RangeGetTimersRequest) (*databases.RangeGetTimersResponse, *databases.DbError) {
	// Use start and end UUIDs for precise range selection
	startUuid := request.StartTimeUuid
	endUuid := request.EndTimeUuid

	// Query timers in the specified range with precise UUID boundaries
	// Using a cleaner approach than the original complex nested query

	// Build the range filter step by step for clarity
	startCondition := buildRangeCondition("$gte", request.StartTimestamp, startUuid)
	endCondition := buildRangeCondition("$lte", request.EndTimestamp, endUuid)

	filter := bson.M{
		"shard_id": shardId,
		"row_type": databases.RowTypeTimer,
		"$and":     []bson.M{startCondition, endCondition},
	}

	// Create find options with sorting and limit
	findOptions := options.Find().
		SetSort(bson.D{
			{Key: "timer_execute_at", Value: 1},
			{Key: "timer_uuid", Value: 1},
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

		// Get UUID from document
		timerUuid := doc["timer_uuid"].(primitive.Binary).Data
		timerUuidGo, _ := uuid.FromBytes(timerUuid)

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
			TimerUuid:              timerUuidGo,
			Namespace:              doc["timer_namespace"].(string),
			ExecuteAt:              doc["timer_execute_at"].(primitive.DateTime).Time(),
			CallbackUrl:            doc["timer_callback_url"].(string),
			Payload:                payload,
			RetryPolicy:            retryPolicy,
			CallbackTimeoutSeconds: doc["timer_callback_timeout_seconds"].(int32),
			CreatedAt:              doc["timer_created_at"].(primitive.DateTime).Time(),
			Attempts:               doc["timer_attempts"].(int32),
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

func (c *MongoDBTimerStore) RangeDeleteWithBatchInsertTxn(ctx context.Context, shardId int, shardVersion int64, request *databases.RangeDeleteTimersRequest, TimersToInsert []*databases.DbTimer) (*databases.RangeDeleteTimersResponse, *databases.DbError) {
	// Use start and end UUIDs for precise range selection
	startUuid := request.StartTimeUuid
	endUuid := request.EndTimeUuid

	// Use ZeroUUID for shard records
	zeroUuid := databases.ZeroUUID

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
			"timer_uuid":       zeroUuid,
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
			dbErr = databases.NewDbErrorOnShardConditionFail("shard version mismatch during delete and insert operation", nil)
			return nil
		}

		// Use the same range condition logic as in RangeGetTimers
		startCondition := buildRangeCondition("$gte", request.StartTimestamp, startUuid)
		endCondition := buildRangeCondition("$lte", request.EndTimestamp, endUuid)

		// Count timers to be deleted
		deleteFilter := bson.M{
			"shard_id": shardId,
			"row_type": databases.RowTypeTimer,
			"$and":     []bson.M{startCondition, endCondition},
		}

		deleteResult, deleteErr := c.collection.DeleteMany(sc, deleteFilter)
		if deleteErr != nil {
			dbErr = databases.NewGenericDbError("failed to delete timers", deleteErr)
			return nil
		}

		// Insert new timers
		for _, timer := range TimersToInsert {
			timerUuid := timer.TimerUuid

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

			timerDoc := c.buildTimerDocumentForUpsert(shardId, timer, timerUuid, payloadJSON, retryPolicyJSON)

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

func (c *MongoDBTimerStore) RangeDeleteWithLimit(ctx context.Context, shardId int, request *databases.RangeDeleteTimersRequest, limit int) (*databases.RangeDeleteTimersResponse, *databases.DbError) {
	// Note: MongoDB limit parameter is ignored for simplicity, similar to Cassandra
	// Use start and end UUIDs for precise range selection
	startUuid := request.StartTimeUuid
	endUuid := request.EndTimeUuid

	// Use the same range condition logic as in RangeGetTimers
	startCondition := buildRangeCondition("$gte", request.StartTimestamp, startUuid)
	endCondition := buildRangeCondition("$lte", request.EndTimestamp, endUuid)

	// Build delete filter for range selection
	deleteFilter := bson.M{
		"shard_id": shardId,
		"row_type": databases.RowTypeTimer,
		"$and":     []bson.M{startCondition, endCondition},
	}

	// Delete all matching documents (limit ignored)
	deleteResult, deleteErr := c.collection.DeleteMany(ctx, deleteFilter)
	if deleteErr != nil {
		return nil, databases.NewGenericDbError("failed to delete timers", deleteErr)
	}

	return &databases.RangeDeleteTimersResponse{
		DeletedCount: int(deleteResult.DeletedCount),
	}, nil
}

func (c *MongoDBTimerStore) UpdateTimer(ctx context.Context, shardId int, shardVersion int64, namespace string, request *databases.UpdateDbTimerRequest) (err *databases.DbError) {
	// Use ZeroUUID for shard records
	zeroUuid := databases.ZeroUUID

	// Serialize payload and retry policy to JSON
	var payloadJSON, retryPolicyJSON interface{}

	if request.Payload != nil {
		payloadBytes, marshalErr := json.Marshal(request.Payload)
		if marshalErr != nil {
			return databases.NewGenericDbError("failed to marshal timer payload", marshalErr)
		}
		payloadJSON = string(payloadBytes)
	}

	if request.RetryPolicy != nil {
		retryPolicyBytes, marshalErr := json.Marshal(request.RetryPolicy)
		if marshalErr != nil {
			return databases.NewGenericDbError("failed to marshal timer retry policy", marshalErr)
		}
		retryPolicyJSON = string(retryPolicyBytes)
	}

	// Use MongoDB transaction for atomic update with shard version check
	session, sessionErr := c.client.StartSession()
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

		// Check shard version first
		shardFilter := bson.M{
			"shard_id":         shardId,
			"row_type":         databases.RowTypeShard,
			"timer_execute_at": databases.ZeroTimestamp,
			"timer_uuid":       zeroUuid,
		}

		var shardDoc bson.M
		shardErr := c.collection.FindOne(sc, shardFilter).Decode(&shardDoc)
		if shardErr != nil {
			if errors.Is(shardErr, mongo.ErrNoDocuments) {
				dbErr = databases.NewDbErrorOnShardConditionFail("shard not found during timer update", nil)
				return nil
			}
			dbErr = databases.NewGenericDbError("failed to read shard", shardErr)
			return nil
		}

		// Get actual shard version and compare
		actualShardVersion := getInt64FromBSON(shardDoc, "shard_version")
		if actualShardVersion != shardVersion {
			dbErr = databases.NewDbErrorOnShardConditionFail("shard version mismatch during timer update", nil)
			return nil
		}

		// Update timer atomically (without pre-reading to optimize performance)
		timerFilter := bson.M{
			"shard_id":        shardId,
			"row_type":        databases.RowTypeTimer,
			"timer_namespace": namespace,
			"timer_id":        request.TimerId,
		}

		// Build update document
		updateDoc := bson.M{
			"timer_execute_at":               request.ExecuteAt,
			"timer_callback_url":             request.CallbackUrl,
			"timer_callback_timeout_seconds": request.CallbackTimeoutSeconds,
		}

		// Update UUID fields only if ExecuteAt changed (we need to regenerate the UUID)
		timerUuid := databases.GenerateTimerUUID(namespace, request.TimerId)
		updateDoc["timer_uuid"] = timerUuid

		if payloadJSON != nil {
			updateDoc["timer_payload"] = payloadJSON
		} else {
			updateDoc["timer_payload"] = nil
		}

		if retryPolicyJSON != nil {
			updateDoc["timer_retry_policy"] = retryPolicyJSON
		} else {
			updateDoc["timer_retry_policy"] = nil
		}

		update := bson.M{"$set": updateDoc}
		result, updateErr := c.collection.UpdateOne(sc, timerFilter, update)
		if updateErr != nil {
			dbErr = databases.NewGenericDbError("failed to update timer", updateErr)
			return nil
		}

		if result.MatchedCount == 0 {
			dbErr = databases.NewDbErrorNotExists("timer not found during update", nil)
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

func (c *MongoDBTimerStore) GetTimer(ctx context.Context, shardId int, namespace string, timerId string) (timer *databases.DbTimer, err *databases.DbError) {
	// Query timer by shard_id, namespace, and timer_id
	filter := bson.M{
		"shard_id":        shardId,
		"row_type":        databases.RowTypeTimer,
		"timer_namespace": namespace,
		"timer_id":        timerId,
	}

	var doc bson.M
	findErr := c.collection.FindOne(ctx, filter).Decode(&doc)
	if findErr != nil {
		if errors.Is(findErr, mongo.ErrNoDocuments) {
			return nil, databases.NewDbErrorNotExists("timer not found", nil)
		}
		return nil, databases.NewGenericDbError("failed to get timer", findErr)
	}

	// Get UUID from document
	timerUuid := doc["timer_uuid"].(primitive.Binary).Data
	timerUuidGo, _ := uuid.FromBytes(timerUuid)

	// Parse JSON payload and retry policy
	var payload interface{}
	var retryPolicy interface{}

	if payloadStr, ok := doc["timer_payload"].(string); ok && payloadStr != "" {
		if unmarshalErr := json.Unmarshal([]byte(payloadStr), &payload); unmarshalErr != nil {
			return nil, databases.NewGenericDbError("failed to unmarshal timer payload", unmarshalErr)
		}
	}

	if retryPolicyStr, ok := doc["timer_retry_policy"].(string); ok && retryPolicyStr != "" {
		if unmarshalErr := json.Unmarshal([]byte(retryPolicyStr), &retryPolicy); unmarshalErr != nil {
			return nil, databases.NewGenericDbError("failed to unmarshal timer retry policy", unmarshalErr)
		}
	}

	timer = &databases.DbTimer{
		Id:                     timerId,
		TimerUuid:              timerUuidGo,
		Namespace:              namespace,
		ExecuteAt:              doc["timer_execute_at"].(primitive.DateTime).Time(),
		CallbackUrl:            doc["timer_callback_url"].(string),
		Payload:                payload,
		RetryPolicy:            retryPolicy,
		CallbackTimeoutSeconds: doc["timer_callback_timeout_seconds"].(int32),
		CreatedAt:              doc["timer_created_at"].(primitive.DateTime).Time(),
		Attempts:               doc["timer_attempts"].(int32),
	}

	return timer, nil
}

func (c *MongoDBTimerStore) DeleteTimer(ctx context.Context, shardId int, shardVersion int64, namespace string, timerId string) *databases.DbError {
	// Use ZeroUUID for shard records
	zeroUuid := databases.ZeroUUID

	// Use MongoDB transaction for atomic delete with shard version check
	session, sessionErr := c.client.StartSession()
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

		// Check shard version first
		shardFilter := bson.M{
			"shard_id":         shardId,
			"row_type":         databases.RowTypeShard,
			"timer_execute_at": databases.ZeroTimestamp,
			"timer_uuid":       zeroUuid,
		}

		var shardDoc bson.M
		shardErr := c.collection.FindOne(sc, shardFilter).Decode(&shardDoc)
		if shardErr != nil {
			if errors.Is(shardErr, mongo.ErrNoDocuments) {
				dbErr = databases.NewDbErrorOnShardConditionFail("shard not found during timer delete", nil)
				return nil
			}
			dbErr = databases.NewGenericDbError("failed to read shard", shardErr)
			return nil
		}

		// Get actual shard version and compare
		actualShardVersion := getInt64FromBSON(shardDoc, "shard_version")
		if actualShardVersion != shardVersion {
			dbErr = databases.NewDbErrorOnShardConditionFail("shard version mismatch during timer delete", nil)
			return nil
		}

		// Delete timer atomically (without pre-reading to optimize performance)
		timerFilter := bson.M{
			"shard_id":        shardId,
			"row_type":        databases.RowTypeTimer,
			"timer_namespace": namespace,
			"timer_id":        timerId,
		}

		_, deleteErr := c.collection.DeleteOne(sc, timerFilter)
		if deleteErr != nil {
			dbErr = databases.NewGenericDbError("failed to delete timer", deleteErr)
			return nil
		}

		// DeleteTimer should be idempotent - if timer doesn't exist, that's OK
		// We don't need to check DeletedCount for idempotent behavior

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
