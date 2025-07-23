package dynamodb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"
	appconfig "github.com/iworkflowio/durable-timer/config"
	"github.com/iworkflowio/durable-timer/databases"
)

const (
	// Sort key prefixes for unified table design
	shardSortKey       = "SHARD"
	timerSortKeyPrefix = "TIMER#"
)

// DynamoDBTimerStore implements TimerStore interface for DynamoDB
type DynamoDBTimerStore struct {
	client    *dynamodb.Client
	tableName string
}

// NewDynamoDBTimerStore creates a new DynamoDB timer store
func NewDynamoDBTimerStore(cfg *appconfig.DynamoDBConnectConfig) (databases.TimerStore, error) {
	// Create AWS config
	var awsCfg aws.Config
	var err error

	if cfg.EndpointURL != "" {
		// For DynamoDB Local
		awsCfg, err = config.LoadDefaultConfig(context.Background(),
			config.WithRegion(cfg.Region),
			config.WithEndpointResolverWithOptions(aws.EndpointResolverWithOptionsFunc(
				func(service, region string, options ...interface{}) (aws.Endpoint, error) {
					return aws.Endpoint{URL: cfg.EndpointURL}, nil
				})),
			config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
				cfg.AccessKeyID, cfg.SecretAccessKey, cfg.SessionToken)),
		)
	} else {
		// For AWS DynamoDB
		awsCfg, err = config.LoadDefaultConfig(context.Background(),
			config.WithRegion(cfg.Region))
	}

	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := dynamodb.NewFromConfig(awsCfg)

	store := &DynamoDBTimerStore{
		client:    client,
		tableName: cfg.TableName,
	}

	return store, nil
}

// Close closes the DynamoDB connection (no-op for DynamoDB)
func (d *DynamoDBTimerStore) Close() error {
	return nil
}

func (d *DynamoDBTimerStore) ClaimShardOwnership(
	ctx context.Context, shardId int, ownerId string, metadata interface{},
) (shardVersion int64, retErr *databases.DbError) {
	// Serialize metadata to JSON
	var metadataJSON *string
	if metadata != nil {
		metadataBytes, err := json.Marshal(metadata)
		if err != nil {
			return 0, databases.NewGenericDbError("failed to marshal metadata", err)
		}
		metadataStr := string(metadataBytes)
		metadataJSON = &metadataStr
	}

	now := time.Now().UTC()

	// DynamoDB item key for shard record
	key := map[string]types.AttributeValue{
		"shard_id": &types.AttributeValueMemberN{Value: strconv.Itoa(shardId)},
		"sort_key": &types.AttributeValueMemberS{Value: shardSortKey},
	}

	// First, try to read the current shard record
	getItemInput := &dynamodb.GetItemInput{
		TableName: aws.String(d.tableName),
		Key:       key,
	}

	result, err := d.client.GetItem(ctx, getItemInput)
	if err != nil {
		return 0, databases.NewGenericDbError("failed to read shard record", err)
	}

	if result.Item == nil {
		// Shard doesn't exist, create it with version 1
		newVersion := int64(1)

		item := map[string]types.AttributeValue{
			"shard_id":         &types.AttributeValueMemberN{Value: strconv.Itoa(shardId)},
			"sort_key":         &types.AttributeValueMemberS{Value: shardSortKey},
			"row_type":         &types.AttributeValueMemberN{Value: strconv.Itoa(int(databases.RowTypeShard))},
			"timer_execute_at": &types.AttributeValueMemberS{Value: databases.ZeroTimestamp.Format(time.RFC3339Nano)},
			"timer_uuid":       &types.AttributeValueMemberS{Value: databases.ZeroUUIDString},
			"shard_version":    &types.AttributeValueMemberN{Value: strconv.FormatInt(newVersion, 10)},
			"shard_owner_id":   &types.AttributeValueMemberS{Value: ownerId},
			"shard_claimed_at": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)},
		}

		if metadataJSON != nil {
			item["shard_metadata"] = &types.AttributeValueMemberS{Value: *metadataJSON}
		}

		putItemInput := &dynamodb.PutItemInput{
			TableName:           aws.String(d.tableName),
			Item:                item,
			ConditionExpression: aws.String("attribute_not_exists(shard_id)"),
		}

		_, err = d.client.PutItem(ctx, putItemInput)
		if err != nil {
			// Check if it's a conditional check failed error (another instance created it concurrently)
			if isConditionalCheckFailedException(err) {
				// Try to read the existing record to return conflict info
				conflictResult, conflictErr := d.client.GetItem(ctx, getItemInput)
				if conflictErr == nil && conflictResult.Item != nil {
					conflictInfo := extractShardInfoFromItem(conflictResult.Item, int64(shardId))
					return 0, databases.NewDbErrorOnShardConditionFail("failed to insert shard record due to concurrent insert", nil, conflictInfo)
				}
			}
			return 0, databases.NewGenericDbError("failed to insert new shard record", err)
		}

		// Successfully created new record
		return newVersion, nil
	}

	// Extract current version and update with optimistic concurrency control
	currentVersion, err := extractShardVersionFromItem(result.Item)
	if err != nil {
		return 0, databases.NewGenericDbError("failed to parse current shard version", err)
	}

	newVersion := currentVersion + 1

	// Update with version check for optimistic concurrency
	updateExpr := "SET shard_version = :new_version, shard_owner_id = :owner_id, shard_claimed_at = :claimed_at"
	exprAttrValues := map[string]types.AttributeValue{
		":new_version":     &types.AttributeValueMemberN{Value: strconv.FormatInt(newVersion, 10)},
		":owner_id":        &types.AttributeValueMemberS{Value: ownerId},
		":claimed_at":      &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)},
		":current_version": &types.AttributeValueMemberN{Value: strconv.FormatInt(currentVersion, 10)},
	}

	if metadataJSON != nil {
		updateExpr += ", shard_metadata = :metadata"
		exprAttrValues[":metadata"] = &types.AttributeValueMemberS{Value: *metadataJSON}
	}

	updateItemInput := &dynamodb.UpdateItemInput{
		TableName:                 aws.String(d.tableName),
		Key:                       key,
		UpdateExpression:          aws.String(updateExpr),
		ConditionExpression:       aws.String("shard_version = :current_version"),
		ExpressionAttributeValues: exprAttrValues,
	}

	_, err = d.client.UpdateItem(ctx, updateItemInput)
	if err != nil {
		if isConditionalCheckFailedException(err) {
			// Version changed concurrently, return conflict info
			conflictInfo := &databases.ShardInfo{
				ShardVersion: currentVersion, // We know it was at least this version
			}
			return 0, databases.NewDbErrorOnShardConditionFail("shard ownership claim failed due to concurrent modification", nil, conflictInfo)
		}
		return 0, databases.NewGenericDbError("failed to update shard record", err)
	}

	return newVersion, nil
}

