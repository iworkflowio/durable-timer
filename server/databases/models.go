package databases

import (
	genapi "github.com/iworkflowio/durable-timer/genapi/go"
	"time"
)

type (
	RangeGetTimersRequest struct {
		GroupId       int
		UpToTimestamp time.Time
		Limit         int
	}

	RangeGetTimersResponse struct {
		Timers []*genapi.Timer
	}

	RangeDeleteTimersRequest struct {
		GroupId int
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
)
