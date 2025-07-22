package cassandra

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gocql/gocql"
	"github.com/iworkflowio/durable-timer/config"
	"github.com/stretchr/testify/require"
)

const (
	testKeyspace = "timer_test"
	testHost     = "localhost:9042"
)

// getTestHost returns the Cassandra test host, checking environment variable first
func getTestHost() string {
	if host := os.Getenv("CASSANDRA_TEST_HOST"); host != "" {
		return host
	}
	return testHost
}

// getSchemaFilePath returns the path to the v1.cql schema file
func getSchemaFilePath() string {
	// Get current file directory
	_, currentFile, _, _ := runtime.Caller(0)
	currentDir := filepath.Dir(currentFile)

	// Path to schema file relative to current file
	schemaPath := filepath.Join(currentDir, "schema", "v1.cql")
	return schemaPath
}

// executeSchemaFile reads and executes CQL statements from the schema file
func executeSchemaFile(session *gocql.Session) error {
	schemaPath := getSchemaFilePath()

	file, err := os.Open(schemaPath)
	if err != nil {
		return fmt.Errorf("failed to open schema file %s: %w", schemaPath, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var statement strings.Builder

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}

		statement.WriteString(line)
		statement.WriteString(" ")

		// Execute statement when we hit a semicolon
		if strings.HasSuffix(line, ";") {
			cqlStatement := strings.TrimSpace(statement.String())
			if cqlStatement != "" {
				err := session.Query(cqlStatement).Exec()
				if err != nil {
					return fmt.Errorf("failed to execute CQL statement '%s': %w", cqlStatement, err)
				}
			}
			statement.Reset()
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading schema file: %w", err)
	}

	return nil
}

// setupTestStore creates a test store with a clean test keyspace
func setupTestStore(t *testing.T) (*CassandraTimerStore, func()) {
	// Try to connect to Cassandra
	cluster := gocql.NewCluster(getTestHost())
	cluster.Timeout = 5 * time.Second
	cluster.ConnectTimeout = 5 * time.Second

	session, err := cluster.CreateSession()
	if err != nil {
		t.Skip("Cassandra not available, skipping integration tests:", err)
		return nil, nil
	}
	session.Close()

	// Create test keyspace and tables
	err = createTestKeyspace()
	if err != nil {
		t.Fatal("Failed to create test keyspace:", err)
	}

	// Create store with test configuration
	config := &config.CassandraConnectConfig{
		Hosts:       []string{getTestHost()},
		Keyspace:    testKeyspace,
		Consistency: gocql.Quorum,
		Timeout:     10 * time.Second,
	}

	store, err := NewCassandraTimerStore(config)
	require.NoError(t, err)
	cassandraStore := store.(*CassandraTimerStore)

	// Cleanup function
	cleanup := func() {
		cassandraStore.Close()
		dropTestKeyspace()
	}

	return cassandraStore, cleanup
}

func createTestKeyspace() error {
	cluster := gocql.NewCluster(getTestHost())
	cluster.Timeout = 5 * time.Second
	session, err := cluster.CreateSession()
	if err != nil {
		return err
	}
	defer session.Close()

	// Drop keyspace if exists
	err = session.Query(fmt.Sprintf("DROP KEYSPACE IF EXISTS %s", testKeyspace)).Exec()
	if err != nil {
		return err
	}

	// Create keyspace
	createKeyspaceQuery := fmt.Sprintf(`
		CREATE KEYSPACE %s 
		WITH REPLICATION = {
			'class': 'SimpleStrategy',
			'replication_factor': 1
		}`, testKeyspace)

	err = session.Query(createKeyspaceQuery).Exec()
	if err != nil {
		return err
	}

	// Switch to test keyspace
	cluster.Keyspace = testKeyspace
	testSession, err := cluster.CreateSession()
	if err != nil {
		return err
	}
	defer testSession.Close()

	// Execute schema from v1.cql file
	err = executeSchemaFile(testSession)
	if err != nil {
		return fmt.Errorf("failed to execute schema file: %w", err)
	}

	return nil
}

func dropTestKeyspace() {
	cluster := gocql.NewCluster(getTestHost())
	cluster.Timeout = 5 * time.Second
	session, err := cluster.CreateSession()
	if err != nil {
		return
	}
	defer session.Close()

	session.Query(fmt.Sprintf("DROP KEYSPACE IF EXISTS %s", testKeyspace)).Exec()
}
