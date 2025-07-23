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
	// Build connection URI
	uri := fmt.Sprintf("mongodb://%s:%s@%s:%d/%s?authSource=%s",
		config.Username, config.Password, config.Host, config.Port, config.Database, config.AuthDatabase)

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
	ctx context.Context, shardId int, ownerId string, metadata interface{},
) (shardVersion int64, retErr *databases.DbError) {
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
		"timer_uuid":       databases.ZeroUUIDString,
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
			"timer_uuid":       databases.ZeroUUIDString,
			"shard_version":    newVersion,
			"shard_owner_id":   ownerId,
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
						OwnerId:      getStringFromBSON(conflictDoc, "shard_owner_id"),
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
		"timer_uuid":       databases.ZeroUUIDString,
		"shard_version":    currentVersion, // Only update if version matches
	}

	update := bson.M{
		"$set": bson.M{
			"shard_version":    newVersion,
			"shard_owner_id":   ownerId,
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

func (c *MongoDBTimerStore) CreateTimer(ctx context.Context, shardId int, shardVersion int64, namespace string, timer *databases.DbTimer) (err *databases.DbError) {
	//TODO implement me
	panic("implement me")
}

func (c *MongoDBTimerStore) CreateTimerNoLock(ctx context.Context, shardId int, namespace string, timer *databases.DbTimer) (err *databases.DbError) {
	//TODO implement me
	panic("implement me")
}

func (c *MongoDBTimerStore) GetTimersUpToTimestamp(ctx context.Context, shardId int, namespace string, request *databases.RangeGetTimersRequest) (*databases.RangeGetTimersResponse, *databases.DbError) {
	//TODO implement me
	panic("implement me")
}

func (c *MongoDBTimerStore) DeleteTimersUpToTimestampWithBatchInsert(ctx context.Context, shardId int, shardVersion int64, namespace string, request *databases.RangeDeleteTimersRequest, TimersToInsert []*databases.DbTimer) (*databases.RangeDeleteTimersResponse, *databases.DbError) {
	//TODO implement me
	panic("implement me")
}

func (c *MongoDBTimerStore) BatchInsertTimers(ctx context.Context, shardId int, shardVersion int64, namespace string, TimersToInsert []*databases.DbTimer) *databases.DbError {
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
