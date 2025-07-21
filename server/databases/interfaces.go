package databases

import (
	"context"
	genapi "github.com/iworkflowio/durable-timer/genapi/go"
)

// TimerStore is the unified interface for all timer databases
// Note that this layer is not aware of the sharding mechanism.
// The shardId is already calculated and passed in.
type (
	TimerStore interface {
		Close() error

		CreateTimer(
			ctx context.Context,
			shardId int, timer *genapi.Timer,
		) (err error)

		GetTimersUpToTimestamp(
			ctx context.Context,
			shardId int, request *RangeGetTimersRequest,
		) (*RangeGetTimersResponse, error)

		DeleteTimersUpToTimestamp(
			ctx context.Context,
			shardId int, request *RangeDeleteTimersRequest,
		) (*RangeDeleteTimersResponse, error)

		UpdateTimer(
			ctx context.Context,
			shardId int, timerId string,
			request *genapi.UpdateTimerRequest,
		) (notExists bool, err error)

		GetTimer(
			ctx context.Context,
			shardId int, timerId string,
		) (timer *genapi.Timer, notExists bool, err error)

		DeleteTimer(
			ctx context.Context,
			shardId int, timerId string,
		) error
	}
)
