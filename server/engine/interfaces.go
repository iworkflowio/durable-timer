package engine

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/iworkflowio/durable-timer/config"
	"github.com/iworkflowio/durable-timer/databases"
	"github.com/iworkflowio/durable-timer/log"
)

type TimerEngine interface {
	Start() error
	Close() error
	AddShard(shardId int) error
	RemoveShard(shardId int) error
}

func NewTimerEngine(config *config.Config, store databases.TimerStore, logger log.Logger) (TimerEngine, error) {
	// TODO: implement
	return nil, nil
}

// TimerQueue is a singleton instance for the whole Engine
//  1. Responsible for storing the timers into memory to be processed
//  2. Listen to the loadingBufferChannel to load the timers into the queue
//  3. Use a double linked list to maintain the timers in order. One list per shard.
//  4. Pass the timers to the CallbackProcessor to be processed
//  5. Listen from a channel from CallbackProcessor to know when a timer is completed. When a timer is completed, remove it from the list.
//     5.1 If the timer is the first timer in the list, send notification signals to TimerBatchDeleter
type TimerQueue interface {
	Start() error
	// Close the timer queue
	Close() error
}

func NewTimerQueue(
	config *config.Config, logger log.Logger,
	loadingBufferChannel <-chan *databases.DbTimer, // the receive-only channel to pass the timers to be loaded into the queue
	committedOffsetNotificationChannel *OffsetChannelPerShard, // the send-only channels to notify the committed offset changes
	processingChannel chan<- *databases.DbTimer, // the send-only channel to send the fired timer to the callback processor
	processingCompletedChannel <-chan *databases.DbTimer, // the receive-only channel to receive the completed timer from the callback processor
) (TimerQueue, error) {
	// TODO: implement
	return nil, nil
}

type TimerOffset struct {
	Timestamp time.Time
	Uuid      uuid.UUID
}

type OffsetChannelPerShard struct {
	channels map[int]chan *TimerOffset
	sync.Mutex
}

type ShadOwnership interface {
	GetShardId() int
	// GetOwnershipLossChannel returns a channel that will be closed when the shard ownership is lost
	GetOwnershipLossChannel() <-chan struct{}
	// UpdateShardMetadata updates the shard metadata
	// The ownership is lost, this will return an error
	UpdateShardMetadata(ctx context.Context, metadata *databases.ShardMetadata) error
	// ReleaseOwnership releases the shard ownership
	ReleaseOwnership()
}

func NewShardOwnership(
	ctx context.Context, logger log.Logger, currentAddress string, shardId int, store databases.TimerStore,
) (ShadOwnership, error) {
	return newShardOwnershipImpl(ctx, logger, currentAddress, shardId, store)
}

// CallbackProcessor should be a singleton instance for the whole Engine
// 1. Responsible for processing the callback of the timers
// 2. Listen to processingChannel to get the timers to process
// 3. Concurrently process timers using a thread pool
// 4. Send the completed timer to the processingCompletedChannel
type CallbackProcessor interface {
	Start() error
	Close() error
}

func NewCallbackProcessor(
	config *config.Config, logger log.Logger,
	processingChannel <-chan *databases.DbTimer, // the receive-only channel to receive the fired timer from the timer queue to be processed
	processingCompletedChannel chan<- *databases.DbTimer, // the send-only channel to send the completed timer to the timer queue
) (CallbackProcessor, error) {
	// TODO: implement
	return nil, nil
}

// TimerBatchReader should be one instance per shard
// 1. Responsible for reading timers from database
// 2. Pass the timers into TimerQueue for processing
type TimerBatchReader interface {
	Start() error
	Close() error
}

func NewTimerBatchReader(
	config *config.Config, logger log.Logger,
	shard ShadOwnership,
	loadingBufferChannel chan<- *databases.DbTimer, // send-only channel to pass the timers to the timer queue
	store databases.TimerStore,
) (TimerBatchReader, error) {
	// TODO: implement
	return nil, nil
}

// TimerBatchDeleter should be one instance per shard
// 1. Responsible for deleting timers from database, and updating the delete offset timestamp and uuid
// 2. Listen to a offset channel to know when to delete timers from database (with some delay to control the rate of deleting)
type TimerBatchDeleter interface {
	Start() error
	Close() error
}

func NewTimerBatchDeleter(
	config *config.Config, logger log.Logger,
	shard ShadOwnership,
	offsetNotificationChannel <-chan *TimerOffset, // the receive-only channel to receive the committed offset from the timer queue
	store databases.TimerStore,
) (TimerBatchDeleter, error) {
	return newBatchDeleterImpl(config, logger, shard, offsetNotificationChannel, store)
}
