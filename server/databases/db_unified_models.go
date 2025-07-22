package databases

import (
	"github.com/gocql/gocql"
	"time"
)

const RowTypeShard = int16(1) // 1 = shard record
const RowTypeTimer = int16(2) // 2 = timer record

// ZeroTimestamp and ZeroUUID are for timer fields since they're part of primary key but not used for shard records
// 1969-12-31 16:00:00 -0800 PST and 00000000-0000-0000-0000-000000000000
var ZeroTimestamp = time.Unix(0, 0) // Unix epoch
var ZeroUUID = gocql.UUID{}         // Zero UUID

type (
	ShardInfo struct {
		ShardId      int64
		OwnerId      string
		ShardVersion int64
		Metadata     interface{}
		ClaimedAt    time.Time
	}

	DbError struct {
		OriginalError      error
		CustomMessage      string
		ShardConditionFail bool
		ConflictShardInfo  *ShardInfo
		NotExists          bool
	}

	// DbTimer is the timer model stored in DB
	DbTimer struct {

		// Unique identifier for the timer
		Id string `json:"id"`

		// Group identifier for the timer. It is used for scalability. Must be one of the groupIds enabled in the system. Must be provided in read/write operation requests for lookup.
		GroupId string `json:"groupId"`

		// When the timer is scheduled to execute
		ExecuteAt time.Time `json:"executeAt"`

		// HTTP URL to call when executing, returning 200 with CallbackResponse means success, otherwise will be retried.
		CallbackUrl string `json:"callbackUrl"`

		// Custom payload data
		Payload interface{} `json:"payload,omitempty"`

		RetryPolicy interface{} `json:"retryPolicy,omitempty"`

		// Timeout for the HTTP callback in seconds
		CallbackTimeoutSeconds int32 `json:"callbackTimeoutSeconds,omitempty"`

		// When the timer was created
		CreatedAt time.Time `json:"createdAt"`

		// When the timer was last updated
		UpdatedAt time.Time `json:"updatedAt"`

		// When the timer was executed (if applicable)
		ExecutedAt time.Time `json:"executedAt,omitempty"`
	}

	RangeGetTimersRequest struct {
		UpToTimestamp time.Time
		Limit         int
	}

	RangeGetTimersResponse struct {
		Timers []*DbTimer
	}

	// RangeDeleteTimersRequest is the request to delete timers from a range of timestamps
	RangeDeleteTimersRequest struct {
		StartTimestamp time.Time
		EndTimestamp   time.Time
	}

	RangeDeleteTimersResponse struct {
		DeletedCount int
	}

	UpdateDbTimerRequest struct {

		// New execution time for the timer
		ExecuteAt time.Time `json:"executeAt,omitempty"`

		// New callback URL, returning 200 with CallbackResponse means success, otherwise will be retried.
		CallbackUrl string `json:"callbackUrl,omitempty"`

		// New payload data
		Payload interface{} `json:"payload,omitempty"`

		RetryPolicy interface{} `json:"retryPolicy,omitempty"`

		// New timeout for the HTTP callback in seconds
		CallbackTimeoutSeconds int32 `json:"callbackTimeoutSeconds,omitempty"`
	}
)

func NewGenericDbError(msg string, err error) *DbError {
	return &DbError{
		OriginalError: err,
		CustomMessage: msg,
	}
}

func NewDbErrorOnShardConditionFail(msg string, err error, shardInfo *ShardInfo) *DbError {
	return &DbError{
		OriginalError:      err,
		CustomMessage:      msg,
		ShardConditionFail: true,
		ConflictShardInfo:  shardInfo,
	}
}
