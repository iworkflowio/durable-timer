package config

import "time"

// DynamoDBConnectConfig holds configuration for DynamoDB connection
type DynamoDBConnectConfig struct {
	Region          string
	EndpointURL     string // For DynamoDB Local, e.g., "http://localhost:8000"
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string // Optional for temporary credentials
	TableName       string
	MaxRetries      int
	Timeout         time.Duration
}
