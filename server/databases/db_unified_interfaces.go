package databases

import (
	"context"
)

// TimerStore is the unified interface for all timer databases
// Note that this layer is not aware of the sharding mechanism.
// The shardId is already calculated and passed in.
// When shardVersion is required, it is implemented by optimistic locking
type (
	TimerStore interface {
		Close() error

		ClaimShardOwnership(
			ctx context.Context,
			shardId int,
			ownerAddr string,
			metadata interface{},
		) (shardVersion int64, err *DbError)

		CreateTimer(
			ctx context.Context,
			shardId int, shardVersion int64, namespace string,
			timer *DbTimer,
		) (err *DbError)

		CreateTimerNoLock(
			ctx context.Context,
			shardId int, namespace string,
			timer *DbTimer,
		) (err *DbError)

		GetTimersUpToTimestamp(
			ctx context.Context,
			shardId int,
			request *RangeGetTimersRequest,
		) (*RangeGetTimersResponse, *DbError)

		DeleteTimersUpToTimestampWithBatchInsert(
			ctx context.Context,
			shardId int, shardVersion int64,
			request *RangeDeleteTimersRequest,
			TimersToInsert []*DbTimer,
		) (*RangeDeleteTimersResponse, *DbError)

		BatchInsertTimers(
			ctx context.Context,
			shardId int, shardVersion int64,
			TimersToInsert []*DbTimer,
		) *DbError

		UpdateTimer(
			ctx context.Context,
			shardId int, shardVersion int64, namespace string,
			request *UpdateDbTimerRequest,
		) (err *DbError)

		GetTimer(
			ctx context.Context,
			shardId int, namespace string, timerId string,
		) (timer *DbTimer, err *DbError)

		DeleteTimer(
			ctx context.Context,
			shardId int, shardVersion int64, namespace string, timerId string,
		) *DbError
	}
)
