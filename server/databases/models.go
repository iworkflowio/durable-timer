package databases

import (
	"time"
)

type (

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
		Payload map[string]interface{} `json:"payload,omitempty"`

		RetryPolicy map[string]interface{} `json:"retryPolicy,omitempty"`

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

	RangeDeleteTimersRequest struct {
		// Delete timers from this timestamp
		// This is necessary because of racing conditions.
		// There could be another instance of the service "owning" the shard and performing the execution/deletion.
		// So we need to delete exactly the timers that are owned by this instance.
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
		Payload map[string]interface{} `json:"payload,omitempty"`

		RetryPolicy map[string]interface{} `json:"retryPolicy,omitempty"`

		// New timeout for the HTTP callback in seconds
		CallbackTimeoutSeconds int32 `json:"callbackTimeoutSeconds,omitempty"`
	}
)
