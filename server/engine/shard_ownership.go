package engine

import (
	"context"
	"sync"

	"github.com/iworkflowio/durable-timer/databases"
	"github.com/iworkflowio/durable-timer/log"
	"github.com/iworkflowio/durable-timer/log/tag"
)

type shardOwnershipImpl struct {
	logger log.Logger
	store  databases.TimerStore

	shardId       int
	shardVersion  int64
	shardMetadata databases.ShardMetadata

	closeChan     chan struct{}
	closeChanOnce func()
}



func newShardOwnershipImpl(
	ctx context.Context,
	logger log.Logger,
	currentAddress string,
	shardId int,
	store databases.TimerStore,
) (ShadOwnership, error) {

	prevShardInfo, currentShardInfo, err := store.ClaimShardOwnership(ctx, shardId, currentAddress)
	if err != nil {
		return nil, err
	}

	logger.Info("Claimed shard ownership", tag.ShardId(shardId), tag.Current(currentShardInfo), tag.Prev(prevShardInfo))

	closeChan := make(chan struct{})
	// safely close the channel once
	var once sync.Once
	closeChanOnce := func() { once.Do(func() { close(closeChan) }) }

	return &shardOwnershipImpl{
		logger: logger,
		store:  store,

		shardId:       shardId,
		shardVersion:  currentShardInfo.ShardVersion,
		shardMetadata: currentShardInfo.Metadata,
		closeChan:     closeChan,
		closeChanOnce: closeChanOnce,
	}, nil
}

// GetOwnershipLossChannel implements ShadOwnership.
func (s *shardOwnershipImpl) GetOwnershipLossChannel() <-chan struct{} {
	return s.closeChan
}

// GetShardId implements ShadOwnership.
func (s *shardOwnershipImpl) GetShardId() int {
	return s.shardId
}

// GetCurrentShardMetadata implements ShadOwnership.
func (s *shardOwnershipImpl) GetCurrentShardMetadata() databases.ShardMetadata {
	return s.shardMetadata
}

// UpdateShardMetadata implements ShadOwnership.
func (s *shardOwnershipImpl) UpdateShardMetadata(ctx context.Context, metadata *databases.ShardMetadata) error {
	updateErr := s.store.UpdateShardMetadata(ctx, s.shardId, s.shardVersion, *metadata)
	if updateErr != nil {
		if updateErr.ShardConditionFail {
			s.logger.Warn("Shard version mismatch when updating shard metadata", tag.ShardId(s.shardId), tag.Error(updateErr))
			s.ReleaseOwnership()
		} else {
			s.logger.Error("Failed to update shard metadata", tag.ShardId(s.shardId), tag.Error(updateErr))
		}
		return updateErr
	}

	s.shardMetadata = *metadata

	s.logger.Info("Updated shard metadata", tag.ShardId(s.shardId), tag.Value(metadata))
	return nil
}

// ReleaseOwnership implements ShadOwnership.
func (s *shardOwnershipImpl) ReleaseOwnership() {
	s.closeChanOnce()
}
