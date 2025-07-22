package config

import "time"

// MongoDBConnectConfig holds configuration for MongoDB connection
type MongoDBConnectConfig struct {
	Host            string
	Port            int
	Database        string
	Username        string
	Password        string
	AuthDatabase    string
	MaxPoolSize     uint64
	MinPoolSize     uint64
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}
