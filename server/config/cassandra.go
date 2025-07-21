package config

import (
	"time"

	"github.com/gocql/gocql"
)

// CassandraConnectConfig holds configuration for Cassandra connection
type CassandraConnectConfig struct {
	Hosts       []string
	Keyspace    string
	Consistency gocql.Consistency
	Timeout     time.Duration
}