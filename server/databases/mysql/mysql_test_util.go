package mysql

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

	_ "github.com/go-sql-driver/mysql"
	"github.com/iworkflowio/durable-timer/config"
	"github.com/stretchr/testify/require"
)

const (
	testHost     = "localhost:3306"
	testDatabase = "timer_service_test"
	testUsername = "root"
	testPassword = "timer_root_password"
)

func getTestHost() string {
	if host := os.Getenv("MYSQL_TEST_HOST"); host != "" {
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

		// Skip comment-only lines
		if strings.HasPrefix(stmt, "--") {
			continue
		}

		_, err = db.Exec(stmt)
		if err != nil {
			return fmt.Errorf("failed to execute SQL statement '%s': %w", stmt, err)
		}
	}

	return nil
}

// setupTestStore creates a test store with a clean test database
func setupTestStore(t *testing.T) (*MySQLTimerStore, func()) {
	// Create test database and tables
	err := createTestDatabase()
	if err != nil {
		t.Fatal("Failed to create test database:", err)
	}

	// Create store with test configuration
	config := &config.MySQLConnectConfig{
		Host:            getTestHost(),
		Database:        testDatabase,
		Username:        testUsername,
		Password:        testPassword,
		MaxOpenConns:    10,
		MaxIdleConns:    5,
		ConnMaxLifetime: 5 * time.Minute,
	}

	store, err := NewMySQLTimerStore(config)
	require.NoError(t, err)
	mysqlStore := store.(*MySQLTimerStore)

	// Cleanup function
	cleanup := func() {
		mysqlStore.Close()
		dropTestDatabase()
	}

	return mysqlStore, cleanup
}

func createTestDatabase() error {
	// Connect without specifying database
	dsn := fmt.Sprintf("%s:%s@tcp(%s)/?parseTime=true&loc=UTC",
		testUsername, testPassword, getTestHost())

	db, err := sql.Open("mysql", dsn)
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
	testDSN := fmt.Sprintf("%s:%s@tcp(%s)/%s?parseTime=true&loc=UTC",
		testUsername, testPassword, getTestHost(), testDatabase)

	testDB, err := sql.Open("mysql", testDSN)
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
	dsn := fmt.Sprintf("%s:%s@tcp(%s)/?parseTime=true&loc=UTC",
		testUsername, testPassword, getTestHost())

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	// Drop test database
	_, err = db.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %s", testDatabase))
	return err
}
