package membership

type (
	ShardMembership interface {
		ClaimShardOwnership(shardId int, ownerId string, metadata interface{}) (shardVersion int64, err *databases.DbError)
	}
)