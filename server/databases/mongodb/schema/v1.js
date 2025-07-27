// MongoDB Schema for Timer Service - Unified Table Design
// JavaScript commands for MongoDB shell (mongosh)

// Create timers collection with document structure
// The collection stores both timer records (row_type=2) and shard records (row_type=1)
db.createCollection("timers");

// Create indexes for efficient queries
// Compound index optimized for timer execution queries (most frequent)
// Also serves as unique constraint for shard records (row_type=1 with zero values)
db.timers.createIndex({
    "shard_id": 1, 
    "row_type": 1, 
    "timer_execute_at": 1, 
    "timer_uuid_high": 1,
    "timer_uuid_low": 1
}, {
    "unique": true
});

// Index for timer CRUD operations
db.timers.createIndex({
    "shard_id": 1, 
    "row_type": 1, 
    "timer_namespace": 1,
    "timer_id": 1
}, {
    "unique": true
}); 