// Helper functions for DynamoDB operations
func extractShardVersionFromItem(item map[string]types.AttributeValue) (int64, error) {
	versionAttr, exists := item["shard_version"]
	if !exists {
		return 0, fmt.Errorf("shard_version attribute not found")
	}

	versionNum, ok := versionAttr.(*types.AttributeValueMemberN)
	if !ok {
		return 0, fmt.Errorf("shard_version is not a number")
	}

	version, err := strconv.ParseInt(versionNum.Value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse shard_version: %w", err)
	}

	return version, nil
}

func extractShardInfoFromItem(item map[string]types.AttributeValue, shardId int64) *databases.ShardInfo {
	info := &databases.ShardInfo{
		ShardId: shardId,
	}

	if versionAttr, exists := item["shard_version"]; exists {
		if versionNum, ok := versionAttr.(*types.AttributeValueMemberN); ok {
			if version, err := strconv.ParseInt(versionNum.Value, 10, 64); err == nil {
				info.ShardVersion = version
			}
		}
	}

	if ownerAttr, exists := item["shard_owner_id"]; exists {
		if ownerStr, ok := ownerAttr.(*types.AttributeValueMemberS); ok {
			info.OwnerId = ownerStr.Value
		}
	}

	if claimedAtAttr, exists := item["shard_claimed_at"]; exists {
		if claimedAtStr, ok := claimedAtAttr.(*types.AttributeValueMemberS); ok {
			if claimedAt, err := time.Parse(time.RFC3339Nano, claimedAtStr.Value); err == nil {
				info.ClaimedAt = claimedAt
			}
		}
	}

	if metadataAttr, exists := item["shard_metadata"]; exists {
		if metadataStr, ok := metadataAttr.(*types.AttributeValueMemberS); ok {
			info.Metadata = metadataStr.Value
		}
	}

	return info
}

// isConditionalCheckFailedException checks if the error is a DynamoDB conditional check failed error
func isConditionalCheckFailedException(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode() == "ConditionalCheckFailedException" ||
			apiErr.ErrorCode() == "TransactionCanceledException"
	}
	return false
}

