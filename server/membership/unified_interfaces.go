package membership

import (
	"github.com/iworkflowio/durable-timer/databases"
)

type (
	// ShardMembershipManager is the interface for managing shard ownership 
	// Each instance of the service will have its own ShardMembershipManager instance
	// The manager is responsible for managing the shard ownership:
	// 1. On startup, find the current peers of the cluster if exists, or start from zero
	// 2. Join the cluster if exists 
	// 3. Find the shards to own
	// 4. Refresh the shard ownership periodically  
	// 4. Claim ownership of the shards
	// 5. Release ownership of the shards
	// 6. Handle shutdown of the instance to gracefully release the shards
	ShardMembershipManager interface {
		ClaimShardOwnership(shardId int, ownerAddr string, metadata interface{}) (shardVersion int64, err *databases.DbError)
	}
)