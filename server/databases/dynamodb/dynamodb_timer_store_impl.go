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
	ctx context.Context, shardId int, ownerAddr string, metadata interface{},
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
	updateExpr := "SET shard_version = :new_version, shard_owner_addr = :owner_addr, shard_claimed_at = :claimed_at"
	exprAttrValues := map[string]types.AttributeValue{
		":new_version":     &types.AttributeValueMemberN{Value: strconv.FormatInt(newVersion, 10)},
		":owner_addr":        &types.AttributeValueMemberS{Value: ownerAddr},
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

func (d *DynamoDBTimerStore) GetTimersUpToTimestamp(ctx context.Context, shardId int, request *databases.RangeGetTimersRequest) (*databases.RangeGetTimersResponse, *databases.DbError) {
	// Create upper bound for execute_at_with_uuid comparison
	upperBound := databases.FormatExecuteAtWithUuid(request.UpToTimestamp, "ffffffff-ffff-ffff-ffff-ffffffffffff")

	// Query using the ExecuteAtWithUuidIndex LSI
	queryInput := &dynamodb.QueryInput{
		TableName:              aws.String(d.tableName),
		IndexName:              aws.String("ExecuteAtWithUuidIndex"),
		KeyConditionExpression: aws.String("shard_id = :shard_id AND timer_execute_at_with_uuid <= :upper_bound"),
		FilterExpression:       aws.String("row_type = :timer_row_type"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":shard_id":       &types.AttributeValueMemberN{Value: strconv.Itoa(shardId)},
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

func (d *DynamoDBTimerStore) DeleteTimersUpToTimestampWithBatchInsert(ctx context.Context, shardId int, shardVersion int64, request *databases.RangeDeleteTimersRequest, TimersToInsert []*databases.DbTimer) (*databases.RangeDeleteTimersResponse, *databases.DbError) {
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
			"timer_attempts":                 &types.AttributeValueMemberN{Value: "0"},
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
				conflictInfo := &databases.ShardInfo{
					ShardVersion: 0, // Unknown version to avoid expensive read
				}
				return nil, databases.NewDbErrorOnShardConditionFail("shard version mismatch during delete and insert operation", nil, conflictInfo)
			}
			return nil, databases.NewGenericDbError("failed to execute atomic delete and insert transaction", transactErr)
		}
	}

	return &databases.RangeDeleteTimersResponse{
		DeletedCount: deletedCount,
	}, nil
}

func (c *DynamoDBTimerStore) BatchInsertTimers(ctx context.Context, shardId int, shardVersion int64, TimersToInsert []*databases.DbTimer) *databases.DbError {
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
