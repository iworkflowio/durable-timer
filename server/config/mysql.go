package config

import "time"

// MySQLConnectConfig holds configuration for MySQL connection
type MySQLConnectConfig struct {
	Host            string
	Database        string
	Username        string
	Password        string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}
