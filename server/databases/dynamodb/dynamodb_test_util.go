package dynamodb

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

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	appconfig "github.com/iworkflowio/durable-timer/config"
	"github.com/stretchr/testify/require"
)

const (
	testEndpointURL = "http://localhost:8000"
	testRegion      = "us-east-1"
	testTableName   = "timers_test"
	testAccessKeyID = "dummy"
	testSecretKey   = "dummy"
)

func getTestEndpointURL() string {
	if endpoint := os.Getenv("DYNAMODB_TEST_ENDPOINT"); endpoint != "" {
		return endpoint
	}
	return testEndpointURL
}

func getSchemaFilePath() string {
	_, filename, _, _ := runtime.Caller(0)
	dir := filepath.Dir(filename)
	return filepath.Join(dir, "schema", "v1.json")
}

// executeSchemaFileWithAWSCLI creates DynamoDB table using AWS CLI and the schema file
func executeSchemaFileWithAWSCLI() error {
	schemaFilePath := getSchemaFilePath()
	if _, err := os.Stat(schemaFilePath); os.IsNotExist(err) {
		log.Fatalf("Schema file not found at %s", schemaFilePath)
	}

	// Use docker exec to run aws cli inside the dynamodb container network
	cmd := exec.Command("docker", "run", "--rm",
		"--network", "docker_default", // Assumes docker-compose network
		"-v", fmt.Sprintf("%s:/schema", filepath.Dir(schemaFilePath)),
		"-e", "AWS_ACCESS_KEY_ID=dummy",
		"-e", "AWS_SECRET_ACCESS_KEY=dummy",
		"-e", "AWS_DEFAULT_REGION=us-east-1",
		"amazon/aws-cli:latest",
		"dynamodb", "create-table",
		"--endpoint-url", "http://timer-service-dynamodb-dev:8000",
		"--table-name", testTableName,
		"--cli-input-json", "file:///schema/v1.json")

	// Modify the command to use the test table name
	cmdStr := fmt.Sprintf(`sed 's/"TableName": "timers"/"TableName": "%s"/' %s | docker run --rm -i --network docker_default -e AWS_ACCESS_KEY_ID=dummy -e AWS_SECRET_ACCESS_KEY=dummy -e AWS_DEFAULT_REGION=us-east-1 amazon/aws-cli:latest dynamodb create-table --endpoint-url http://timer-service-dynamodb-dev:8000 --cli-input-json file:///dev/stdin`, testTableName, schemaFilePath)

	cmd = exec.Command("sh", "-c", cmdStr)

	// Run the command
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("aws cli command failed: %w\nOutput: %s", err, string(output))
	}

	log.Printf("DynamoDB table %s created successfully with AWS CLI", testTableName)
	return nil
}

// setupTestStore creates a test store with a clean test table
func setupTestStore(t *testing.T) (*DynamoDBTimerStore, func()) {
	// Create test table
	err := createTestTable()
	if err != nil {
		t.Fatal("Failed to create test table:", err)
	}

	// Create store with test configuration
	config := &appconfig.DynamoDBConnectConfig{
		Region:          testRegion,
		EndpointURL:     getTestEndpointURL(),
		AccessKeyID:     testAccessKeyID,
		SecretAccessKey: testSecretKey,
		TableName:       testTableName,
		MaxRetries:      3,
		Timeout:         10 * time.Second,
	}

	store, err := NewDynamoDBTimerStore(config)
	require.NoError(t, err)
	dynamodbStore := store.(*DynamoDBTimerStore)

	// Cleanup function
	cleanup := func() {
		dynamodbStore.Close()
		dropTestTable()
	}

	return dynamodbStore, cleanup
}

func createTestTable() error {
	// Try to create table using AWS CLI via Docker
	err := executeSchemaFileWithAWSCLI()
	if err != nil {
		// If AWS CLI approach fails, try direct approach
		return createTestTableDirect()
	}
	return nil
}

func createTestTableDirect() error {
	// Create AWS config for DynamoDB Local
	awsCfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(testRegion),
		config.WithEndpointResolverWithOptions(aws.EndpointResolverWithOptionsFunc(
			func(service, region string, options ...interface{}) (aws.Endpoint, error) {
				return aws.Endpoint{URL: getTestEndpointURL()}, nil
			})),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			testAccessKeyID, testSecretKey, "")),
	)
	if err != nil {
		return err
	}

	client := dynamodb.NewFromConfig(awsCfg)

	// Drop table if exists
	_, err = client.DeleteTable(context.Background(), &dynamodb.DeleteTableInput{
		TableName: aws.String(testTableName),
	})
	// Ignore error if table doesn't exist

	// Wait a bit for table deletion
	time.Sleep(1 * time.Second)

	// Create table with the same schema as v1.json but with test table name
	createTableInput := &dynamodb.CreateTableInput{
		TableName: aws.String(testTableName),
		AttributeDefinitions: []types.AttributeDefinition{
			{
				AttributeName: aws.String("shard_id"),
				AttributeType: types.ScalarAttributeTypeN,
			},
			{
				AttributeName: aws.String("sort_key"),
				AttributeType: types.ScalarAttributeTypeS,
			},
			{
				AttributeName: aws.String("timer_execute_at"),
				AttributeType: types.ScalarAttributeTypeS,
			},
		},
		KeySchema: []types.KeySchemaElement{
			{
				AttributeName: aws.String("shard_id"),
				KeyType:       types.KeyTypeHash,
			},
			{
				AttributeName: aws.String("sort_key"),
				KeyType:       types.KeyTypeRange,
			},
		},
		LocalSecondaryIndexes: []types.LocalSecondaryIndex{
			{
				IndexName: aws.String("ExecuteAtIndex"),
				KeySchema: []types.KeySchemaElement{
					{
						AttributeName: aws.String("shard_id"),
						KeyType:       types.KeyTypeHash,
					},
					{
						AttributeName: aws.String("timer_execute_at"),
						KeyType:       types.KeyTypeRange,
					},
				},
				Projection: &types.Projection{
					ProjectionType: types.ProjectionTypeAll,
				},
			},
		},
		BillingMode: types.BillingModePayPerRequest,
	}

	_, err = client.CreateTable(context.Background(), createTableInput)
	if err != nil {
		return fmt.Errorf("failed to create table: %w", err)
	}

	// Wait for table to be active
	waiter := dynamodb.NewTableExistsWaiter(client)
	err = waiter.Wait(context.Background(), &dynamodb.DescribeTableInput{
		TableName: aws.String(testTableName),
	}, 30*time.Second)
	if err != nil {
		return fmt.Errorf("failed to wait for table to be active: %w", err)
	}

	log.Printf("DynamoDB table %s created successfully", testTableName)
	return nil
}

func dropTestTable() error {
	// Create AWS config for DynamoDB Local
	awsCfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(testRegion),
		config.WithEndpointResolverWithOptions(aws.EndpointResolverWithOptionsFunc(
			func(service, region string, options ...interface{}) (aws.Endpoint, error) {
				return aws.Endpoint{URL: getTestEndpointURL()}, nil
			})),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			testAccessKeyID, testSecretKey, "")),
	)
	if err != nil {
		return err
	}

	client := dynamodb.NewFromConfig(awsCfg)

	// Drop test table
	_, err = client.DeleteTable(context.Background(), &dynamodb.DeleteTableInput{
		TableName: aws.String(testTableName),
	})
	return err
}
