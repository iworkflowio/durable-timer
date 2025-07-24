package postgresql

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/iworkflowio/durable-timer/config"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

const (
	testHost     = "localhost"
	testPort     = 5432
	testDatabase = "timer_service_test"
	testUsername = "postgres"
	testPassword = "postgres_root_password"
	testSSLMode  = "disable"
)

func getTestHost() string {
	if host := os.Getenv("POSTGRESQL_TEST_HOST"); host != "" {
		return host
	}
	return testHost
}

func getSchemaFilePath() string {
	_, filename, _, _ := runtime.Caller(0)
	dir := filepath.Dir(filename)
	return filepath.Join(dir, "schema", "v1.sql")
}

// executeSchemaFile reads and executes SQL statements from the schema file
func executeSchemaFile(db *sql.DB) error {
	contentBytes, err := os.ReadFile(getSchemaFilePath())
	if err != nil {
		log.Fatalf("Error reading file: %v at %v", err, getSchemaFilePath())
	}

	content := string(contentBytes)

	// Split by semicolon to get individual statements
	statements := strings.Split(content, ";")

	for _, stmt := range statements {
		// Trim whitespace and skip empty statements
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}

		// Process the statement: remove comments but keep SQL content
		lines := strings.Split(stmt, "\n")
		var cleanLines []string
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "--") {
				cleanLines = append(cleanLines, line)
			}
		}

		if len(cleanLines) == 0 {
			continue
		}

		cleanStmt := strings.Join(cleanLines, " ")

		_, err = db.Exec(cleanStmt)
		if err != nil {
			return fmt.Errorf("failed to execute SQL statement '%s': %w", cleanStmt, err)
		}
	}

	return nil
}

// setupTestStore creates a test store with a clean test database
func setupTestStore(t *testing.T) (*PostgreSQLTimerStore, func()) {
	// Create test database and tables
	err := createTestDatabase()
	if err != nil {
		t.Fatal("Failed to create test database:", err)
	}

	// Create store with test configuration
	config := &config.PostgreSQLConnectConfig{
		Host:            getTestHost(),
		Port:            testPort,
		Database:        testDatabase,
		Username:        testUsername,
		Password:        testPassword,
		SSLMode:         testSSLMode,
		MaxOpenConns:    10,
		MaxIdleConns:    5,
		ConnMaxLifetime: 5 * time.Minute,
	}

	store, err := NewPostgreSQLTimerStore(config)
	require.NoError(t, err)
	postgresqlStore := store.(*PostgreSQLTimerStore)

	// Cleanup function
	cleanup := func() {
		postgresqlStore.Close()
		dropTestDatabase()
	}

	return postgresqlStore, cleanup
}

func createTestDatabase() error {
	// Connect without specifying database
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s sslmode=%s",
		getTestHost(), testPort, testUsername, testPassword, testSSLMode)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	// Drop database if exists
	_, err = db.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %s", testDatabase))
	if err != nil {
		return err
	}

	// Create database
	_, err = db.Exec(fmt.Sprintf("CREATE DATABASE %s", testDatabase))
	if err != nil {
		return err
	}

	// Connect to test database
	testDSN := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		getTestHost(), testPort, testUsername, testPassword, testDatabase, testSSLMode)

	testDB, err := sql.Open("postgres", testDSN)
	if err != nil {
		return err
	}
	defer testDB.Close()

	// Execute schema from v1.sql file
	err = executeSchemaFile(testDB)
	if err != nil {
		return fmt.Errorf("failed to execute schema file: %w", err)
	}

	return nil
}

func dropTestDatabase() error {
	// Connect without specifying database
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s sslmode=%s",
		getTestHost(), testPort, testUsername, testPassword, testSSLMode)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	// Drop test database
	_, err = db.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %s", testDatabase))
	return err
}
