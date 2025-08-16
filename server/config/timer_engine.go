package config

import "time"

type TimerEngineConfig struct {
	// MinTimerDuration is the minimum duration of the timer
	// The timer duration should be larger than this value
	// This is to avoid the timer duration is too short and we have to insert a lot of timers into the in-memory queue
	// Default is 500 ms
	MinimumTimerDuration time.Duration

	// ShutdownTimeout is the timeout to shutdown the engine
	// Default is 2 seconds
	ShutdownTimeout time.Duration

	// CallbackProcessorConfig is the config for the CallbackProcessor
	// Note that callback processor is a singleton instance for the whole Engine
	CallbackProcessorConfig CallbackProcessorConfig


	// TimerQueueConfig is the config for the TimerQueue
	// Note that timer queue is one instance per shard
	TimerQueueConfig       TimerQueueConfig

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
	// Default is 0.5
	QueueAvailableThresholdToLoad float64
	// MaxPreloadTimeRange is the how far we can preload the timers from the database into the queue.
	// The shorter the range, the less likely that we get new timers inserted into the loaded time window
	// which could cause the queue oversize the MaxQueueSizeToUnload and trigger the unload
	MaxPreloadTimeRange time.Duration
	// MaxLookAheadTimeRange is the max time duration to look ahead 
	// Looking ahead means to get a next timer after the preload time window.
	// It happens when preload time window does not have enough timers to fill the queue.
	// The single next timer will help to trigger the next preload.
	MaxLookAheadTimeRange time.Duration
	// BatchReadLimitPerRequest is the max number of timers to read from the database per request
	BatchReadLimitPerRequest int
}

type TimerQueueConfig struct {
	// ExpectedQueueSize is the expected size of the queue
	// The actual size could be larger than this value because there could be new timers inserted 
	// into the loaded time window, and we have to unload the queue to ensure memory usage is under control
	ExpectedQueueSize int
	// MaxQueueSizeToUnload is the max size of the queue to unload
	MaxQueueSizeToUnload int
}

type CallbackProcessorConfig struct {
	// MaxCallbackTimeoutSeconds is the max timeout for the callback to be executed
	// This is to avoid the callback being executed for too long and block the processor thread
	MaxCallbackTimeoutSeconds int

	// Concurrency is the number of concurrent callback tasks to be executed
	// It is controlling the number of threads in the callback processor
	Concurrency int
}