-- Timer Service Cassandra Initialization Script
-- This script creates the keyspace and tables for the distributed timer service

-- Create keyspace for timer service
CREATE KEYSPACE IF NOT EXISTS timer_service 
WITH REPLICATION = {
    'class': 'SimpleStrategy',
    'replication_factor': 1
};

-- Use the timer_service keyspace
USE timer_service;

-- Display successful initialization message
-- Note: CQL doesn't support procedural statements like this, but keeping for documentation
-- SELECT 'Timer Service Cassandra initialization completed successfully' as message; 