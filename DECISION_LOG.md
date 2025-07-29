# Distributed Durable Timer Service - Decision Log

This document tracks all major architectural and design decisions made during the development of the distributed durable timer service.

## Decision Entry Format
Each decision should include:
- **Date**: When the decision was made
- **Context**: Problem being solved or situation requiring a decision
- **Decision**: What was decided
- **Rationale**: Why this decision was made
- **Alternatives**: Other options that were considered
- **Impact**: Expected consequences or implications

**Ordering**: from latest to olderst. 

---

## Decisions

### [Date: 2025-07-29] DynamoDB FilterExpression and Limit Ordering Issue

- **Context**: During implementation of `GetTimersUpToTimestamp` API for DynamoDB, discovered that DynamoDB applies the `Limit` parameter before the `FilterExpression`. When querying the `ExecuteAtWithUuidIndex` LSI with a limit of 3 and filtering for `row_type = 2` (timer records), DynamoDB would scan the first 3 records from the index (which might include 1 shard record with `row_type = 1`), then apply the filter, returning only 2 timer records instead of the expected 3. This behavior is specific to DynamoDB's query execution order and differs from SQL databases where filtering typically happens before limiting.

- **Decision**: Account for potential shard record filtering by setting the DynamoDB query limit to `request.Limit + 1` instead of `request.Limit`. Within each shard, there is exactly one shard record, so in the worst case DynamoDB returns `request.Limit` timer records + 1 shard record. After the `FilterExpression` removes the shard record, we get exactly the requested number of timer records. Additionally, implement client-side limit enforcement to ensure we never return more than `request.Limit` records even if all returned records are timers.

- **Rationale**: This solution is precise and efficient because it accounts for exactly the maximum number of non-timer records that could be filtered out (1 shard record per shard). The `+1` approach is much more efficient than alternatives like doubling the limit or using pagination, while still guaranteeing correct results. The solution leverages our understanding of the data model (exactly one shard record per shard) to provide the minimal overhead needed.

- **Alternatives**: Use much larger limits (e.g., `request.Limit * 2`) to account for filtering, implement DynamoDB pagination to fetch additional results when needed, remove the FilterExpression and implement filtering in application code, use separate queries for shard and timer records

- **Impact**: Enables correct `GetTimersUpToTimestamp` behavior in DynamoDB with minimal performance overhead. The solution is specific to DynamoDB and doesn't affect other database implementations. Provides a template for handling similar FilterExpression/Limit ordering issues in other DynamoDB queries. Documents an important DynamoDB behavior that differs from SQL databases for future reference.

### [Date: 2025-07-23] UUID Splitting for Predictable Pagination Support
- **Context**: Timer pagination requires predictable cursor ordering based on `execute_at + timer_uuid` to determine if new timers fall within loaded page windows. The timer engine needs to inject new timers into the correct page position and know whether a timer with given `(execute_at, timer_uuid)` belongs in the current page window. Storing UUIDs as native UUID types (128-bit values) loses the ability to make predictable comparisons for cursor-based pagination since UUIDs are not easily comparable for ordering operations.
- **Decision**: Split the 128-bit UUID into two 64-bit integers (`timer_uuid_high` and `timer_uuid_low`) for most databases to enable predictable ordering and comparison operations. **Special case for DynamoDB**: Use composite field `timer_execute_at_with_uuid` that combines timestamp and UUID string (e.g., `"2025-01-01T10:00:00.000Z#550e8400-e29b-41d4-a716-446655440000"`) for stable lexicographic cursor ordering. Provide conversion functions to split UUID strings into high/low integers and reconstruct UUIDs from integer pairs.
- **Rationale**: Splitting UUIDs into 64-bit integers enables predictable numeric comparisons required for cursor-based pagination while maintaining full UUID uniqueness. Database engines can efficiently order and compare BIGINT fields for pagination queries. DynamoDB's composite approach leverages native lexicographic ordering in sort keys for optimal pagination performance. This design enables the timer engine to reliably determine page boundaries and inject new timers into correct positions.
- **Alternatives**: Use sequential numeric IDs instead of UUIDs, implement application-level pagination logic without predictable ordering, store UUIDs as strings and accept string comparison overhead, use database-specific UUID ordering functions where available
- **Impact**: Enables efficient cursor-based pagination with predictable ordering across all databases. Allows timer engine to accurately determine page boundaries for new timer injection. Requires schema changes to split UUID fields in all database implementations (except DynamoDB). Adds UUID conversion logic in database access layer. Maintains full UUID functionality while adding pagination predictability.

