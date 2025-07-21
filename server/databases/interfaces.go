package databases

import (
	"context"
	genapi "github.com/iworkflowio/durable-timer/genapi/go"
)

// TimerStore is the unified interface for all timer databases
type (
	TimerStore interface {
		Close() error

		CreateTimer(
			ctx context.Context,
			timer *genapi.Timer,
		) (alreadyExists bool, err error)

		GetTimersUpToTimestamp(
			ctx context.Context,
			request *RangeGetTimersRequest,
		) (*RangeGetTimersResponse, error)

		DeleteTimersUpToTimestamp(
			ctx context.Context,
			request *RangeDeleteTimersRequest,
		) (*RangeDeleteTimersResponse, error)

		UpdateTimer(
			ctx context.Context,
			groupId int, timerId string,
			request *genapi.UpdateTimerRequest,
		) (notExists bool, err error)

		GetTimer(
			ctx context.Context,
			groupId int, timerId string,
		) (timer *genapi.Timer, notExists bool, err error)

		DeleteTimer(
			ctx context.Context,
			groupId int, timerId string,
		) error
	}
)