func (d *DynamoDBTimerStore) CreateTimer(ctx context.Context, shardId int, shardVersion int64, namespace string, timer *databases.DbTimer) (err *databases.DbError) {
	// Serialize payload and retry policy to JSON
	var payloadJSON, retryPolicyJSON *string

	if timer.Payload != nil {
		payloadBytes, marshalErr := json.Marshal(timer.Payload)
		if marshalErr != nil {
			return databases.NewGenericDbError("failed to marshal timer payload", marshalErr)
		}
		payloadStr := string(payloadBytes)
		payloadJSON = &payloadStr
	}

	if timer.RetryPolicy != nil {
		retryPolicyBytes, marshalErr := json.Marshal(timer.RetryPolicy)
		if marshalErr != nil {
			return databases.NewGenericDbError("failed to marshal timer retry policy", marshalErr)
		}
		retryPolicyStr := string(retryPolicyBytes)
		retryPolicyJSON = &retryPolicyStr
	}

	// Create timer sort key: TIMER#<namespace>#<timer_id>
	timerSortKey := fmt.Sprintf("%s%s#%s", timerSortKeyPrefix, namespace, timer.Id)

	// Create timer item
	timerItem := map[string]types.AttributeValue{
		"shard_id":                       &types.AttributeValueMemberN{Value: strconv.Itoa(shardId)},
		"sort_key":                       &types.AttributeValueMemberS{Value: timerSortKey},
		"row_type":                       &types.AttributeValueMemberN{Value: strconv.Itoa(int(databases.RowTypeTimer))},
		"timer_execute_at":               &types.AttributeValueMemberS{Value: timer.ExecuteAt.Format(time.RFC3339Nano)},
		"timer_uuid":                     &types.AttributeValueMemberS{Value: timer.TimerUuid},
		"timer_id":                       &types.AttributeValueMemberS{Value: timer.Id},
		"timer_namespace":                &types.AttributeValueMemberS{Value: timer.Namespace},
		"timer_callback_url":             &types.AttributeValueMemberS{Value: timer.CallbackUrl},
		"timer_callback_timeout_seconds": &types.AttributeValueMemberN{Value: strconv.Itoa(int(timer.CallbackTimeoutSeconds))},
		"timer_created_at":               &types.AttributeValueMemberS{Value: timer.CreatedAt.Format(time.RFC3339Nano)},
		"timer_attempts":                 &types.AttributeValueMemberN{Value: "0"},
	}

	if payloadJSON != nil {
		timerItem["timer_payload"] = &types.AttributeValueMemberS{Value: *payloadJSON}
	}

	if retryPolicyJSON != nil {
		timerItem["timer_retry_policy"] = &types.AttributeValueMemberS{Value: *retryPolicyJSON}
	}

	// Prepare shard key for condition check
	shardKey := map[string]types.AttributeValue{
		"shard_id": &types.AttributeValueMemberN{Value: strconv.Itoa(shardId)},
		"sort_key": &types.AttributeValueMemberS{Value: shardSortKey},
	}

	// Create transaction with shard version check and timer insertion
	transactItems := []types.TransactWriteItem{
		{
			// Condition check: verify shard version matches
			ConditionCheck: &types.ConditionCheck{
				TableName:           aws.String(d.tableName),
				Key:                 shardKey,
				ConditionExpression: aws.String("shard_version = :expected_version"),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":expected_version": &types.AttributeValueMemberN{Value: strconv.FormatInt(shardVersion, 10)},
				},
			},
		},
		{
			// Put item: insert the timer
			Put: &types.Put{
				TableName: aws.String(d.tableName),
				Item:      timerItem,
			},
		},
	}

	transactInput := &dynamodb.TransactWriteItemsInput{
		TransactItems: transactItems,
	}

	_, transactErr := d.client.TransactWriteItems(ctx, transactInput)
	if transactErr != nil {
		if isConditionalCheckFailedException(transactErr) {
			// Shard version mismatch - don't perform expensive read query as requested
			// Just return a generic conflict error without specific version info
			conflictInfo := &databases.ShardInfo{
				ShardVersion: 0, // Unknown version to avoid expensive read
			}
			return databases.NewDbErrorOnShardConditionFail("shard version mismatch during timer creation", nil, conflictInfo)
		}
		return databases.NewGenericDbError("failed to execute atomic timer creation transaction", transactErr)
	}

	return nil
}

