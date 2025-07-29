package dynamodb

import (
	"context"
	"fmt"
	"log"
	"os"

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


// setupTestStore creates a test store with a clean test table
func setupTestStore(t *testing.T) (*DynamoDBTimerStore, func()) {
	// Create test table
	err := createTestTableDirect()
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
	_, _ = client.DeleteTable(context.Background(), &dynamodb.DeleteTableInput{
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
				AttributeName: aws.String("timer_execute_at_with_uuid"),
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
				IndexName: aws.String("ExecuteAtWithUuidIndex"),
				KeySchema: []types.KeySchemaElement{
					{
						AttributeName: aws.String("shard_id"),
						KeyType:       types.KeyTypeHash,
					},
					{
						AttributeName: aws.String("timer_execute_at_with_uuid"),
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