### [Date: 2025-07-23] Stable TimerUuid for Consistent Upsert Behavior Across Databases
- **Context**: Different database implementations were generating their own UUIDs for timer records, making it impossible to implement consistent "upsert" behavior where creating a timer with the same namespace+timer_id would overwrite existing data. This created poor user experience where duplicate timer creation would either fail with database errors or create multiple records instead of updating the existing timer.
- **Decision**: Add `TimerUuid` field to `DbTimer` model that callers must provide. Callers generate stable UUIDs based on namespace+timer_id (e.g., using MD5 hash). All database implementations use the provided TimerUuid instead of generating their own. MySQL and PostgreSQL use `ON DUPLICATE KEY UPDATE` and `ON CONFLICT DO UPDATE` respectively for true upsert behavior. MongoDB uses `ReplaceOne` with upsert option. DynamoDB naturally overwrites with `PutItem`. Cassandra allows multiple records with same timer_id but different execution times due to clustering key design.
- **Rationale**: Stable UUIDs enable consistent upsert behavior across all databases, providing predictable API behavior where timer creation always succeeds and overwrites existing timers. Caller-provided UUIDs ensure the same timer_id maps to the same UUID across all operations. Database-specific upsert operations leverage native capabilities for optimal performance. Cassandra's limitation is acceptable given its clustering benefits for batch operations.
- **Alternatives**: Continue with database-generated UUIDs and accept duplicate errors, implement application-level duplicate detection, change primary key design to exclude execution time, use database-specific implementations without unified behavior
- **Impact**: Provides consistent upsert behavior across all databases, improving user experience with predictable timer creation API. Requires callers to generate stable UUIDs, adding slight complexity. Cassandra will still allow duplicates with different execution times, which must be documented as a known limitation due to clustering key design trade-offs for performance.

### [Date: 2025-07-23] API Namespace and Request Body Design Revision
- **Context**: Previous API design used groupId with path parameters `/timers/{groupId}/{timerId}` for timer identification. This approach mixed scalability concerns (groupId) with API design, and path parameters created inconsistent endpoint structures across operations.
- **Decision**: Replace groupId with namespace concept and move all identification parameters to request bodies. All operations use POST method with consistent endpoint structure: `/timers/create`, `/timers/get`, `/timers/update`, `/timers/delete`. Timer identification uses namespace+timerId in request bodies, with TimerSelection schema for get/delete operations and integrated fields in CreateTimerRequest/UpdateTimerRequest.
- **Rationale**: Namespace provides clearer semantic meaning for timer ID uniqueness scope while maintaining scalability through shard configuration. Request body parameters provide consistent interface across all operations and eliminate complex URL path handling. All-POST design simplifies client implementation and server routing logic. Each namespace can be configured with specific number of shards for fine-grained scalability control.
- **Alternatives**: Keep groupId concept with path parameters, use mixed HTTP methods (GET/PUT/DELETE), hybrid approach with some path and some body parameters, separate namespacing from scalability concerns
- **Impact**: Cleaner API surface with consistent endpoint structure, simplified client integration with uniform POST requests, clear separation between timer identification (namespace+timerId) and scalability implementation (shard configuration), requires updating all API documentation and implementation code to use namespace concept instead of groupId

### [Date: 2025-07-22] Timer Field Naming Convention and Retry Logic Support
- **Context**: Need consistent field naming in unified table design to distinguish between timer-specific and shard-specific fields. Also need to track retry attempts for timer scheduling logic instead of generic update timestamps.
- **Decision**: Add "timer_" prefix to all timer-specific fields (timer_id, timer_group_id, timer_callback_url, timer_payload, timer_retry_policy, timer_callback_timeout_seconds, timer_execute_at, timer_uuid). Change created_at to timer_created_at and updated_at to timer_attempts (integer) for retry scheduling logic.
- **Rationale**: Consistent naming convention clarifies field ownership in unified table design, reduces implementation confusion when handling row_type-specific fields. timer_attempts provides direct retry count tracking needed for retry scheduling logic instead of timestamp-based updated_at. Fields starting with "timer_" are for timer rows, fields starting with "shard_" are for shard rows (except shard_id which is the partition key).
- **Alternatives**: Keep generic field names with conditional usage, use separate timer_attempt_count field alongside updated_at, database-specific naming conventions
- **Impact**: Clearer code implementation with explicit field ownership, simplified retry logic implementation using integer attempt counter, consistent schema across all databases, requires updating existing implementations to use new field names

