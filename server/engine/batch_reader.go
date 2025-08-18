package engine

import (
	"context"
	"time"

	"github.com/iworkflowio/durable-timer/config"
	"github.com/iworkflowio/durable-timer/databases"
	"github.com/iworkflowio/durable-timer/engine/backoff"
	"github.com/iworkflowio/durable-timer/log"
	"github.com/iworkflowio/durable-timer/log/tag"
)

type batchReaderImpl struct {
	config               *config.Config
	logger               log.Logger
	shard                ShadOwnership
	loadingBufferChannel chan<- *databases.DbTimer
	newTimerChannel      <-chan time.Time // the receive-only channel to receive the newly inserted timer that could be the next wake-up time
	store                databases.TimerStore
	closeChan            chan struct{}
	lastReadOffset       *TimerOffset
	nextWakeupTime       TimerGate
}

func newBatchReaderImpl(
	config *config.Config, logger log.Logger,
	shard ShadOwnership,
	loadingBufferChannel chan<- *databases.DbTimer, // send-only channel to pass the timers to the timer queue
	newTimerChannel <-chan time.Time, // the receive-only channel to receive the newly inserted timer that could be the next wake-up time
	store databases.TimerStore,
) (TimerBatchReader, error) {
	// read from last committed offset
	lastReadOffset := &TimerOffset{
		Timestamp: shard.GetCurrentShardMetadata().CommittedOffsetTimestamp,
		Uuid:      shard.GetCurrentShardMetadata().CommittedOffsetUuid,
	}

	return &batchReaderImpl{
		config:               config,
		logger:               logger,
		shard:                shard,
		loadingBufferChannel: loadingBufferChannel,
		newTimerChannel:      newTimerChannel,
		store:                store,
		closeChan:            make(chan struct{}),
		lastReadOffset:       lastReadOffset,
		nextWakeupTime:       NewLocalTimerGate(logger),
	}, nil
}

func (b *batchReaderImpl) Close() error {
	b.logger.Info("Closing batch reader")
	close(b.closeChan)
	return nil
}

func (b *batchReaderImpl) Start() error {
	go b.run()
	return nil
}

// run is the main loop that reads timers from database and sends them to the loading buffer
func (b *batchReaderImpl) run() {
	b.logger.Info("Starting batch reader")

	b.nextWakeupTime.Update(time.Now().UTC()) // set the next wake-up time to now to initialize the first read
	var readTimers []*databases.DbTimer
	var lookAheadTime *time.Time
	for {

		if(len(readTimers) == 0) {
			select {
			case <-b.closeChan:
				b.logger.Debug("Shutting down batch reader")
				return
	
			case <-b.shard.GetOwnershipLossChannel():
				b.logger.Warn("Shard ownership lost, shutting down batch reader")
				b.Close()
				return
	
			case <-b.nextWakeupTime.FireChan():
				// read timers from database with lookAheadTime
				readTimers, lookAheadTime = b.readTimersAndLookAheadFromDatabase()
				if(lookAheadTime != nil) {
					b.nextWakeupTime.Update(*lookAheadTime)
				}
	
			case newTimerTime := <-b.newTimerChannel:
				b.nextWakeupTime.Update(newTimerTime)
			}
		}else{
			select {
			case <-b.closeChan:
				b.logger.Debug("Shutting down batch reader")
				return
	
			case <-b.shard.GetOwnershipLossChannel():
				b.logger.Warn("Shard ownership lost, shutting down batch reader")
				b.Close()
				return
	
			case b.loadingBufferChannel <- readTimers[0]:
				// send the first timer to the loading buffer until all timers are sent
				readTimers = readTimers[1:]
	
			case newTimerTime := <-b.newTimerChannel:
				b.nextWakeupTime.Update(newTimerTime)
			}
		}
	}
}


