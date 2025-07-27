package mongodb

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/iworkflowio/durable-timer/config"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	testHost         = "localhost"
	testPort         = 27017
	testDatabase     = "timer_service_test"
	testUsername     = "" // No authentication in test setup
	testPassword     = "" // No authentication in test setup
	testAuthDatabase = "" // No authentication in test setup
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

// executeSchemaFileWithMongosh reads and executes MongoDB commands from the schema file using mongosh
func executeSchemaFileWithMongosh() error {
	schemaFilePath := getSchemaFilePath()
	if _, err := os.Stat(schemaFilePath); os.IsNotExist(err) {
		log.Fatalf("Schema file not found at %s", schemaFilePath)
	}

	// Read the schema file content
	contentBytes, err := os.ReadFile(schemaFilePath)
	if err != nil {
		return fmt.Errorf("failed to read schema file: %w", err)
	}

	// Use docker exec to run mongosh inside the MongoDB container
	cmd := exec.Command("docker", "exec", "timer-service-mongodb-dev", "mongosh",
		testDatabase,
		"--eval", string(contentBytes))

	// Run the command
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker mongosh command failed: %w\nOutput: %s", err, string(output))
	}

	log.Printf("Schema file %s executed successfully with docker mongosh", schemaFilePath)
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
	// Connect without authentication for development/CI setup
	uri := fmt.Sprintf("mongodb://%s:%d/?directConnection=true",
		getTestHost(), testPort)

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

	// Execute schema from v1.js file using mongosh command
	err = executeSchemaFileWithMongosh()
	if err != nil {
		return fmt.Errorf("failed to execute schema file: %w", err)
	}

	return nil
}

func dropTestDatabase() error {
	// Connect as root user with direct connection for localhost testing
	uri := fmt.Sprintf("mongodb://%s:%s@%s:%d/?authSource=%s&directConnection=true",
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
