package config

import "time"

// PostgreSQLConnectConfig holds configuration for PostgreSQL connection
type PostgreSQLConnectConfig struct {
	Host            string
	Port            int
	Database        string
	Username        string
	Password        string
	SSLMode         string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}