func (d *DynamoDBTimerStore) CreateTimerNoLock(ctx context.Context, shardId int, namespace string, timer *databases.DbTimer) (err *databases.DbError) {
	// Serialize payload and retry policy to JSON
	var payloadJSON, retryPolicyJSON *string

	if timer.Payload != nil {
		payloadBytes, marshalErr := json.Marshal(timer.Payload)
		if marshalErr != nil {
			return databases.NewGenericDbError("failed to marshal timer payload", marshalErr)
		}
		payloadStr := string(payloadBytes)
		payloadJSON = &payloadStr
	}

	if timer.RetryPolicy != nil {
		retryPolicyBytes, marshalErr := json.Marshal(timer.RetryPolicy)
		if marshalErr != nil {
			return databases.NewGenericDbError("failed to marshal timer retry policy", marshalErr)
		}
		retryPolicyStr := string(retryPolicyBytes)
		retryPolicyJSON = &retryPolicyStr
	}

	// Create timer sort key: TIMER#<namespace>#<timer_id>
	timerSortKey := fmt.Sprintf("%s%s#%s", timerSortKeyPrefix, namespace, timer.Id)

	// Create timer item
	timerItem := map[string]types.AttributeValue{
		"shard_id":                       &types.AttributeValueMemberN{Value: strconv.Itoa(shardId)},
		"sort_key":                       &types.AttributeValueMemberS{Value: timerSortKey},
		"row_type":                       &types.AttributeValueMemberN{Value: strconv.Itoa(int(databases.RowTypeTimer))},
		"timer_execute_at":               &types.AttributeValueMemberS{Value: timer.ExecuteAt.Format(time.RFC3339Nano)},
		"timer_uuid":                     &types.AttributeValueMemberS{Value: timer.TimerUuid},
		"timer_id":                       &types.AttributeValueMemberS{Value: timer.Id},
		"timer_namespace":                &types.AttributeValueMemberS{Value: timer.Namespace},
		"timer_callback_url":             &types.AttributeValueMemberS{Value: timer.CallbackUrl},
		"timer_callback_timeout_seconds": &types.AttributeValueMemberN{Value: strconv.Itoa(int(timer.CallbackTimeoutSeconds))},
		"timer_created_at":               &types.AttributeValueMemberS{Value: timer.CreatedAt.Format(time.RFC3339Nano)},
		"timer_attempts":                 &types.AttributeValueMemberN{Value: "0"},
	}

	if payloadJSON != nil {
		timerItem["timer_payload"] = &types.AttributeValueMemberS{Value: *payloadJSON}
	}

	if retryPolicyJSON != nil {
		timerItem["timer_retry_policy"] = &types.AttributeValueMemberS{Value: *retryPolicyJSON}
	}

	// Insert the timer directly without any locking or version checking
	putItemInput := &dynamodb.PutItemInput{
		TableName: aws.String(d.tableName),
		Item:      timerItem,
	}

	_, putErr := d.client.PutItem(ctx, putItemInput)
	if putErr != nil {
		return databases.NewGenericDbError("failed to insert timer", putErr)
	}

	return nil
}

func (c *DynamoDBTimerStore) GetTimersUpToTimestamp(ctx context.Context, shardId int, namespace string, request *databases.RangeGetTimersRequest) (*databases.RangeGetTimersResponse, *databases.DbError) {
	//TODO implement me
	panic("implement me")
}

func (c *DynamoDBTimerStore) DeleteTimersUpToTimestampWithBatchInsert(ctx context.Context, shardId int, shardVersion int64, namespace string, request *databases.RangeDeleteTimersRequest, TimersToInsert []*databases.DbTimer) (*databases.RangeDeleteTimersResponse, *databases.DbError) {
	//TODO implement me
	panic("implement me")
}

func (c *DynamoDBTimerStore) BatchInsertTimers(ctx context.Context, shardId int, shardVersion int64, namespace string, TimersToInsert []*databases.DbTimer) *databases.DbError {
	//TODO implement me
	panic("implement me")
}

func (c *DynamoDBTimerStore) UpdateTimer(ctx context.Context, shardId int, shardVersion int64, namespace string, request *databases.UpdateDbTimerRequest) (err *databases.DbError) {
	//TODO implement me
	panic("implement me")
}

func (c *DynamoDBTimerStore) GetTimer(ctx context.Context, shardId int, namespace string, timerId string) (timer *databases.DbTimer, err *databases.DbError) {
	//TODO implement me
	panic("implement me")
}

func (c *DynamoDBTimerStore) DeleteTimer(ctx context.Context, shardId int, shardVersion int64, namespace string, timerId string) *databases.DbError {
	//TODO implement me
	panic("implement me")
}
