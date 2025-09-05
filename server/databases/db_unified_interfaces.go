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
		) (prevShardInfo, currentShardInfo *ShardInfo, err *DbError)

		UpdateShardMetadata(
			ctx context.Context,
			shardId int, shardVersion int64,
			metadata ShardMetadata,
		) (err *DbError)

		GetTimer(
			ctx context.Context,
			shardId int, namespace string, timerId string,
		) (timer *DbTimer, err *DbError)

		CreateTimerNoLock(
			ctx context.Context,
			shardId int, namespace string,
			timer *DbTimer,
		) (err *DbError)

		UpdateTimerNoLock(
			ctx context.Context,
			shardId int, namespace string,
			request *UpdateDbTimerRequest,
		) (err *DbError)

		DeleteTimerNoLock(
			ctx context.Context,
			shardId int, namespace string, timerId string,
		) *DbError

		RangeGetTimers(
			ctx context.Context,
			shardId int,
			request *RangeGetTimersRequest,
		) (*RangeGetTimersResponse, *DbError)

		// RangeDeleteWithBatchInsertTxn is a transaction that deletes timers in a range and inserts new timers
		// TODO: remove this
		RangeDeleteWithBatchInsertTxn(
			ctx context.Context,
			shardId int, shardVersion int64,
			request *RangeDeleteTimersRequest,
			TimersToInsert []*DbTimer,
		) (*RangeDeleteTimersResponse, *DbError)

		// RangeDeleteWithLimit is a non-transactional operation that deletes timers in a range
		RangeDeleteWithLimit(
			ctx context.Context,
			shardId int,
			request *RangeDeleteTimersRequest,
			limit int, // Note that some distributed databases like Cassandra/MongoDB/DynamoDB don't support multiple range queries with LIMIT, so it may be ignored
		) (*RangeDeleteTimersResponse, *DbError)

		// TODO: only used in speical case (time skew)
		UpdateTimer(
			ctx context.Context,
			shardId int, shardVersion int64, namespace string,
			request *UpdateDbTimerRequest,
		) (err *DbError)

		// TODO: only used in speical case (time skew)
		DeleteTimer(
			ctx context.Context,
			shardId int, shardVersion int64, namespace string, timerId string,
		) *DbError

		// TODO: only used in speical case (time skew)
		CreateTimer(
			ctx context.Context,
			shardId int, shardVersion int64, namespace string,
			timer *DbTimer,
		) (err *DbError)
	}
)
