package dynamodb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"
	"github.com/google/uuid"
	appconfig "github.com/iworkflowio/durable-timer/config"
	"github.com/iworkflowio/durable-timer/databases"
)

const (
	// Sort key prefixes for unified table design
	shardSortKey       = "SHARD"
	timerSortKeyPrefix = "TIMER#"
)

// GetTimerSortKey creates a consistent timer sort key format
func GetTimerSortKey(namespace, timerId string) string {
	return fmt.Sprintf("%s%s#%s", timerSortKeyPrefix, namespace, timerId)
}

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
	ctx context.Context,
	shardId int,
	ownerAddr string,
) (prevShardInfo, currentShardInfo *databases.ShardInfo, err *databases.DbError) {
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

	result, getErr := d.client.GetItem(ctx, getItemInput)
	if getErr != nil {
		return nil, nil, databases.NewGenericDbError("failed to read shard record", getErr)
	}

	if result.Item == nil {
		// Shard doesn't exist, create it with version 1
		newVersion := int64(1)

		// Initialize with default metadata
		defaultMetadata := databases.ShardMetadata{}
		metadataJSON, marshalErr := json.Marshal(defaultMetadata)
		if marshalErr != nil {
			return nil, nil, databases.NewGenericDbError("failed to marshal default metadata", marshalErr)
		}

		// Create composite execute_at_with_uuid field for shard records (using zero values)
		shardExecuteAtWithUuid := databases.FormatExecuteAtWithUuid(databases.ZeroTimestamp, databases.ZeroUUIDString)

		item := map[string]types.AttributeValue{
			"shard_id":                   &types.AttributeValueMemberN{Value: strconv.Itoa(shardId)},
			"sort_key":                   &types.AttributeValueMemberS{Value: shardSortKey},
			"row_type":                   &types.AttributeValueMemberN{Value: strconv.Itoa(int(databases.RowTypeShard))},
			"timer_execute_at_with_uuid": &types.AttributeValueMemberS{Value: shardExecuteAtWithUuid},
			"shard_version":              &types.AttributeValueMemberN{Value: strconv.FormatInt(newVersion, 10)},
			"shard_owner_addr":           &types.AttributeValueMemberS{Value: ownerAddr},
			"shard_claimed_at":           &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)},
			"shard_metadata":             &types.AttributeValueMemberS{Value: string(metadataJSON)},
		}

		putItemInput := &dynamodb.PutItemInput{
			TableName:           aws.String(d.tableName),
			Item:                item,
			ConditionExpression: aws.String("attribute_not_exists(shard_id)"),
		}

		_, putErr := d.client.PutItem(ctx, putItemInput)
		if putErr != nil {
			// Check if it's a conditional check failed error (another instance created it concurrently)
			if isConditionalCheckFailedException(putErr) {
				return nil, nil, databases.NewDbErrorOnShardConditionFail("failed to insert shard record due to concurrent insert", putErr)
			}
			return nil, nil, databases.NewGenericDbError("failed to insert new shard record", putErr)
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
	prevShardInfo = extractShardInfoFromItem(result.Item, int64(shardId))

	// Extract current version and update with optimistic concurrency control
	currentVersion, versionErr := extractShardVersionFromItem(result.Item)
	if versionErr != nil {
		return nil, nil, databases.NewGenericDbError("failed to parse current shard version", versionErr)
	}

	newVersion := currentVersion + 1

	// Update with version check for optimistic concurrency (preserve existing metadata)
	updateExpr := "SET shard_version = :new_version, shard_owner_addr = :owner_addr, shard_claimed_at = :claimed_at"
	exprAttrValues := map[string]types.AttributeValue{
		":new_version":     &types.AttributeValueMemberN{Value: strconv.FormatInt(newVersion, 10)},
		":owner_addr":      &types.AttributeValueMemberS{Value: ownerAddr},
		":claimed_at":      &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)},
		":current_version": &types.AttributeValueMemberN{Value: strconv.FormatInt(currentVersion, 10)},
	}

	updateItemInput := &dynamodb.UpdateItemInput{
		TableName:                 aws.String(d.tableName),
		Key:                       key,
		UpdateExpression:          aws.String(updateExpr),
		ConditionExpression:       aws.String("shard_version = :current_version"),
		ExpressionAttributeValues: exprAttrValues,
	}

	_, updateErr := d.client.UpdateItem(ctx, updateItemInput)
	if updateErr != nil {
		if isConditionalCheckFailedException(updateErr) {
			// Don't read conflict info to avoid expensive extra query
			return nil, nil, databases.NewDbErrorOnShardConditionFail("shard ownership claim failed due to concurrent modification", nil)
		}
		return nil, nil, databases.NewGenericDbError("failed to update shard record", updateErr)
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

	if ownerAttr, exists := item["shard_owner_addr"]; exists {
		if ownerStr, ok := ownerAttr.(*types.AttributeValueMemberS); ok {
			info.OwnerAddr = ownerStr.Value
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
		if metadataStr, ok := metadataAttr.(*types.AttributeValueMemberS); ok && metadataStr.Value != "" {
			var shardMetadata databases.ShardMetadata
			if err := json.Unmarshal([]byte(metadataStr.Value), &shardMetadata); err == nil {
				info.Metadata = shardMetadata
			}
			// If parsing fails, use default metadata (zero values)
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

func (d *DynamoDBTimerStore) UpdateShardMetadata(
	ctx context.Context,
	shardId int, shardVersion int64,
	metadata databases.ShardMetadata,
) (err *databases.DbError) {
	// Marshal metadata to JSON
	metadataJSON, marshalErr := json.Marshal(metadata)
	if marshalErr != nil {
		return databases.NewGenericDbError("failed to marshal metadata", marshalErr)
	}

	// DynamoDB item key for shard record
	key := map[string]types.AttributeValue{
		"shard_id": &types.AttributeValueMemberN{Value: strconv.Itoa(shardId)},
		"sort_key": &types.AttributeValueMemberS{Value: shardSortKey},
	}

	// Use conditional update to update shard metadata only if the shard version matches
	updateInput := &dynamodb.UpdateItemInput{
		TableName:           aws.String(d.tableName),
		Key:                 key,
		UpdateExpression:    aws.String("SET shard_metadata = :metadata"),
		ConditionExpression: aws.String("attribute_exists(sort_key) AND shard_version = :expected_version"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":metadata":         &types.AttributeValueMemberS{Value: string(metadataJSON)},
			":expected_version": &types.AttributeValueMemberN{Value: strconv.FormatInt(shardVersion, 10)},
		},
	}

	_, updateErr := d.client.UpdateItem(ctx, updateInput)
	if updateErr != nil {
		if isConditionalCheckFailedException(updateErr) {
			// Don't read conflict info to avoid expensive extra query
			return databases.NewDbErrorOnShardConditionFail("shard version mismatch during metadata update", updateErr)
		}
		return databases.NewGenericDbError("failed to update shard metadata", updateErr)
	}

	return nil
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

	timerSortKey := GetTimerSortKey(namespace, timer.Id)

	// Create composite execute_at_with_uuid field for predictable pagination
	executeAtWithUuid := databases.FormatExecuteAtWithUuid(timer.ExecuteAt, timer.TimerUuid.String())

	// Create timer item
	timerItem := map[string]types.AttributeValue{
		"shard_id":                       &types.AttributeValueMemberN{Value: strconv.Itoa(shardId)},
		"sort_key":                       &types.AttributeValueMemberS{Value: timerSortKey},
		"row_type":                       &types.AttributeValueMemberN{Value: strconv.Itoa(int(databases.RowTypeTimer))},
		"timer_execute_at_with_uuid":     &types.AttributeValueMemberS{Value: executeAtWithUuid},
		"timer_id":                       &types.AttributeValueMemberS{Value: timer.Id},
		"timer_namespace":                &types.AttributeValueMemberS{Value: timer.Namespace},
		"timer_callback_url":             &types.AttributeValueMemberS{Value: timer.CallbackUrl},
		"timer_callback_timeout_seconds": &types.AttributeValueMemberN{Value: strconv.Itoa(int(timer.CallbackTimeoutSeconds))},
		"timer_created_at":               &types.AttributeValueMemberS{Value: timer.CreatedAt.Format(time.RFC3339Nano)},
		"timer_attempts":                 &types.AttributeValueMemberN{Value: strconv.Itoa(int(timer.Attempts))},
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
			return databases.NewDbErrorOnShardConditionFail("shard version mismatch during timer creation", nil)
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

	timerSortKey := GetTimerSortKey(namespace, timer.Id)

	// Create composite execute_at_with_uuid field for predictable pagination
	executeAtWithUuid := databases.FormatExecuteAtWithUuid(timer.ExecuteAt, timer.TimerUuid.String())

	// Create timer item
	timerItem := map[string]types.AttributeValue{
		"shard_id":                       &types.AttributeValueMemberN{Value: strconv.Itoa(shardId)},
		"sort_key":                       &types.AttributeValueMemberS{Value: timerSortKey},
		"row_type":                       &types.AttributeValueMemberN{Value: strconv.Itoa(int(databases.RowTypeTimer))},
		"timer_execute_at_with_uuid":     &types.AttributeValueMemberS{Value: executeAtWithUuid},
		"timer_id":                       &types.AttributeValueMemberS{Value: timer.Id},
		"timer_namespace":                &types.AttributeValueMemberS{Value: timer.Namespace},
		"timer_callback_url":             &types.AttributeValueMemberS{Value: timer.CallbackUrl},
		"timer_callback_timeout_seconds": &types.AttributeValueMemberN{Value: strconv.Itoa(int(timer.CallbackTimeoutSeconds))},
		"timer_created_at":               &types.AttributeValueMemberS{Value: timer.CreatedAt.Format(time.RFC3339Nano)},
		"timer_attempts":                 &types.AttributeValueMemberN{Value: strconv.Itoa(int(timer.Attempts))},
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

func (d *DynamoDBTimerStore) RangeGetTimers(ctx context.Context, shardId int, request *databases.RangeGetTimersRequest) (*databases.RangeGetTimersResponse, *databases.DbError) {
	// Create bounds for execute_at_with_uuid comparison
	lowerBound := databases.FormatExecuteAtWithUuid(request.StartTimestamp, request.StartTimeUuid.String())
	upperBound := databases.FormatExecuteAtWithUuid(request.EndTimestamp, request.EndTimeUuid.String())

	// Query using the ExecuteAtWithUuidIndex LSI with range
	queryInput := &dynamodb.QueryInput{
		TableName:              aws.String(d.tableName),
		IndexName:              aws.String("ExecuteAtWithUuidIndex"),
		KeyConditionExpression: aws.String("shard_id = :shard_id AND timer_execute_at_with_uuid BETWEEN :lower_bound AND :upper_bound"),
		FilterExpression:       aws.String("row_type = :timer_row_type"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":shard_id":       &types.AttributeValueMemberN{Value: strconv.Itoa(shardId)},
			":lower_bound":    &types.AttributeValueMemberS{Value: lowerBound},
			":upper_bound":    &types.AttributeValueMemberS{Value: upperBound},
			":timer_row_type": &types.AttributeValueMemberN{Value: strconv.Itoa(int(databases.RowTypeTimer))},
		},
		Limit:            aws.Int32(int32(request.Limit + 1)), // Account for potential shard record filtering
		ScanIndexForward: aws.Bool(true),                      // Sort ascending by timer_execute_at_with_uuid
	}

	result, err := d.client.Query(ctx, queryInput)
	if err != nil {
		return nil, databases.NewGenericDbError("failed to query timers", err)
	}

	var timers []*databases.DbTimer

	for _, item := range result.Items {
		// Parse timer_execute_at_with_uuid to get individual execute_at and uuid
		executeAtWithUuidStr := item["timer_execute_at_with_uuid"].(*types.AttributeValueMemberS).Value
		executeAt, timerUuidStr, parseErr := databases.ParseExecuteAtWithUuid(executeAtWithUuidStr)
		if parseErr != nil {
			return nil, databases.NewGenericDbError("failed to parse timer_execute_at_with_uuid", parseErr)
		}

		// Parse timer UUID
		timerUuid, uuidErr := uuid.Parse(timerUuidStr)
		if uuidErr != nil {
			return nil, databases.NewGenericDbError("failed to parse timer UUID", uuidErr)
		}

		// Parse other fields
		timerId := item["timer_id"].(*types.AttributeValueMemberS).Value
		timerNamespace := item["timer_namespace"].(*types.AttributeValueMemberS).Value
		callbackUrl := item["timer_callback_url"].(*types.AttributeValueMemberS).Value

		timeoutSecondsStr := item["timer_callback_timeout_seconds"].(*types.AttributeValueMemberN).Value
		timeoutSeconds, _ := strconv.Atoi(timeoutSecondsStr)

		createdAtStr := item["timer_created_at"].(*types.AttributeValueMemberS).Value
		createdAt, _ := time.Parse(time.RFC3339Nano, createdAtStr)

		attemptsStr := item["timer_attempts"].(*types.AttributeValueMemberN).Value
		attempts, _ := strconv.Atoi(attemptsStr)

		// Parse JSON payload and retry policy
		var payload interface{}
		var retryPolicy interface{}

		if payloadAttr, exists := item["timer_payload"]; exists {
			payloadStr := payloadAttr.(*types.AttributeValueMemberS).Value
			if payloadStr != "" {
				if err := json.Unmarshal([]byte(payloadStr), &payload); err != nil {
					return nil, databases.NewGenericDbError("failed to unmarshal timer payload", err)
				}
			}
		}

		if retryPolicyAttr, exists := item["timer_retry_policy"]; exists {
			retryPolicyStr := retryPolicyAttr.(*types.AttributeValueMemberS).Value
			if retryPolicyStr != "" {
				if err := json.Unmarshal([]byte(retryPolicyStr), &retryPolicy); err != nil {
					return nil, databases.NewGenericDbError("failed to unmarshal timer retry policy", err)
				}
			}
		}

		timer := &databases.DbTimer{
			Id:                     timerId,
			TimerUuid:              timerUuid,
			Namespace:              timerNamespace,
			ExecuteAt:              executeAt,
			CallbackUrl:            callbackUrl,
			Payload:                payload,
			RetryPolicy:            retryPolicy,
			CallbackTimeoutSeconds: int32(timeoutSeconds),
			CreatedAt:              createdAt,
			Attempts:               int32(attempts),
		}

		timers = append(timers, timer)

		// Stop if we've reached the requested limit
		if len(timers) >= request.Limit {
			break
		}
	}

	return &databases.RangeGetTimersResponse{
		Timers: timers,
	}, nil
}

func (d *DynamoDBTimerStore) RangeDeleteWithBatchInsertTxn(ctx context.Context, shardId int, shardVersion int64, request *databases.RangeDeleteTimersRequest, TimersToInsert []*databases.DbTimer) (*databases.RangeDeleteTimersResponse, *databases.DbError) {
	// Create bounds for execute_at_with_uuid comparison
	lowerBound := databases.FormatExecuteAtWithUuid(request.StartTimestamp, request.StartTimeUuid.String())
	upperBound := databases.FormatExecuteAtWithUuid(request.EndTimestamp, request.EndTimeUuid.String())

	// First, query timers to delete to get their exact keys
	// TODO: this is not efficient, ideally it should use a range delete instead
	// However, DynamoDB does not support range delete, so we need to use a query and delete one by one
	// In the future, we should just pass in the timers directly and delete timers using batch delete, without querying
	queryInput := &dynamodb.QueryInput{
		TableName:              aws.String(d.tableName),
		IndexName:              aws.String("ExecuteAtWithUuidIndex"),
		KeyConditionExpression: aws.String("shard_id = :shard_id AND timer_execute_at_with_uuid BETWEEN :lower_bound AND :upper_bound"),
		FilterExpression:       aws.String("row_type = :timer_row_type"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":shard_id":       &types.AttributeValueMemberN{Value: strconv.Itoa(shardId)},
			":lower_bound":    &types.AttributeValueMemberS{Value: lowerBound},
			":upper_bound":    &types.AttributeValueMemberS{Value: upperBound},
			":timer_row_type": &types.AttributeValueMemberN{Value: strconv.Itoa(int(databases.RowTypeTimer))},
		},
		ScanIndexForward: aws.Bool(true),
	}

	result, err := d.client.Query(ctx, queryInput)
	if err != nil {
		return nil, databases.NewGenericDbError("failed to query timers to delete", err)
	}

	// Build list of transaction write items
	var transactItems []types.TransactWriteItem
	deletedCount := len(result.Items)

	// Prepare shard key for condition check
	shardKey := map[string]types.AttributeValue{
		"shard_id": &types.AttributeValueMemberN{Value: strconv.Itoa(shardId)},
		"sort_key": &types.AttributeValueMemberS{Value: shardSortKey},
	}

	// Add shard version check
	transactItems = append(transactItems, types.TransactWriteItem{
		ConditionCheck: &types.ConditionCheck{
			TableName:           aws.String(d.tableName),
			Key:                 shardKey,
			ConditionExpression: aws.String("shard_version = :expected_version"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":expected_version": &types.AttributeValueMemberN{Value: strconv.FormatInt(shardVersion, 10)},
			},
		},
	})

	// Add delete operations for found timers
	for _, item := range result.Items {
		sortKeyAttr, sortKeyExists := item["sort_key"]
		shardIdAttr, shardIdExists := item["shard_id"]

		if !sortKeyExists || !shardIdExists {
			return nil, databases.NewGenericDbError("missing required key attributes in timer item", nil)
		}

		itemKey := map[string]types.AttributeValue{
			"shard_id": shardIdAttr,
			"sort_key": sortKeyAttr,
		}

		transactItems = append(transactItems, types.TransactWriteItem{
			Delete: &types.Delete{
				TableName: aws.String(d.tableName),
				Key:       itemKey,
			},
		})
	}

	// Add insert operations for new timers
	for _, timer := range TimersToInsert {
		// Serialize payload and retry policy to JSON
		var payloadJSON, retryPolicyJSON *string

		if timer.Payload != nil {
			payloadBytes, marshalErr := json.Marshal(timer.Payload)
			if marshalErr != nil {
				return nil, databases.NewGenericDbError("failed to marshal timer payload", marshalErr)
			}
			payloadStr := string(payloadBytes)
			payloadJSON = &payloadStr
		}

		if timer.RetryPolicy != nil {
			retryPolicyBytes, marshalErr := json.Marshal(timer.RetryPolicy)
			if marshalErr != nil {
				return nil, databases.NewGenericDbError("failed to marshal timer retry policy", marshalErr)
			}
			retryPolicyStr := string(retryPolicyBytes)
			retryPolicyJSON = &retryPolicyStr
		}

		timerSortKey := GetTimerSortKey(timer.Namespace, timer.Id)

		// Create composite execute_at_with_uuid field for LSI
		executeAtWithUuid := databases.FormatExecuteAtWithUuid(timer.ExecuteAt, timer.TimerUuid.String())

		timerItem := map[string]types.AttributeValue{
			"shard_id":                       &types.AttributeValueMemberN{Value: strconv.Itoa(shardId)},
			"sort_key":                       &types.AttributeValueMemberS{Value: timerSortKey},
			"row_type":                       &types.AttributeValueMemberN{Value: strconv.Itoa(int(databases.RowTypeTimer))},
			"timer_execute_at_with_uuid":     &types.AttributeValueMemberS{Value: executeAtWithUuid},
			"timer_id":                       &types.AttributeValueMemberS{Value: timer.Id},
			"timer_namespace":                &types.AttributeValueMemberS{Value: timer.Namespace},
			"timer_callback_url":             &types.AttributeValueMemberS{Value: timer.CallbackUrl},
			"timer_callback_timeout_seconds": &types.AttributeValueMemberN{Value: strconv.Itoa(int(timer.CallbackTimeoutSeconds))},
			"timer_created_at":               &types.AttributeValueMemberS{Value: timer.CreatedAt.Format(time.RFC3339Nano)},
			"timer_attempts":                 &types.AttributeValueMemberN{Value: strconv.Itoa(int(timer.Attempts))},
		}

		if payloadJSON != nil {
			timerItem["timer_payload"] = &types.AttributeValueMemberS{Value: *payloadJSON}
		}

		if retryPolicyJSON != nil {
			timerItem["timer_retry_policy"] = &types.AttributeValueMemberS{Value: *retryPolicyJSON}
		}

		transactItems = append(transactItems, types.TransactWriteItem{
			Put: &types.Put{
				TableName: aws.String(d.tableName),
				Item:      timerItem,
			},
		})
	}

	// Execute the transaction if there are any operations beyond the shard check
	if len(transactItems) > 1 {
		transactInput := &dynamodb.TransactWriteItemsInput{
			TransactItems: transactItems,
		}

		_, transactErr := d.client.TransactWriteItems(ctx, transactInput)
		if transactErr != nil {
			if isConditionalCheckFailedException(transactErr) {
				// Shard version mismatch
				return nil, databases.NewDbErrorOnShardConditionFail("shard version mismatch during delete and insert operation", nil)
			}
			return nil, databases.NewGenericDbError("failed to execute atomic delete and insert transaction", transactErr)
		}
	}

	return &databases.RangeDeleteTimersResponse{
		DeletedCount: deletedCount,
	}, nil
}

func (d *DynamoDBTimerStore) RangeDeleteWithLimit(ctx context.Context, shardId int, request *databases.RangeDeleteTimersRequest, limit int) (*databases.RangeDeleteTimersResponse, *databases.DbError) {
	// Create bounds for execute_at_with_uuid comparison
	lowerBound := databases.FormatExecuteAtWithUuid(request.StartTimestamp, request.StartTimeUuid.String())
	upperBound := databases.FormatExecuteAtWithUuid(request.EndTimestamp, request.EndTimeUuid.String())

	// Build query input with optional limit
	queryInput := &dynamodb.QueryInput{
		TableName:              aws.String(d.tableName),
		IndexName:              aws.String("ExecuteAtWithUuidIndex"),
		KeyConditionExpression: aws.String("shard_id = :shard_id AND timer_execute_at_with_uuid BETWEEN :lower_bound AND :upper_bound"),
		FilterExpression:       aws.String("row_type = :timer_row_type"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":shard_id":       &types.AttributeValueMemberN{Value: strconv.Itoa(shardId)},
			":lower_bound":    &types.AttributeValueMemberS{Value: lowerBound},
			":upper_bound":    &types.AttributeValueMemberS{Value: upperBound},
			":timer_row_type": &types.AttributeValueMemberN{Value: strconv.Itoa(int(databases.RowTypeTimer))},
		},
		ScanIndexForward: aws.Bool(true),
	}

	// Apply limit if specified
	if limit > 0 {
		queryInput.Limit = aws.Int32(int32(limit))
	}

	result, err := d.client.Query(ctx, queryInput)
	if err != nil {
		return nil, databases.NewGenericDbError("failed to query timers to delete", err)
	}

	if len(result.Items) == 0 {
		return &databases.RangeDeleteTimersResponse{DeletedCount: 0}, nil
	}

	// Delete timers using batch delete (DynamoDB has 25 items limit per batch)
	deletedCount := 0
	const batchSize = 25

	for i := 0; i < len(result.Items); i += batchSize {
		end := i + batchSize
		if end > len(result.Items) {
			end = len(result.Items)
		}

		// Prepare batch delete request
		var writeRequests []types.WriteRequest
		for j := i; j < end; j++ {
			item := result.Items[j]
			deleteRequest := types.WriteRequest{
				DeleteRequest: &types.DeleteRequest{
					Key: map[string]types.AttributeValue{
						"shard_id": item["shard_id"],
						"sort_key": item["sort_key"],
					},
				},
			}
			writeRequests = append(writeRequests, deleteRequest)
		}

		// Execute batch write
		batchWriteInput := &dynamodb.BatchWriteItemInput{
			RequestItems: map[string][]types.WriteRequest{
				d.tableName: writeRequests,
			},
		}

		batchResult, batchErr := d.client.BatchWriteItem(ctx, batchWriteInput)
		if batchErr != nil {
			return nil, databases.NewGenericDbError("failed to batch delete timers", batchErr)
		}

		// Handle unprocessed items (retry logic could be added here if needed)
		if len(batchResult.UnprocessedItems) > 0 {
			// For simplicity, we'll return an error if there are unprocessed items
			// In production, you might want to implement retry logic
			return nil, databases.NewGenericDbError("batch delete had unprocessed items", nil)
		}

		deletedCount += len(writeRequests)
	}

	return &databases.RangeDeleteTimersResponse{
		DeletedCount: deletedCount,
	}, nil
}

func (d *DynamoDBTimerStore) UpdateTimer(ctx context.Context, shardId int, shardVersion int64, namespace string, request *databases.UpdateDbTimerRequest) (err *databases.DbError) {
	// For DynamoDB, ExecuteAt is part of the LSI sort key, making ExecuteAt changes complex (requires delete+insert)
	// To keep the implementation simple and optimized, we don't support ExecuteAt updates
	if !request.ExecuteAt.IsZero() {
		return databases.NewDbErrorNotSupport("UpdateTimer does not support ExecuteAt changes in DynamoDB implementation", nil)
	}

	// Construct the sort key for the timer
	timerSortKey := GetTimerSortKey(namespace, request.TimerId)

	// Serialize payload and retry policy if provided
	var payloadJSON, retryPolicyJSON *string
	if request.Payload != nil {
		payloadBytes, marshalErr := json.Marshal(request.Payload)
		if marshalErr != nil {
			return databases.NewGenericDbError("failed to marshal timer payload", marshalErr)
		}
		payloadStr := string(payloadBytes)
		payloadJSON = &payloadStr
	}

	if request.RetryPolicy != nil {
		retryPolicyBytes, marshalErr := json.Marshal(request.RetryPolicy)
		if marshalErr != nil {
			return databases.NewGenericDbError("failed to marshal timer retry policy", marshalErr)
		}
		retryPolicyStr := string(retryPolicyBytes)
		retryPolicyJSON = &retryPolicyStr
	}

	// Perform in-place update for supported fields
	return d.updateTimerInPlace(ctx, shardId, shardVersion, namespace, request, timerSortKey, payloadJSON, retryPolicyJSON)
}

func (d *DynamoDBTimerStore) updateTimerInPlace(ctx context.Context, shardId int, shardVersion int64, namespace string, request *databases.UpdateDbTimerRequest, timerSortKey string, payloadJSON, retryPolicyJSON *string) *databases.DbError {
	// Use TransactWriteItems for atomic update with shard version check

	// Build update expression and attribute values
	updateExpressions := []string{}
	expressionAttributeValues := map[string]types.AttributeValue{}

	// Update specific fields
	if request.CallbackUrl != "" {
		updateExpressions = append(updateExpressions, "timer_callback_url = :callback_url")
		expressionAttributeValues[":callback_url"] = &types.AttributeValueMemberS{Value: request.CallbackUrl}
	}

	if request.CallbackTimeoutSeconds > 0 {
		updateExpressions = append(updateExpressions, "timer_callback_timeout_seconds = :timeout_seconds")
		expressionAttributeValues[":timeout_seconds"] = &types.AttributeValueMemberN{Value: strconv.Itoa(int(request.CallbackTimeoutSeconds))}
	}

	// Note: ExecuteAt updates are handled differently since they affect the LSI key
	// For in-place updates, we don't update ExecuteAt to avoid changing the UUID
	// ExecuteAt changes should be handled by the delete+insert path

	if payloadJSON != nil {
		updateExpressions = append(updateExpressions, "timer_payload = :payload")
		expressionAttributeValues[":payload"] = &types.AttributeValueMemberS{Value: *payloadJSON}
	}

	if retryPolicyJSON != nil {
		updateExpressions = append(updateExpressions, "timer_retry_policy = :retry_policy")
		expressionAttributeValues[":retry_policy"] = &types.AttributeValueMemberS{Value: *retryPolicyJSON}
	}

	if len(updateExpressions) == 0 {
		return nil // Nothing to update
	}

	// Transaction items
	transactItems := []types.TransactWriteItem{
		// Update timer with condition that it exists
		{
			Update: &types.Update{
				TableName: aws.String(d.tableName),
				Key: map[string]types.AttributeValue{
					"shard_id": &types.AttributeValueMemberN{Value: strconv.Itoa(shardId)},
					"sort_key": &types.AttributeValueMemberS{Value: timerSortKey},
				},
				UpdateExpression:          aws.String("SET " + strings.Join(updateExpressions, ", ")),
				ConditionExpression:       aws.String("attribute_exists(sort_key)"),
				ExpressionAttributeValues: expressionAttributeValues,
			},
		},
		// Update shard version (with condition check)
		{
			Update: &types.Update{
				TableName: aws.String(d.tableName),
				Key: map[string]types.AttributeValue{
					"shard_id": &types.AttributeValueMemberN{Value: strconv.Itoa(shardId)},
					"sort_key": &types.AttributeValueMemberS{Value: shardSortKey},
				},
				UpdateExpression:    aws.String("SET shard_version = shard_version + :inc"),
				ConditionExpression: aws.String("shard_version = :shard_version"),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":inc":           &types.AttributeValueMemberN{Value: "1"},
					":shard_version": &types.AttributeValueMemberN{Value: strconv.FormatInt(shardVersion, 10)},
				},
			},
		},
	}

	// Execute transaction
	transactInput := &dynamodb.TransactWriteItemsInput{
		TransactItems: transactItems,
	}

	_, transactErr := d.client.TransactWriteItems(ctx, transactInput)
	if transactErr != nil {
		return d.handleTransactionError(transactErr, "update timer")
	}

	return nil
}

// handleTransactionError handles DynamoDB transaction errors and converts them to appropriate DbError types
func (d *DynamoDBTimerStore) handleTransactionError(err error, operation string) *databases.DbError {
	var transactionCanceledException *types.TransactionCanceledException
	if errors.As(err, &transactionCanceledException) {
		// Check which condition failed
		for _, reason := range transactionCanceledException.CancellationReasons {
			if reason.Code != nil {
				switch *reason.Code {
				case "ConditionalCheckFailed":
					// This could be either a shard version mismatch or timer not found
					// For simplicity, assume shard version mismatch (more common case)
					return databases.NewDbErrorOnShardConditionFail("shard version mismatch during "+operation, err)
				case "ValidationException":
					return databases.NewGenericDbError("validation error during "+operation, err)
				}
			}
		}
		return databases.NewGenericDbError("transaction cancelled during "+operation, err)
	}

	// Handle other DynamoDB errors
	return databases.NewGenericDbError("failed to "+operation, err)
}

func (d *DynamoDBTimerStore) GetTimer(ctx context.Context, shardId int, namespace string, timerId string) (timer *databases.DbTimer, err *databases.DbError) {
	// Construct the sort key for the timer
	timerSortKey := GetTimerSortKey(namespace, timerId)

	// Query the timer directly using partition key and sort key
	getInput := &dynamodb.GetItemInput{
		TableName: aws.String(d.tableName),
		Key: map[string]types.AttributeValue{
			"shard_id": &types.AttributeValueMemberN{Value: strconv.Itoa(shardId)},
			"sort_key": &types.AttributeValueMemberS{Value: timerSortKey},
		},
	}

	result, getErr := d.client.GetItem(ctx, getInput)
	if getErr != nil {
		return nil, databases.NewGenericDbError("failed to get timer", getErr)
	}

	// Check if timer exists
	if result.Item == nil {
		return nil, databases.NewDbErrorNotExists("timer not found", nil)
	}

	// Parse timer_execute_at_with_uuid to get individual execute_at and uuid
	executeAtWithUuidStr := result.Item["timer_execute_at_with_uuid"].(*types.AttributeValueMemberS).Value
	executeAt, timerUuidStr, parseErr := databases.ParseExecuteAtWithUuid(executeAtWithUuidStr)
	if parseErr != nil {
		return nil, databases.NewGenericDbError("failed to parse timer_execute_at_with_uuid", parseErr)
	}

	// Parse timer UUID
	timerUuid, uuidErr := uuid.Parse(timerUuidStr)
	if uuidErr != nil {
		return nil, databases.NewGenericDbError("failed to parse timer UUID", uuidErr)
	}

	// Parse other fields
	callbackUrl := result.Item["timer_callback_url"].(*types.AttributeValueMemberS).Value

	timeoutSecondsStr := result.Item["timer_callback_timeout_seconds"].(*types.AttributeValueMemberN).Value
	timeoutSeconds, _ := strconv.Atoi(timeoutSecondsStr)

	createdAtStr := result.Item["timer_created_at"].(*types.AttributeValueMemberS).Value
	createdAt, _ := time.Parse(time.RFC3339Nano, createdAtStr)

	attemptsStr := result.Item["timer_attempts"].(*types.AttributeValueMemberN).Value
	attempts, _ := strconv.Atoi(attemptsStr)

	// Parse JSON payload and retry policy
	var payload interface{}
	var retryPolicy interface{}

	if payloadAttr, exists := result.Item["timer_payload"]; exists {
		payloadStr := payloadAttr.(*types.AttributeValueMemberS).Value
		if payloadStr != "" {
			if unmarshalErr := json.Unmarshal([]byte(payloadStr), &payload); unmarshalErr != nil {
				return nil, databases.NewGenericDbError("failed to unmarshal timer payload", unmarshalErr)
			}
		}
	}

	if retryPolicyAttr, exists := result.Item["timer_retry_policy"]; exists {
		retryPolicyStr := retryPolicyAttr.(*types.AttributeValueMemberS).Value
		if retryPolicyStr != "" {
			if unmarshalErr := json.Unmarshal([]byte(retryPolicyStr), &retryPolicy); unmarshalErr != nil {
				return nil, databases.NewGenericDbError("failed to unmarshal timer retry policy", unmarshalErr)
			}
		}
	}

	timer = &databases.DbTimer{
		Id:                     timerId,
		TimerUuid:              timerUuid,
		Namespace:              namespace,
		ExecuteAt:              executeAt,
		CallbackUrl:            callbackUrl,
		Payload:                payload,
		RetryPolicy:            retryPolicy,
		CallbackTimeoutSeconds: int32(timeoutSeconds),
		CreatedAt:              createdAt,
		Attempts:               int32(attempts),
	}

	return timer, nil
}

func (d *DynamoDBTimerStore) UpdateTimerNoLock(ctx context.Context, shardId int, namespace string, request *databases.UpdateDbTimerRequest) (err *databases.DbError) {
	// For DynamoDB, ExecuteAt is part of the LSI sort key, making ExecuteAt changes complex (requires delete+insert)
	// To keep the implementation simple and optimized, we don't support ExecuteAt updates
	if !request.ExecuteAt.IsZero() {
		return databases.NewDbErrorNotSupport("UpdateTimerNoLock does not support ExecuteAt changes in DynamoDB implementation", nil)
	}

	// Construct the sort key for the timer
	timerSortKey := GetTimerSortKey(namespace, request.TimerId)

	// Serialize payload and retry policy if provided
	var payloadJSON, retryPolicyJSON *string
	if request.Payload != nil {
		payloadBytes, marshalErr := json.Marshal(request.Payload)
		if marshalErr != nil {
			return databases.NewGenericDbError("failed to marshal timer payload", marshalErr)
		}
		payloadStr := string(payloadBytes)
		payloadJSON = &payloadStr
	}

	if request.RetryPolicy != nil {
		retryPolicyBytes, marshalErr := json.Marshal(request.RetryPolicy)
		if marshalErr != nil {
			return databases.NewGenericDbError("failed to marshal timer retry policy", marshalErr)
		}
		retryPolicyStr := string(retryPolicyBytes)
		retryPolicyJSON = &retryPolicyStr
	}

	// Build update expression and attribute values
	updateExpressions := []string{}
	expressionAttributeValues := map[string]types.AttributeValue{}

	// Update specific fields
	if request.CallbackUrl != "" {
		updateExpressions = append(updateExpressions, "timer_callback_url = :callback_url")
		expressionAttributeValues[":callback_url"] = &types.AttributeValueMemberS{Value: request.CallbackUrl}
	}

	if request.CallbackTimeoutSeconds > 0 {
		updateExpressions = append(updateExpressions, "timer_callback_timeout_seconds = :timeout_seconds")
		expressionAttributeValues[":timeout_seconds"] = &types.AttributeValueMemberN{Value: strconv.Itoa(int(request.CallbackTimeoutSeconds))}
	}

	if payloadJSON != nil {
		updateExpressions = append(updateExpressions, "timer_payload = :payload")
		expressionAttributeValues[":payload"] = &types.AttributeValueMemberS{Value: *payloadJSON}
	}

	if retryPolicyJSON != nil {
		updateExpressions = append(updateExpressions, "timer_retry_policy = :retry_policy")
		expressionAttributeValues[":retry_policy"] = &types.AttributeValueMemberS{Value: *retryPolicyJSON}
	}

	if len(updateExpressions) == 0 {
		return nil // Nothing to update
	}

	// Perform update without shard version check (NoLock version)
	updateInput := &dynamodb.UpdateItemInput{
		TableName: aws.String(d.tableName),
		Key: map[string]types.AttributeValue{
			"shard_id": &types.AttributeValueMemberN{Value: strconv.Itoa(shardId)},
			"sort_key": &types.AttributeValueMemberS{Value: timerSortKey},
		},
		UpdateExpression:          aws.String("SET " + strings.Join(updateExpressions, ", ")),
		ConditionExpression:       aws.String("attribute_exists(sort_key)"),
		ExpressionAttributeValues: expressionAttributeValues,
	}

	_, updateErr := d.client.UpdateItem(ctx, updateInput)
	if updateErr != nil {
		if isConditionalCheckFailedException(updateErr) {
			// Timer doesn't exist
			return databases.NewDbErrorNotExists("timer not found", updateErr)
		}
		return databases.NewGenericDbError("failed to update timer", updateErr)
	}

	return nil
}

func (d *DynamoDBTimerStore) DeleteTimerNoLock(ctx context.Context, shardId int, namespace string, timerId string) *databases.DbError {
	// Construct the sort key for the timer
	timerSortKey := GetTimerSortKey(namespace, timerId)

	// Delete the timer with condition check to ensure it exists
	// This matches the behavior of MySQL and PostgreSQL implementations
	deleteInput := &dynamodb.DeleteItemInput{
		TableName: aws.String(d.tableName),
		Key: map[string]types.AttributeValue{
			"shard_id": &types.AttributeValueMemberN{Value: strconv.Itoa(shardId)},
			"sort_key": &types.AttributeValueMemberS{Value: timerSortKey},
		},
		ConditionExpression: aws.String("attribute_exists(sort_key)"),
	}

	_, deleteErr := d.client.DeleteItem(ctx, deleteInput)
	if deleteErr != nil {
		if isConditionalCheckFailedException(deleteErr) {
			// Timer doesn't exist
			return databases.NewDbErrorNotExists("timer not found", deleteErr)
		}
		return databases.NewGenericDbError("failed to delete timer", deleteErr)
	}

	return nil
}

func (d *DynamoDBTimerStore) DeleteTimer(ctx context.Context, shardId int, shardVersion int64, namespace string, timerId string) *databases.DbError {
	// Construct the sort key for the timer
	timerSortKey := GetTimerSortKey(namespace, timerId)

	// Use TransactWriteItems for atomic delete with shard version check
	transactItems := []types.TransactWriteItem{
		// Delete timer with condition that it exists
		{
			Delete: &types.Delete{
				TableName: aws.String(d.tableName),
				Key: map[string]types.AttributeValue{
					"shard_id": &types.AttributeValueMemberN{Value: strconv.Itoa(shardId)},
					"sort_key": &types.AttributeValueMemberS{Value: timerSortKey},
				},
				ConditionExpression: aws.String("attribute_exists(sort_key)"),
			},
		},
		// Update shard version (with condition check)
		{
			Update: &types.Update{
				TableName: aws.String(d.tableName),
				Key: map[string]types.AttributeValue{
					"shard_id": &types.AttributeValueMemberN{Value: strconv.Itoa(shardId)},
					"sort_key": &types.AttributeValueMemberS{Value: shardSortKey},
				},
				UpdateExpression:    aws.String("SET shard_version = shard_version + :inc"),
				ConditionExpression: aws.String("shard_version = :shard_version"),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":inc":           &types.AttributeValueMemberN{Value: "1"},
					":shard_version": &types.AttributeValueMemberN{Value: strconv.FormatInt(shardVersion, 10)},
				},
			},
		},
	}

	// Execute transaction
	transactInput := &dynamodb.TransactWriteItemsInput{
		TransactItems: transactItems,
	}

	_, transactErr := d.client.TransactWriteItems(ctx, transactInput)
	if transactErr != nil {
		// Check if the error is because timer doesn't exist (for idempotent behavior)
		var transactionCanceledException *types.TransactionCanceledException
		if errors.As(transactErr, &transactionCanceledException) {
			// Check which specific condition failed
			for i, reason := range transactionCanceledException.CancellationReasons {
				if reason.Code != nil && *reason.Code == "ConditionalCheckFailed" {
					if i == 0 {
						// First item is the timer delete - timer doesn't exist, that's OK (idempotent)
						return nil
					}
					if i == 1 {
						// Second item is shard version check - this is a real error
						return databases.NewDbErrorOnShardConditionFail("shard version mismatch during delete timer", transactErr)
					}
				}
			}
		}
		return d.handleTransactionError(transactErr, "delete timer")
	}

	return nil
}
