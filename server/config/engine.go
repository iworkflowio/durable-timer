package config

import "time"

type EngineConfig struct {
	// MinTimerDuration is the minimum duration of the timer
	// The timer duration should be larger than this value, if less than this, it will be rounded up to this value.
	// This is to avoid the timer duration is too short and we could run into issues with time skew. 
	// Because shard owner instance will load timers that has fired by NOW, and then when completed, range delete the timers up to the committed offset. If another instance isinserting a timer that is firing Now, it could be deleted by the range delete when the other instance has a time skew with the shard owner instance.
	// Default is 1 second.
	MinTimerDuration time.Duration

	// MaxTimerPayloadSizeInBytes is the maximum size of the timer payload in bytes
	// The timer payload is the data that is passed to the callback
	// Default is 100 KB (100 * 1024 bytes)
	MaxTimerPayloadSizeInBytes int

	// MaxCallbackTimeoutSeconds is the max timeout for the callback to be executed
	// This is to avoid the callback being executed for too long and block the processor thread
	// Default is 10 seconds
	MaxCallbackTimeoutSeconds int

	// EngineShutdownTimeout is the timeout to shutdown the engine
	// Default is 10 seconds
	EngineShutdownTimeout time.Duration

	// ShardEngineShutdownTimeout is the timeout to shutdown the shard engine
	// Default is 2 seconds
	ShardEngineShutdownTimeout time.Duration

	// CallbackProcessorConfig is the config for the CallbackProcessor
	// Note that callback processor is a singleton instance for the whole Engine
	CallbackProcessorConfig CallbackProcessorConfig

	// TimerQueueConfig is the config for the TimerQueue
	// Note that timer queue is a singleton instance for the whole Engine
	TimerQueueConfig TimerQueueConfig

	// TimerBatchDeleterConfig is the config for the TimerBatchDeleter
	// Note that timer batch deleter is one instance per shard
	TimerBatchDeleterConfig TimerBatchDeleterConfig

	// TimerBatchReaderConfig is the config for the TimerBatchReader
	// Note that timer batch reader is one instance per shard
	TimerBatchReaderConfig TimerBatchReaderConfig

	// DatabaseAPITimeout is the timeout for the database API calls
	// Default is 10 seconds
	DatabaseAPITimeout time.Duration
}

// TimerBatchDeleterConfig is the config for the TimerBatchDeleter
// Note that timer batch deleter is one instance per shard
type TimerBatchDeleterConfig struct {
	// DeletingInterval is the interval to delete the completed timers from the database(the timers before the committed offset)
	// Default is 30 seconds
	DeletingInterval time.Duration
	// DeletingIntervalJitter is the jitter for the deleting interval
	// Default is 5 seconds
	DeletingIntervalJitter time.Duration
	// CommittingInterval is the interval to commit the timer completedoffset to the database
	// Default is 10 seconds
	CommittingInterval time.Duration
	// CommittingIntervalJitter is the jitter for the committing interval
	// Default is 5 second
	CommittingIntervalJitter time.Duration
	// DeletingBatchLimitPerRequest is the max number of timers to delete from the database per request
	// Default is 1000
	DeletingBatchLimitPerRequest int
}

// TimerBatchReaderConfig is the config for the TimerBatchReader
// Note that timer batch reader is one instance per shard
type TimerBatchReaderConfig struct {
	// MinLookAheadTimeDuration is the min time duration to look ahead.
	// When reading timers from the database, it will read up to NOW()+MinLookAheadTimeDuration.
	// But then filter the timers' execute_at to be less than or equal to NOW(). The first one that is greater than NOW() will be the next wake-up time.
	// Default is 1 second.
	MinLookAheadTimeDuration time.Duration
	// MaxLookAheadTimeDuration is the max time duration to look ahead.
	// When using NOW() + MinLookAheadTimeDuration doesn't find any timer that is greater than NOW(), it will read ONE timer that is greater than NOW() + MaxLookAheadTimeDuration to find the next wake-up time.
	// Because of shard movements (shard ownership is eventual consistent), it's recommended to not set this value too large. Because there could be edge cases where a timer is inserted to database by another instance. For example, the shard owner instance is waiting for a year as next wake-up time, but another instance claims the ownership and inserts a new timer of next hour, then release the ownership. If the MaxLookAheadTimeDuration is too large, the shard owner instance will not be able to load the timer of next hour.
	// Default is 5 minutes
	MaxLookAheadTimeDuration time.Duration
	// BatchReadLimitPerRequest is the max number of timers to read from the database per request
	// Default is 1000
	BatchReadLimitPerRequest int
}

// TimerQueueConfig is the config for the TimerQueue
// Note that timer queue is a singleton instance for the whole Engine
type TimerQueueConfig struct {
	// QueueSize is the size of the queue
	// The queue is used to store the fired timers to be processed by the callback processor
	// The memory consumption of the queue is QueueSize * MaxTimerPayloadSizeInBytes
	// Default is 3000, meaning the default memory consumption is 3000 * 100KB = 300MB for this timer queue
	QueueSize int
	// LoadingBufferChannelSize is the size of the loading buffer channel.
	// The channel is used to deliver the timers to the TimerQueue from the batch readers
	// This is to avoid the batch readers to be blocked by the timer queue.
	// Default is 10. 
	LoadingBufferChannelSize *int
}

// CallbackProcessorConfig is the config for the CallbackProcessor
// Note that CallbackProcessor is a singleton instance for the whole Engine
type CallbackProcessorConfig struct {
	// Concurrency is the number of concurrent callback tasks to be executed
	// It is controlling the number of threads in the callback processor
	// Default is 1000 
	Concurrency int
}