### [Date: 2025-07-22] Unified Table Design - Merging Timers and Shards Tables
- **Context**: Previously designed separate `timers` and `shards` tables for timer storage and shard ownership management. T
- **Decision**: Merge the `timers` and `shards` tables into a single unified `timers` table using a `row_type` field as discriminator (1=shard record, 2=timer record). Use conditional fields where timer-specific fields are only populated for timer records (row_type=2) and shard-specific fields only for shard records (row_type=1).
- **Rationale**: Single table design optimizes transaction performance and data locality since shard management and timer operations can happen within the same partition. Atomic operations become simpler with single-table transactions. Related shard and timer data is stored together for better data locality, improving cache efficiency and reducing cross-table joins.
- **Alternatives**: Keep separate tables with foreign key relationships, use separate databases for timers vs shards, accept multi-table transaction complexity, use database-specific solutions
- **Impact**: Better performance for operations involving both shard and timer data. Simplified transaction logic with single-table operations. However, increases schema complexity due to conditional field usage and mixed record types in same table. Requires row_type-aware queries and conditional field handling in application code. Makes schema evolution more complex as changes affect multiple logical entity types.

### [Date: 2025-07-20] Versioned Shard Ownership Management
- **Context**: Distributed timer service instances use eventually consistent membership frameworks (gossip-based), creating race conditions where multiple instances may simultaneously claim the same shard during network partitions or membership changes. Critical race condition: Instance A loads timers for execution window, Instance B claims same shard and inserts new timer into window, Instance A performs range delete without executing Instance B's timer.
- **Decision**: Implement dedicated `shards` table with optimistic concurrency control using version numbers. Ownership protocol: instances claim shards by incrementing version atomically, cache version in memory, verify version before all write operations, gracefully exit if version mismatch detected (ownership lost).
- **Rationale**: Version-based ownership provides atomic ownership claims through database guarantees. Memory caching enables fast conflict detection without per-operation database round trips. Graceful exit ensures clean ownership transfer without data corruption. Significantly reduces race condition window from membership framework delays to single database operation.
- **Alternatives**: Leader election with external coordination service, timestamp-based ownership claims, advisory locking, accept eventual consistency risks
- **Impact**: Adds second table to schema but provides strong ownership guarantees. Prevents timer execution race conditions. Enables safe distributed processing with minimal coordination overhead. Memory version caching provides excellent performance characteristics.

