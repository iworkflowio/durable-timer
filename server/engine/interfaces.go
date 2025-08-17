package engine

import (
	"github.com/iworkflowio/durable-timer/config"
	"github.com/iworkflowio/durable-timer/databases"
	genapi "github.com/iworkflowio/durable-timer/genapi/go"
)

type TimerEngine interface {
	Close() error
	AddShard(shardId int) error
	RemoveShard(shardId int) error

	AddTimer(request *genapi.CreateTimerRequest) error
	DeleteTimer(request *genapi.TimerSelection) error
	GetTimer(request *genapi.TimerSelection) (*genapi.Timer, error)
	UpdateTimer(request *genapi.UpdateTimerRequest) error
}

func NewTimerEngine(config *config.Config, store databases.TimerStore) (TimerEngine, error) {
	// TODO: implement
	return nil, nil
}

type TimerEngineForShard interface {
	Close() error

	AddTimer(request *genapi.CreateTimerRequest) error
	DeleteTimer(request *genapi.TimerSelection) error
	GetTimer(request *genapi.TimerSelection) (*genapi.Timer, error)
	UpdateTimer(request *genapi.UpdateTimerRequest) error
}

func NewTimerEngineForShard(config *config.Config, shardId int, store databases.TimerStore) (TimerEngineForShard, error) {
	// TODO: implement
	return nil, nil
}

type TimerQueue interface {
	// 0. This should be one instance per shard
	// 1. Responsible for storing the timers into memory to be processed
	// 2. Sort the timers by execute_at in a priority queue(the reads from DB is sorted by there may be new timers inserted anytime)
	// 3. Wait for the next timer to be ready to be processed, and pass it to CallbackProcessor
	// 4. Use a list to maintain the timers passed to the CallbackProcessor (once they pop from the prioty queue)
	// 5. Have a background thread to check the timers that are completed and remove them from the list, and send notification signals to TimerBatchDeleter to delete the timers from database
	Close() error
}

func NewTimerQueue(
	config *config.Config, shardId int,
	loadingChannel <-chan *genapi.Timer, // the receive-only channel to pass the timers to be loaded into the queue
	queueSizeNotificationChannel chan<- int, // the send-only channel to notify the queue size changes
	committedOffsetNotificationChannel chan<- int, // the send-only channel to notify the committed offset changes
	firedTimerChannel chan<- *genapi.Timer, // the send-only channel to send the fired timer to the callback processor
	completedTimerChannel <-chan *genapi.Timer, // the receive-only channel to receive the completed timer from the callback processor
) (TimerQueue, error) {
	// TODO: implement
	return nil, nil
}

type CallbackProcessor interface {
	// 0. This should be a singleton instance for the whole Engine
	// 1. Responsible for processing the callback of the timers
	// 2. Listen to TimerQueue to get the timers to process
	// 3. Concurrently process timers using a thread pool
	// 4. Send the completed timer to the completedTimerChannel
	Close() error
}

func NewCallbackProcessor(config *config.Config,
	firedTimerChannel <-chan *genapi.Timer,
	completedTimerChannel chan<- *genapi.Timer,
) (CallbackProcessor, error) {
	// TODO: implement
	return nil, nil
}

type TimerBatchReader interface {
	// 0. This should be one instance per shard
	// 1. Responsible for reading timers from database
	// 2. Pass the timers into TimerQueue for processing
	// 3. Listen to TimerQueue to know when to read next batch of timers (e.g. 50% of the queue is empty)
	Close() error
}

type TimerBatchDeleter interface {
	// 0. This should be one instance per shard
	// 1. Responsible for deleting timers from database, and updating the delete offset timestamp and uuid
	// 2. Listen to TimerQueue to know when to delete timers from database (with some delay to control the rate of deleting)
	Close() error
}
