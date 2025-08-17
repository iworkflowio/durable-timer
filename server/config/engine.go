package config

import "time"

type EngineConfig struct {
	// MinTimerDuration is the minimum duration of the timer
	// The timer duration should be larger than this value
	// This is to avoid the timer duration is too short and we have to insert a lot of timers into the in-memory queue
	// Default is 500 ms
	MinTimerDuration time.Duration

	// MaxTimerPayloadSizeInBytes is the maximum size of the timer payload in bytes
	// The timer payload is the data that is passed to the callback
	// Default is 100 KB (100 * 1024 bytes)
	MaxTimerPayloadSizeInBytes int

	// MaxCallbackTimeoutSeconds is the max timeout for the callback to be executed
	// This is to avoid the callback being executed for too long and block the processor thread
	// Default is 10 seconds
	MaxiCallbackTimeoutSeconds int

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
	// Note that timer queue is one instance per shard
	TimerQueueConfig TimerQueueConfig

	// TimerBatchDeleterConfig is the config for the TimerBatchDeleter
	// Note that timer batch deleter is one instance per shard
	TimerBatchDeleterConfig TimerBatchDeleterConfig

	// TimerBatchReaderConfig is the config for the TimerBatchReader
	// Note that timer batch reader is one instance per shard
	TimerBatchReaderConfig TimerBatchReaderConfig
}

// TimerBatchDeleterConfig is the config for the TimerBatchDeleter
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
type TimerBatchReaderConfig struct {

	// QueueAvailableThresholdToLoad is the threshold to load the timers from the database into the queue
	// Default is 0.5. Meaning that when the queue is <=50% full, it will trigger loading the timers from the database into the queue.
	// Note that the actual loading condition also depends on the MaxPreloadTimeDuration and MaxLookAheadTimeDuration, and whether there are timers can be loaded.
	QueueAvailableThresholdToLoad float64
	// MaxPreloadTimeDuration is the how far we can preload the timers from the database into the queue.
	// The longer the duration, the more timers we can preload for read efficiency, but the more likely that we get new timers inserted into the loaded time window which will cause the queue oversize the MaxQueueSizeToUnload and trigger the unload.
	// Default is 1 minute. Setting it to zero means no preload.
	MaxPreloadTimeDuration *time.Duration
	// MaxLookAheadTimeDuration is the max time duration to look ahead.
	// Looking ahead is the mechanism when preload time window does not have enough timers to fill the queue,
	// it will look ahead to get a next timer after the preload time window.
	// The duration means the max time duration to look ahead.
	// If the next timer is found, the timer's execute_at will trigger the next preload
	// If the next timer is not found, next preload will be triggered after the MaxPreloadTimeDuration.
	// When a new timer is inserted, if the timer is within the MaxLookAheadTimeDuration, it will update the next preload time.
	// Because of shard movements (shard ownership is eventual consistent), it's recommended to not set this value too large. Because there could be edge cases where a timer is inserted to database by another instance (e.g. the current instance is waiting for a year as next preload time, but the shard is owned by another instance and a new timer of next hour is inserted, then ownership is transferred back to the current instance).
	// This is also used to calculate the timer duration to insert without forwarding the request to the owner instance, which is the optimization to insert timers without locking overhead -- when the new timer's execute_at is greater than MaxPreloadTimeDuration + MaxLookAheadTimeDuration, it is safe to insert the timer without forwarding the request to the owner instance.
	// Default is 1 minute
	MaxLookAheadTimeDuration time.Duration
	// BatchReadLimitPerRequest is the max number of timers to read from the database per request
	// Default is 1000
	BatchReadLimitPerRequest int
}

type TimerQueueConfig struct {
	// ExpectedQueueSize is the expected size of the queue
	// The actual size could be larger than this value because there could be new timers inserted
	// into the loaded time window, and we have to unload the queue to ensure memory usage is under control.
	// The unload is controlled by the MaxQueueSizeToUnload.
	// Default is 500, meaning expected memory size is 500 * 100KB = 50MB for a shard.
	ExpectedQueueSize int
	// MaxQueueSizeToUnload is the max size of the queue to unload
	// Unloading is the mechanism to unload the queue to ensure memory usage is under control.
	// Default is 1000, meaning the max memory size is 1000* 100KB = 100MB for a shard.
	MaxQueueSizeToUnload int
}

type CallbackProcessorConfig struct {
	// Concurrency is the number of concurrent callback tasks to be executed
	// It is controlling the number of threads in the callback processor
	// Default is 2000
	Concurrency int
}