### [Date: 2025-07-20] DynamoDB Special Case Design
- **Context**: DynamoDB has unique constraints that prevent using the universal schema design: it only supports one sort key per table (not multiple columns), doesn't support UUID natively, and physical clustering optimization is unnecessary since it's a managed service without self-hosting options.
- **Decision**: Use a special DynamoDB-specific design with `(shard_id, timer_id)` as primary key and `execute_at` as Local Secondary Index (LSI). This provides direct timer CRUD operations via primary key and timer execution queries via the LSI.
- **Rationale**: DynamoDB's single sort key limitation requires a different approach. Using timer_id as sort key enables direct CRUD operations without complex composite keys. LSI on execute_at provides cost-effective timer execution queries since LSI writes don't incur additional capacity costs. This design maintains consistency with the logical intent while adapting to DynamoDB's constraints.
- **Alternatives**: Use composite range key (executeAt#timerUuid), use Global Secondary Index instead of LSI, separate tables for different access patterns
- **Impact**: DynamoDB implementation differs from other databases but provides optimal cost and performance characteristics for the managed service environment, maintains logical consistency while adapting to platform constraints

### [Date: 2025-07-20] Unified Database Schema Design
- **Context**: Previous database designs varied significantly across different databases, with complex primary key strategies and database-specific optimizations that created implementation complexity and inconsistent performance patterns.
- **Decision**: Implement universal `(shard_id, execute_at, timer_uuid)` primary key design across all databases with `timer_id` as unique index. Use UUID for uniqueness instead of complex constraints, and ensure execute_at is first clustering key for optimal execution performance.
- **Rationale**: Eliminates database-specific complexity while maintaining optimal performance. UUID provides simple uniqueness without complex collision handling. execute_at first optimizes core timer execution workload. Consistent design enables easier testing, migration, and multi-database support.
- **Alternatives**: Database-specific optimizations, timer_id in primary key for uniqueness, separate primary keys for different databases, complex uniqueness constraints
- **Impact**: Simplified implementation with no database-specific logic, consistent performance patterns across all databases, easier database migration and multi-database environments, optimal performance for both execution and CRUD operations

### [Date: 2025-07-20] Development Cassandra Docker Compose Setup
- **Context**: Need a reliable development environment for the timer service using Cassandra as the database backend. Must automatically initialize database schema, provide clean state for each development session, and be easy to start/stop.
- **Decision**: Implement two-container Docker Compose setup: main Cassandra service for database, separate initialization service for schema setup. Use health check dependencies, no persistent volumes for fresh state, and same Cassandra image for initialization to leverage built-in cqlsh tool.
- **Rationale**: Separate initialization container provides clean separation of concerns and reliable startup sequence. Using health check dependencies ensures Cassandra is ready before initialization. No persistent volumes gives fresh database state for reproducible development. Same image for init service provides compatible cqlsh without additional dependencies.
- **Alternatives**: Single container with custom entrypoint (unreliable), Python container with cassandra-driver (complex), persistent volumes (stale data issues), external initialization scripts (not containerized)
- **Impact**: Reliable automated database initialization, reproducible development environment, clean container separation, simplified maintenance using standard Cassandra tools

### [Date: 2025-07-19] Server and CLI Technology Stack Decision
- **Context**: Need to choose implementation language for the core timer service server and CLI tools. Must prioritize performance, concurrency, operational simplicity, and development productivity for a distributed system handling millions of timers.
- **Decision**: Implement both server and CLI components in Go (Golang). Server follows standard Go project layout with cmd/, internal/, and pkg/ directories. CLI uses Cobra framework for command-line interface. Both components share common libraries through internal packages.
- **Rationale**: Go provides excellent concurrency primitives (goroutines) for timer execution, strong HTTP standard library, mature ecosystem for database drivers, simple deployment (single binary), fast compilation, and strong operational characteristics. CLI consistency with server simplifies build and deployment.
- **Alternatives**: Java (Spring Boot), Rust, Python (FastAPI), Node.js, C#, separate language for CLI
- **Impact**: Enables high-performance concurrent timer execution, simplified deployment and operations, consistent development experience across server and CLI, leverages Go's strong ecosystem for distributed systems

### [Date: 2025-07-19] Repository Layout and Structure Decision
- **Context**: Need to organize a complex multi-component project including server implementation, multi-language SDKs, WebUI, CLI tools, deployment configurations, and comprehensive testing. Structure must support independent development, multi-language builds, and operational excellence.
- **Decision**: Implement component-based repository structure with clear separation of concerns: server/, sdks/{language}/, webUI/, cli/, benchmark/, docker/, helmcharts/, operations/, .github/, and docs/. Each component has independent development lifecycle while maintaining integration points. Full specification: [docs/repo-layout.md](docs/repo-layout.md)
- **Rationale**: Separation of concerns enables parallel development, component-specific CI/CD pipelines, independent versioning, and clear ownership boundaries. Multi-language SDK organization supports language-specific tooling and best practices. Infrastructure-as-code organization enables reliable deployment automation.
- **Alternatives**: Monolithic structure, separate repositories per component, language-agnostic SDK organization, merged infrastructure and application code
- **Impact**: Enables parallel development across multiple teams, supports independent release cycles, facilitates multi-language SDK maintenance, and provides clear operational deployment path through Docker and Kubernetes configurations

### [Date: 2025-07-19] DynamoDB LSI Cost Optimization Decision
- **Context**: Need to balance DynamoDB costs vs storage flexibility. Original design used GSI to avoid LSI 10GB partition limit, but GSI incurs significant additional costs for write-heavy timer workloads due to separate write capacity consumption on both base table and GSI.
- **Decision**: Revert to Local Secondary Index (LSI) instead of Global Secondary Index (GSI) for DynamoDB implementation to minimize costs. Manage the 10GB partition limit through administrative controls - when partitions approach the limit, create new groups with higher shard counts to redistribute load.
- **Rationale**: LSI is significantly more cost-effective for write-heavy workloads since writes are included in base table capacity. The 10GB limit is manageable through proper capacity planning and administrative resharding. Cost savings outweigh the operational overhead of monitoring partition sizes.
- **Alternatives**: Continue using GSI for unlimited storage, hybrid approach with both LSI and GSI options, separate storage backends for large partitions
- **Impact**: Substantial cost reduction for DynamoDB deployments, requires monitoring of partition sizes and administrative resharding procedures, establishes foundation for future premium GSI features

### [Date: 2025-07-19] API Documentation Decision
- **Context**: Need to document API design decisions and rationale for future reference and team alignment
- **Decision**: Create comprehensive API design document at [docs/design/api-design.md](docs/design/api-design.md) covering all design decisions, principles, and examples
- **Rationale**: Centralized documentation ensures team understanding of design choices, facilitates onboarding, and provides reference for future API evolution decisions
- **Alternatives**: Inline API comments only, separate architecture document, wiki-based documentation
- **Impact**: Clear design documentation supports consistent implementation and informed future decisions

### [Date: 2025-07-19] Timestamp Data Type Decision
- **Context**: Need efficient time representation for execute_at field that is both performant and human-readable across different database types
- **Decision**: Use native timestamp data types in each database (TIMESTAMP in SQL databases, ISODate in MongoDB, etc.) for efficiency and human readability
- **Rationale**: Native timestamp types provide efficient storage and indexing, enable database-native time operations, maintain human readability for debugging, and support timezone handling
- **Alternatives**: Unix epoch integers, string-based ISO timestamps, separate date/time fields, UTC-only timestamps
- **Impact**: Efficient time-based queries, database-specific timestamp handling, human-readable storage, requires consistent timezone handling across systems

### [Date: 2025-07-19] Query Frequency Optimization Decision
- **Context**: Need to choose between optimizing for timer CRUD operations (by timer_id) vs timer execution queries (by execute_at). Analysis shows execution queries happen continuously every few seconds per shard, while CRUD operations are occasional user-driven actions.
- **Decision**: Optimize all database schemas for high-frequency execution queries: use execute_at as primary clustering/sort key with secondary indexes on timer_id for CRUD operations. Applied to Cassandra, MongoDB, TiDB, and DynamoDB.
- **Rationale**: Timer execution is the core service operation happening continuously at scale, while CRUD operations are infrequent user actions. Optimizing for the most frequent operation provides better overall system performance across all database types.
- **Alternatives**: Optimize for CRUD operations with execute_at secondary indexes, dual table design, hybrid clustering approaches, database-specific optimizations
- **Impact**: Extremely fast execution queries using primary key/index order across all databases, CRUD operations use secondary indexes with acceptable performance cost, consistent optimization strategy across all supported databases

### [Date: 2025-07-19] Primary Key Size Optimization Decision
- **Context**: Need to balance query performance optimization with primary key size efficiency. Larger primary keys impact index size, storage overhead, and query performance. Some databases support non-unique primary keys with separate unique constraints.
- **Decision**: Use database-specific primary key strategies - smaller primary keys for databases supporting non-unique indexes (MongoDB), and include timer_id in primary key only when required for uniqueness (Cassandra, TiDB, DynamoDB)
- **Rationale**: Smaller primary keys improve index performance, reduce storage overhead, and enhance cache efficiency. For databases that support it, separating query optimization from uniqueness constraints provides better performance.
- **Alternatives**: Use same primary key strategy across all databases, always include timer_id in primary key, use separate tables for different access patterns
- **Impact**: MongoDB gets optimized smaller primary index (shardId, executeAt) with separate unique constraint, while Cassandra/TiDB/DynamoDB retain (shardId, executeAt, timerId) primary keys for uniqueness requirements

### [Date: 2025-07-19] Database Partitioning Strategy Decision
- **Context**: Need efficient storage design to support millions of concurrent timers with deterministic lookups and horizontal scaling
- **Decision**: Use deterministic partitioning with shardId = hash(timerId) % group.numShards, where different groups have configurable shard counts based on scale requirements
- **Rationale**: Deterministic sharding eliminates scatter-gather queries, enables direct shard targeting for O(1) operations, supports different scale requirements per group, and provides predictable performance
- **Alternatives**: Random sharding, time-based partitioning, consistent hashing with virtual nodes, single database without partitioning
- **Impact**: Enables horizontal scaling, requires shard computation logic, provides predictable query performance, supports multi-tenant workload isolation

### [Date: 2025-07-19] Simplified Timer Model Decision
- **Context**: Need to balance functionality with simplicity to avoid over-engineering
- **Decision**: Remove timer status tracking and nextExecuteAt fields from timer model, handle rescheduling via callback responses only
- **Rationale**: Simplifies data model, reduces state management complexity, focuses on core timer functionality, makes rescheduling explicit via callback interaction
- **Alternatives**: Complex status state machine, persistent next execution tracking, hybrid approaches
- **Impact**: Simplified implementation, requires callback-driven rescheduling, reduces storage complexity

### [Date: 2025-07-19] Callback Response Protocol Decision
- **Context**: Need standardized way for callbacks to indicate success/failure and enable timer rescheduling (FR-2.4)
- **Decision**: Define CallbackResponse schema with required 'ok' boolean and optional 'nextExecuteAt' for rescheduling
- **Rationale**: Clear success/failure semantics prevent ambiguous callback responses, enables timer rescheduling capability, standardizes callback contract across all integrations
- **Alternatives**: HTTP status codes only, custom headers, implicit rescheduling logic
- **Impact**: Requires callback endpoints to return structured JSON response, enables powerful rescheduling workflows

### [Date: 2025-07-19] Group-Based Scalability Decision
- **Context**: Need to design for horizontal scaling to support millions of concurrent timers as specified in requirements
- **Decision**: Implement group-based sharding with composite keys {groupId, timerId} for all timer operations
- **Rationale**: Groups enable horizontal scaling by distributing different groups across service instances, provide workload isolation, and support efficient lookups without expensive list operations
- **Alternatives**: Single global namespace, hash-based sharding, time-based partitioning
- **Impact**: Enables horizontal scaling, requires clients to specify groupId, simplifies sharding logic

### [Date: 2025-07-19] API Design Decision  
- **Context**: Need to define the REST API structure and data models for the timer service
- **Decision**: Minimal RESTful API with group-based scalability using composite keys {groupId, timerId}, standardized callback response protocol, and enhanced retry policies with duration limits. Full specification: [api.yaml](api.yaml), Design documentation: [docs/design/api-design.md](docs/design/api-design.md)
- **Rationale**: Group-based sharding enables horizontal scaling, composite keys support efficient lookups, CallbackResponse schema provides clear success/failure semantics with rescheduling capability, simplified timer model reduces complexity
- **Alternatives**: Single-key timers, complex status tracking, implicit callback responses, basic retry policies
- **Impact**: Enables horizontal scaling through sharding, clear callback semantics, flexible retry control, simplified client integration

### [Date: 2025-07-19] WebUI Inclusion Decision
- **Context**: Need to decide whether to include a web-based user interface for timer management and monitoring
- **Decision**: Include a comprehensive WebUI as part of the core service offering
- **Rationale**: A WebUI will significantly improve operator experience by providing visual timer management, real-time monitoring, and system health visibility. This reduces the learning curve and operational complexity.
- **Alternatives**: CLI-only interface, separate third-party monitoring tools, API-only approach
- **Impact**: Adds frontend development complexity but greatly improves usability and adoption potential

### [Date: 2025-07-19] Requirements Adherence Rule
- **Context**: During API design, I added features not explicitly specified in requirements (list API, execution history), which violates the principle of building only what's specified
- **Decision**: Add strict requirements adherence rule to project guidelines - implement only what is explicitly documented in requirements
- **Rationale**: Prevents scope creep, maintains project focus, ensures we build exactly what was specified rather than what seems "obvious" or "nice to have"
- **Alternatives**: Allow reasonable feature additions, rely on judgment calls for "obvious" features
- **Impact**: Requires explicit requirements updates for any new features, prevents uncontrolled feature growth

---

*This log should be updated whenever significant decisions are made during the project development.* 