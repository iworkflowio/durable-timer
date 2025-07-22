package mongodb

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/iworkflowio/durable-timer/config"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	testHost         = "localhost"
	testPort         = 27017
	testDatabase     = "timer_service_test"
	testUsername     = "root"
	testPassword     = "mongodb_root_password"
	testAuthDatabase = "admin"
)

func getTestHost() string {
	if host := os.Getenv("MONGODB_TEST_HOST"); host != "" {
		return host
	}
	return testHost
}

func getSchemaFilePath() string {
	_, filename, _, _ := runtime.Caller(0)
	dir := filepath.Dir(filename)
	return filepath.Join(dir, "schema", "v1.js")
}

// executeSchemaFile reads and executes MongoDB commands from the schema file
func executeSchemaFile(database *mongo.Database) error {
	contentBytes, err := os.ReadFile(getSchemaFilePath())
	if err != nil {
		log.Fatalf("Error reading file: %v at %v", err, getSchemaFilePath())
	}

	content := string(contentBytes)

	// Split by semicolon to get individual statements
	statements := strings.Split(content, ";")

	ctx := context.Background()
	for _, stmt := range statements {
		// Trim whitespace and skip empty statements
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}

		// Skip comment-only lines
		if strings.HasPrefix(stmt, "//") {
			continue
		}

		// Execute MongoDB JavaScript command
		// Note: This is a simplified approach - in reality, you'd parse the JS and convert to Go MongoDB operations
		// For now, we'll create the collection and indexes manually
		if strings.Contains(stmt, "createCollection") {
			err = database.CreateCollection(ctx, "timers")
			if err != nil {
				// Collection might already exist, ignore error
				continue
			}
		}

		// Handle index creation - create the two indexes from the design doc
		if strings.Contains(stmt, "createIndex") {
			collection := database.Collection("timers")

			// Create compound index for timer execution queries
			if strings.Contains(stmt, "timer_execute_at") {
				indexModel := mongo.IndexModel{
					Keys: map[string]interface{}{
						"shard_id":         1,
						"row_type":         1,
						"timer_execute_at": 1,
						"timer_uuid":       1,
					},
					Options: options.Index().SetUnique(true),
				}
				_, err = collection.Indexes().CreateOne(ctx, indexModel)
				if err != nil {
					return fmt.Errorf("failed to create execution index: %w", err)
				}
			}

			// Create unique index for timer CRUD operations
			if strings.Contains(stmt, "timer_id") {
				indexModel := mongo.IndexModel{
					Keys: map[string]interface{}{
						"shard_id": 1,
						"row_type": 1,
						"timer_id": 1,
					},
					Options: options.Index().SetUnique(true),
				}
				_, err = collection.Indexes().CreateOne(ctx, indexModel)
				if err != nil {
					return fmt.Errorf("failed to create timer lookup index: %w", err)
				}
			}
		}
	}

	return nil
}

// setupTestStore creates a test store with a clean test database
func setupTestStore(t *testing.T) (*MongoDBTimerStore, func()) {
	// Create test database and collections
	err := createTestDatabase()
	if err != nil {
		t.Fatal("Failed to create test database:", err)
	}

	// Create store with test configuration
	config := &config.MongoDBConnectConfig{
		Host:            getTestHost(),
		Port:            testPort,
		Database:        testDatabase,
		Username:        testUsername,
		Password:        testPassword,
		AuthDatabase:    testAuthDatabase,
		MaxPoolSize:     10,
		MinPoolSize:     1,
		ConnMaxLifetime: 5 * time.Minute,
		ConnMaxIdleTime: 2 * time.Minute,
	}

	store, err := NewMongoDBTimerStore(config)
	require.NoError(t, err)
	mongoDBStore := store.(*MongoDBTimerStore)

	// Cleanup function
	cleanup := func() {
		mongoDBStore.Close()
		dropTestDatabase()
	}

	return mongoDBStore, cleanup
}

func createTestDatabase() error {
	// Connect as root user
	uri := fmt.Sprintf("mongodb://%s:%s@%s:%d/?authSource=%s",
		testUsername, testPassword, getTestHost(), testPort, testAuthDatabase)

	client, err := mongo.Connect(context.Background(), options.Client().ApplyURI(uri))
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())

	// Drop database if exists
	err = client.Database(testDatabase).Drop(context.Background())
	if err != nil {
		// Database might not exist, ignore error
	}

	// Get database reference (MongoDB creates it lazily)
	database := client.Database(testDatabase)

	// Create the timers collection explicitly
	ctx := context.Background()
	err = database.CreateCollection(ctx, "timers")
	if err != nil {
		return fmt.Errorf("failed to create timers collection: %w", err)
	}

	// Create the required indexes directly
	collection := database.Collection("timers")

	// Create compound index for timer execution queries (unique)
	executionIndexModel := mongo.IndexModel{
		Keys: bson.D{
			{"shard_id", 1},
			{"row_type", 1},
			{"timer_execute_at", 1},
			{"timer_uuid", 1},
		},
		Options: options.Index().SetUnique(true),
	}
	_, err = collection.Indexes().CreateOne(ctx, executionIndexModel)
	if err != nil {
		return fmt.Errorf("failed to create execution index: %w", err)
	}

	// Create unique index for timer CRUD operations
	timerIndexModel := mongo.IndexModel{
		Keys: bson.D{
			{"shard_id", 1},
			{"row_type", 1},
			{"timer_id", 1},
		},
		Options: options.Index().SetUnique(true),
	}
	_, err = collection.Indexes().CreateOne(ctx, timerIndexModel)
	if err != nil {
		return fmt.Errorf("failed to create timer lookup index: %w", err)
	}

	return nil
}

func dropTestDatabase() error {
	// Connect as root user
	uri := fmt.Sprintf("mongodb://%s:%s@%s:%d/?authSource=%s",
		testUsername, testPassword, getTestHost(), testPort, testAuthDatabase)

	client, err := mongo.Connect(context.Background(), options.Client().ApplyURI(uri))
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())

	// Drop test database
	err = client.Database(testDatabase).Drop(context.Background())
	return err
}
