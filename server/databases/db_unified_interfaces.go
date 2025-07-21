package databases

import (
	"context"
)

// TimerStore is the unified interface for all timer databases
// Note that this layer is not aware of the sharding mechanism.
// The shardId is already calculated and passed in.
type (
	TimerStore interface {
		Close() error

		ClaimShardOwnership(
			ctx context.Context,
			shardId int,
			ownerId string,
			metadata interface{},
		) (shardVersion int64, err *DbError)

		CreateTimer(
			ctx context.Context,
			shardId int, shardVersion int64, timer *DbTimer,
		) (err *DbError)

		GetTimersUpToTimestamp(
			ctx context.Context,
			shardId int, request *RangeGetTimersRequest,
		) (*RangeGetTimersResponse, *DbError)

		DeleteTimersUpToTimestamp(
			ctx context.Context,
			shardId int, shardVersion int64, request *RangeDeleteTimersRequest,
		) (*RangeDeleteTimersResponse, *DbError)

		UpdateTimer(
			ctx context.Context,
			shardId int, shardVersion int64, timerId string,
			request *UpdateDbTimerRequest,
		) (err *DbError)

		GetTimer(
			ctx context.Context,
			shardId int, timerId string,
		) (timer *DbTimer, err *DbError)

		DeleteTimer(
			ctx context.Context,
			shardId int, shardVersion int64, timerId string,
		) *DbError
	}
)
