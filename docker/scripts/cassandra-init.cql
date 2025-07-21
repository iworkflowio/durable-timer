-- Timer Service Cassandra Initialization Script
-- This script creates the keyspace and tables for the distributed timer service

-- Create keyspace for timer service
CREATE KEYSPACE IF NOT EXISTS timer_service 
WITH REPLICATION = {
    'class': 'SimpleStrategy',
    'replication_factor': 1
};
