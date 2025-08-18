package engine

import (
	"context"
	"math/rand"
	"time"

	"github.com/iworkflowio/durable-timer/config"
	"github.com/iworkflowio/durable-timer/databases"
	"github.com/iworkflowio/durable-timer/log"
	"github.com/iworkflowio/durable-timer/log/tag"
)

type batchDeleterImpl struct {
	config                    *config.Config
	logger                    log.Logger
	shard                     ShadOwnership
	offsetNotificationChannel <-chan *TimerOffset
	store                     databases.TimerStore
	receivedOffset            *TimerOffset // the offset that is received from the offset notification channel
	committedOffset           *TimerOffset // the offset that is committed to the database
	closeChan                 chan struct{}
}

func newBatchDeleterImpl(
	config *config.Config, logger log.Logger,
	shard ShadOwnership,
	offsetNotificationChannel <-chan *TimerOffset, // the receive-only channel to receive the committed offset from the timer queue
	store databases.TimerStore,
) (TimerBatchDeleter, error) {
	return &batchDeleterImpl{
		config:                    config,
		logger:                    logger,
		shard:                     shard,
		offsetNotificationChannel: offsetNotificationChannel,
		store:                     store,
	}, nil
}

// Close implements TimerBatchDeleter.
func (b *batchDeleterImpl) Close() error {
	b.logger.Info("Closing batch deleter")
	close(b.closeChan)
	return nil
}

// Start implements TimerBatchDeleter.
func (b *batchDeleterImpl) Start() error {
	go b.run()
	return nil
}

// run is the main loop that handles offset notifications and manages two timers
func (b *batchDeleterImpl) run() {
	deleterConfig := b.config.Engine.TimerBatchDeleterConfig

	for {
		select {
		case <-b.closeChan:
			b.logger.Debug("[BatchDeleter] Shutting down batch deleter")
			return

		case <-b.shard.GetOwnershipLossChannel():
			b.logger.Warn("Shard ownership lost, shutting down batch deleter")
			b.Close()
			return

		case offset := <-b.offsetNotificationChannel:
			// Update the last committed offset when receiving new offset notifications
			if offset != nil {
				b.receivedOffset = offset
				b.logger.Debug("[BatchDeleter] update last received offset", tag.Value(offset))
			}

		case <-time.After(b.getCommitInterval(deleterConfig)):
			// NOTE: As of Go 1.23, the garbage collector can recover unreferenced unstopped timers. There is no reason to prefer NewTimer when After will do.
			// Timer fired for committing offset
			b.commitOffset()
		case <-time.After(b.getDeleteInterval(deleterConfig)):
			// NOTE: As of Go 1.23, the garbage collector can recover unreferenced unstopped timers. There is no reason to prefer NewTimer when After will do.
			// Timer fired for deleting timers
			b.deleteTimers()
		}
	}
}

// commitOffset commits the current lastReceivedOffset to the database shard metadata 
// and update the lastCommittedOffset to the lastReceivedOffset
func (b *batchDeleterImpl) commitOffset() {
	if b.receivedOffset == nil {
		b.logger.Debug("[BatchDeleter] No offset to commit")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), b.config.Engine.DatabaseAPITimeout)
	defer cancel()

	// Update shard metadata with the committed offset
	metadata := databases.ShardMetadata{
		CommittedOffsetTimestamp: b.receivedOffset.Timestamp,
		CommittedOffsetUuid:      b.receivedOffset.Uuid,
	}

	updateErr := b.shard.UpdateShardMetadata(ctx, &metadata)
	if updateErr != nil {
		return
	}

	b.committedOffset = b.receivedOffset
}

// deleteTimers deletes completed timers from the database up to the committed offset
func (b *batchDeleterImpl) deleteTimers() {
	if b.receivedOffset == nil {
		b.logger.Debug("[BatchDeleter] No committed offset available for deletion")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), b.config.Engine.DatabaseAPITimeout)
	defer cancel()

	// Delete timers from the beginning up to the committed offset
	deleteRequest := &databases.RangeDeleteTimersRequest{
		StartTimestamp: databases.ZeroTimestamp,
		StartTimeUuid:  databases.ZeroUUID,
		EndTimestamp:   b.receivedOffset.Timestamp,
		EndTimeUuid:    b.receivedOffset.Uuid,
	}

	deleterConfig := b.config.Engine.TimerBatchDeleterConfig
	limit := deleterConfig.DeletingBatchLimitPerRequest

	_, err := b.store.RangeDeleteWithLimit(ctx, b.shard.GetShardId(), deleteRequest, limit)
	if err != nil {
		b.logger.Error("Failed to delete timers", tag.Error(err))
	}
}

// getCommitInterval returns the commit interval with jitter
func (b *batchDeleterImpl) getCommitInterval(config config.TimerBatchDeleterConfig) time.Duration {
	jitter := time.Duration(rand.Int63n(int64(config.CommittingIntervalJitter)))
	return config.CommittingInterval + jitter
}

// getDeleteInterval returns the delete interval with jitter
func (b *batchDeleterImpl) getDeleteInterval(config config.TimerBatchDeleterConfig) time.Duration {
	jitter := time.Duration(rand.Int63n(int64(config.DeletingIntervalJitter)))
	return config.DeletingInterval + jitter
}