// readTimersAndLookAheadFromDatabase reads timers from database with lookAheadTime
// No error returned because if it runs into error, it will retry after 10s interval internally
// either timers are found, or lookAheadTime is set, or both
func (b *batchReaderImpl) readTimersAndLookAheadFromDatabase() ([]*databases.DbTimer, *time.Time) {

	now := time.Now().UTC()
	readerConfig := b.config.Engine.TimerBatchReaderConfig

	// Calculate the end time for reading timers (now + MinLookAheadTimeDuration)
	minLookAheadEnd := now.Add(readerConfig.MinLookAheadTimeDuration)

	// Read timers from last read offset to minLookAheadEnd
	request := &databases.RangeGetTimersRequest{
		StartTimestamp: b.lastReadOffset.Timestamp,
		StartTimeUuid:  b.lastReadOffset.Uuid,
		EndTimestamp:   minLookAheadEnd,
		EndTimeUuid:    databases.MaxUUID,
		Limit:          readerConfig.BatchReadLimitPerRequest,
	}

	var response *databases.RangeGetTimersResponse
	var err error

	op := func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, b.config.Engine.DatabaseAPITimeout)
		defer cancel()
		response, err = b.store.RangeGetTimers(ctx, b.shard.GetShardId(), request)
		if err != nil {
			b.logger.Error("Failed to read timers from database", tag.Error(err))
			return err
		}
		return nil
	}

	policy := backoff.NewExponentialRetryPolicy(100 * time.Millisecond)
	policy.SetMaximumInterval(60 * time.Second)

	throttleRetry := backoff.NewThrottleRetry(
		backoff.WithRetryPolicy(policy),
		backoff.WithRetryableError(func(_ error) bool { return true }), // retry on all errors
	)

	err = throttleRetry.Do(context.Background(), op)

	if err != nil {
		b.logger.Error("Failed to read timers from database after retry", tag.Error(err))
		return nil, nil
	}

	readyTimers := []*databases.DbTimer{}
	var lookAheadTime *time.Time

	// Process the timers
	for _, timer := range response.Timers {
		if timer.ExecuteAt.Before(now) || timer.ExecuteAt.Equal(now) {
			// Timer is ready to be processed
			readyTimers = append(readyTimers, timer)
			// Update last read offset to this timer
			b.lastReadOffset = &TimerOffset{
				Timestamp: timer.ExecuteAt,
				Uuid:      timer.TimerUuid,
			}
		} else {
			// Timer is in the future, this becomes our next wake-up time
			lookAheadTime = &timer.ExecuteAt
			break
		}
	}

	// at this point, we have 4 cases:
	// 1. lookAheadTime is nil, and readyTimers is empty: no timer found within the min look-ahead time
	// 2. lookAheadTime is nil, and readyTimers is not empty: timers are found and all ready to be processed
	// 3. lookAheadTime is not nil, and readyTimers is empty: no timer is ready, the first timer is the next wake-up time
	// 4. lookAheadTime is not nil, and readyTimers is not empty: some timers are found and  ready to be processed, also a timer is the next wake-up time
	// Only case 1 need to read one timer beyond the max look-ahead time to find the next wake-up time

	if lookAheadTime == nil && len(readyTimers) == 0 {
		// case 1: no timer found within the min look-ahead time
		// read one timer beyond the max look-ahead time to find the next wake-up time
		t := b.findNextWakeupTimeWithMaxLookAhead(context.Background(), now, readerConfig)
		lookAheadTime = &t
	}

	return readyTimers, lookAheadTime
}

// findNextWakeupTimeWithMaxLookAhead looks for the next timer beyond MinLookAheadTimeDuration
func (b *batchReaderImpl) findNextWakeupTimeWithMaxLookAhead(ctx context.Context, now time.Time, readerConfig config.TimerBatchReaderConfig) time.Time {
	maxLookAheadEnd := now.Add(readerConfig.MaxLookAheadTimeDuration)

	// Read ONE timer beyond MinLookAheadTimeDuration to find next wake-up time
	request := &databases.RangeGetTimersRequest{
		StartTimestamp: b.lastReadOffset.Timestamp,
		StartTimeUuid:  b.lastReadOffset.Uuid,
		EndTimestamp:   maxLookAheadEnd,
		EndTimeUuid:    databases.MaxUUID,
		Limit:          1, // Only need one timer to determine next wake-up time
	}

	response, err := b.store.RangeGetTimers(ctx, b.shard.GetShardId(), request)
	if err != nil {
		b.logger.Error("Failed to read timers for max look-ahead", tag.Error(err))
		return now.Add(readerConfig.MaxLookAheadTimeDuration) // Default to MaxLookAheadTimeDuration on error
	}

	if len(response.Timers) > 0 {
		nextTimer := response.Timers[0]
		return nextTimer.ExecuteAt
	}

	return now.Add(readerConfig.MaxLookAheadTimeDuration) // default to MaxLookAheadTimeDuration on not found
}
