package engine

type TimerEngine interface {
	
}

type TimerEngineForShard interface {
	
}

type TimerQueue interface {
	// 0. This should be one instance per shard
	// 1. Responsible for storing the timers into memory to be processed
	// 2. Sort the timers by execute_at in a priority queue(the reads from DB is sorted by there may be new timers inserted anytime)
	// 3. Wait for the next timer to be ready to be processed, and pass it to CallbackProcessor
	// 4. Use a list to maintain the timers passed to the CallbackProcessor (once they pop from the prioty queue) 
	// 5. Have a background thread to check the timers that are completed and remove them from the list, and send signal to TimerBatchDeleter to delete the timers from database
}

// TimerCallbackTaskCompletion is the function to be executed when the callback is completed
type TimerCallbackTaskCompletion func()

type CallbackProcessor interface {
	// 0. This should be a singleton instance for the whole Engine
	// 1. Responsible for processing the callback of the timers
	// 2. Listen to TimerQueue to get the timers to process
	// 3. Concurrently process timers using a thread pool
	// 4. Execute the TimerCallbackTaskCompletion when the callback is completed
}

type TimerBatchReader interface {
	// 0. This should be one instance per shard
	// 1. Responsible for reading timers from database
	// 2. Pass the timers into TimerQueue for processing
	// 3. Listen to TimerQueue to know when to read next batch of timers (e.g. 50% of the queue is empty)

}

type TimerBatchDeleter interface {
	// 0. This should be one instance per shard
	// 1. Responsible for deleting timers from database, and updating the delete offset timestamp and uuid
	// 2. Listen to TimerQueue to know when to delete timers from database (with some delay to control the rate of deleting)
}