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


---

## Decisions

### [Date: 2025-07-19] Requirements Adherence Rule
- **Context**: During API design, I added features not explicitly specified in requirements (list API, execution history), which violates the principle of building only what's specified
- **Decision**: Add strict requirements adherence rule to project guidelines - implement only what is explicitly documented in requirements
- **Rationale**: Prevents scope creep, maintains project focus, ensures we build exactly what was specified rather than what seems "obvious" or "nice to have"
- **Alternatives**: Allow reasonable feature additions, rely on judgment calls for "obvious" features
- **Impact**: Requires explicit requirements updates for any new features, prevents uncontrolled feature growth

### [Date: 2025-07-19] WebUI Inclusion Decision
- **Context**: Need to decide whether to include a web-based user interface for timer management and monitoring
- **Decision**: Include a comprehensive WebUI as part of the core service offering
- **Rationale**: A WebUI will significantly improve operator experience by providing visual timer management, real-time monitoring, and system health visibility. This reduces the learning curve and operational complexity.
- **Alternatives**: CLI-only interface, separate third-party monitoring tools, API-only approach
- **Impact**: Adds frontend development complexity but greatly improves usability and adoption potential

### [Date: 2025-07-19] API Design Decision  
- **Context**: Need to define the REST API structure and data models for the timer service
- **Decision**: Minimal RESTful API with group-based scalability using composite keys {groupId, timerId}, standardized callback response protocol, and enhanced retry policies with duration limits. Full specification: [api.yaml](api.yaml), Design documentation: [docs/design/api-design.md](docs/design/api-design.md)
- **Rationale**: Group-based sharding enables horizontal scaling, composite keys support efficient lookups, CallbackResponse schema provides clear success/failure semantics with rescheduling capability, simplified timer model reduces complexity
- **Alternatives**: Single-key timers, complex status tracking, implicit callback responses, basic retry policies
- **Impact**: Enables horizontal scaling through sharding, clear callback semantics, flexible retry control, simplified client integration

### [Date: 2025-07-19] Group-Based Scalability Decision
- **Context**: Need to design for horizontal scaling to support millions of concurrent timers as specified in requirements
- **Decision**: Implement group-based sharding with composite keys {groupId, timerId} for all timer operations
- **Rationale**: Groups enable horizontal scaling by distributing different groups across service instances, provide workload isolation, and support efficient lookups without expensive list operations
- **Alternatives**: Single global namespace, hash-based sharding, time-based partitioning
- **Impact**: Enables horizontal scaling, requires clients to specify groupId, simplifies sharding logic

### [Date: 2025-07-19] Callback Response Protocol Decision
- **Context**: Need standardized way for callbacks to indicate success/failure and enable timer rescheduling (FR-2.4)
- **Decision**: Define CallbackResponse schema with required 'ok' boolean and optional 'nextExecuteAt' for rescheduling
- **Rationale**: Clear success/failure semantics prevent ambiguous callback responses, enables timer rescheduling capability, standardizes callback contract across all integrations
- **Alternatives**: HTTP status codes only, custom headers, implicit rescheduling logic
- **Impact**: Requires callback endpoints to return structured JSON response, enables powerful rescheduling workflows

### [Date: 2025-07-19] Simplified Timer Model Decision
- **Context**: Need to balance functionality with simplicity to avoid over-engineering
- **Decision**: Remove timer status tracking and nextExecuteAt fields from timer model, handle rescheduling via callback responses only
- **Rationale**: Simplifies data model, reduces state management complexity, focuses on core timer functionality, makes rescheduling explicit via callback interaction
- **Alternatives**: Complex status state machine, persistent next execution tracking, hybrid approaches
- **Impact**: Simplified implementation, requires callback-driven rescheduling, reduces storage complexity

### [Date: 2025-07-19] Database Partitioning Strategy Decision
- **Context**: Need efficient storage design to support millions of concurrent timers with deterministic lookups and horizontal scaling
- **Decision**: Use deterministic partitioning with shardId = hash(timerId) % group.numShards, where different groups have configurable shard counts based on scale requirements
- **Rationale**: Deterministic sharding eliminates scatter-gather queries, enables direct shard targeting for O(1) operations, supports different scale requirements per group, and provides predictable performance
- **Alternatives**: Random sharding, time-based partitioning, consistent hashing with virtual nodes, single database without partitioning
- **Impact**: Enables horizontal scaling, requires shard computation logic, provides predictable query performance, supports multi-tenant workload isolation

### [Date: 2025-07-19] Primary Key Size Optimization Decision
- **Context**: Need to balance query performance optimization with primary key size efficiency. Larger primary keys impact index size, storage overhead, and query performance. Some databases support non-unique primary keys with separate unique constraints.
- **Decision**: Use database-specific primary key strategies - smaller primary keys for databases supporting non-unique indexes (MongoDB), and include timer_id in primary key only when required for uniqueness (Cassandra, TiDB, DynamoDB)
- **Rationale**: Smaller primary keys improve index performance, reduce storage overhead, and enhance cache efficiency. For databases that support it, separating query optimization from uniqueness constraints provides better performance.
- **Alternatives**: Use same primary key strategy across all databases, always include timer_id in primary key, use separate tables for different access patterns
- **Impact**: MongoDB gets optimized smaller primary index (shardId, executeAt) with separate unique constraint, while Cassandra/TiDB/DynamoDB retain (shardId, executeAt, timerId) primary keys for uniqueness requirements

### [Date: 2025-07-19] Query Frequency Optimization Decision
- **Context**: Need to choose between optimizing for timer CRUD operations (by timer_id) vs timer execution queries (by execute_at). Analysis shows execution queries happen continuously every few seconds per shard, while CRUD operations are occasional user-driven actions.
- **Decision**: Optimize all database schemas for high-frequency execution queries: use execute_at as primary clustering/sort key with secondary indexes on timer_id for CRUD operations. Applied to Cassandra, MongoDB, TiDB, and DynamoDB.
- **Rationale**: Timer execution is the core service operation happening continuously at scale, while CRUD operations are infrequent user actions. Optimizing for the most frequent operation provides better overall system performance across all database types.
- **Alternatives**: Optimize for CRUD operations with execute_at secondary indexes, dual table design, hybrid clustering approaches, database-specific optimizations
- **Impact**: Extremely fast execution queries using primary key/index order across all databases, CRUD operations use secondary indexes with acceptable performance cost, consistent optimization strategy across all supported databases

### [Date: 2025-07-19] Timestamp Data Type Decision
- **Context**: Need efficient time representation for execute_at field that is both performant and human-readable across different database types
- **Decision**: Use native timestamp data types in each database (TIMESTAMP in SQL databases, ISODate in MongoDB, etc.) for efficiency and human readability
- **Rationale**: Native timestamp types provide efficient storage and indexing, enable database-native time operations, maintain human readability for debugging, and support timezone handling
- **Alternatives**: Unix epoch integers, string-based ISO timestamps, separate date/time fields, UTC-only timestamps
- **Impact**: Efficient time-based queries, database-specific timestamp handling, human-readable storage, requires consistent timezone handling across systems

### [Date: 2025-07-19] API Documentation Decision
- **Context**: Need to document API design decisions and rationale for future reference and team alignment
- **Decision**: Create comprehensive API design document at [docs/design/api-design.md](docs/design/api-design.md) covering all design decisions, principles, and examples
- **Rationale**: Centralized documentation ensures team understanding of design choices, facilitates onboarding, and provides reference for future API evolution decisions
- **Alternatives**: Inline API comments only, separate architecture document, wiki-based documentation
- **Impact**: Clear design documentation supports consistent implementation and informed future decisions

### [Date: 2025-07-19] DynamoDB LSI Cost Optimization Decision
- **Context**: Need to balance DynamoDB costs vs storage flexibility. Original design used GSI to avoid LSI 10GB partition limit, but GSI incurs significant additional costs for write-heavy timer workloads due to separate write capacity consumption on both base table and GSI.
- **Decision**: Revert to Local Secondary Index (LSI) instead of Global Secondary Index (GSI) for DynamoDB implementation to minimize costs. Manage the 10GB partition limit through administrative controls - when partitions approach the limit, create new groups with higher shard counts to redistribute load.
- **Rationale**: LSI is significantly more cost-effective for write-heavy workloads since writes are included in base table capacity. The 10GB limit is manageable through proper capacity planning and administrative resharding. Cost savings outweigh the operational overhead of monitoring partition sizes.
- **Alternatives**: Continue using GSI for unlimited storage, hybrid approach with both LSI and GSI options, separate storage backends for large partitions
- **Impact**: Substantial cost reduction for DynamoDB deployments, requires monitoring of partition sizes and administrative resharding procedures, establishes foundation for future premium GSI features

### [Date: 2025-07-19] Repository Layout and Structure Decision
- **Context**: Need to organize a complex multi-component project including server implementation, multi-language SDKs, WebUI, CLI tools, deployment configurations, and comprehensive testing. Structure must support independent development, multi-language builds, and operational excellence.
- **Decision**: Implement component-based repository structure with clear separation of concerns: server/, sdks/{language}/, webUI/, cli/, benchmark/, docker/, helmcharts/, operations/, .github/, and docs/. Each component has independent development lifecycle while maintaining integration points. Full specification: [docs/repo-layout.md](docs/repo-layout.md)
- **Rationale**: Separation of concerns enables parallel development, component-specific CI/CD pipelines, independent versioning, and clear ownership boundaries. Multi-language SDK organization supports language-specific tooling and best practices. Infrastructure-as-code organization enables reliable deployment automation.
- **Alternatives**: Monolithic structure, separate repositories per component, language-agnostic SDK organization, merged infrastructure and application code
- **Impact**: Enables parallel development across multiple teams, supports independent release cycles, facilitates multi-language SDK maintenance, and provides clear operational deployment path through Docker and Kubernetes configurations

### [Date: 2025-07-19] Server and CLI Technology Stack Decision
- **Context**: Need to choose implementation language for the core timer service server and CLI tools. Must prioritize performance, concurrency, operational simplicity, and development productivity for a distributed system handling millions of timers.
- **Decision**: Implement both server and CLI components in Go (Golang). Server follows standard Go project layout with cmd/, internal/, and pkg/ directories. CLI uses Cobra framework for command-line interface. Both components share common libraries through internal packages.
- **Rationale**: Go provides excellent concurrency primitives (goroutines) for timer execution, strong HTTP standard library, mature ecosystem for database drivers, simple deployment (single binary), fast compilation, and strong operational characteristics. CLI consistency with server simplifies build and deployment.
- **Alternatives**: Java (Spring Boot), Rust, Python (FastAPI), Node.js, C#, separate language for CLI
- **Impact**: Enables high-performance concurrent timer execution, simplified deployment and operations, consistent development experience across server and CLI, leverages Go's strong ecosystem for distributed systems

### [Date: 2025-07-20] Development Cassandra Docker Compose Setup
- **Context**: Need a reliable development environment for the timer service using Cassandra as the database backend. Must automatically initialize database schema, provide clean state for each development session, and be easy to start/stop.
- **Decision**: Implement two-container Docker Compose setup: main Cassandra service for database, separate initialization service for schema setup. Use health check dependencies, no persistent volumes for fresh state, and same Cassandra image for initialization to leverage built-in cqlsh tool.
- **Rationale**: Separate initialization container provides clean separation of concerns and reliable startup sequence. Using health check dependencies ensures Cassandra is ready before initialization. No persistent volumes gives fresh database state for reproducible development. Same image for init service provides compatible cqlsh without additional dependencies.
- **Alternatives**: Single container with custom entrypoint (unreliable), Python container with cassandra-driver (complex), persistent volumes (stale data issues), external initialization scripts (not containerized)
- **Impact**: Reliable automated database initialization, reproducible development environment, clean container separation, simplified maintenance using standard Cassandra tools

### [Date: 2025-07-20] Unified Database Schema Design
- **Context**: Previous database designs varied significantly across different databases, with complex primary key strategies and database-specific optimizations that created implementation complexity and inconsistent performance patterns.
- **Decision**: Implement universal `(shard_id, execute_at, timer_uuid)` primary key design across all databases with `timer_id` as unique index. Use UUID for uniqueness instead of complex constraints, and ensure execute_at is first clustering key for optimal execution performance.
- **Rationale**: Eliminates database-specific complexity while maintaining optimal performance. UUID provides simple uniqueness without complex collision handling. execute_at first optimizes core timer execution workload. Consistent design enables easier testing, migration, and multi-database support.
- **Alternatives**: Database-specific optimizations, timer_id in primary key for uniqueness, separate primary keys for different databases, complex uniqueness constraints
- **Impact**: Simplified implementation with no database-specific logic, consistent performance patterns across all databases, easier database migration and multi-database environments, optimal performance for both execution and CRUD operations

### [Date: 2025-07-20] DynamoDB Special Case Design
- **Context**: DynamoDB has unique constraints that prevent using the universal schema design: it only supports one sort key per table (not multiple columns), doesn't support UUID natively, and physical clustering optimization is unnecessary since it's a managed service without self-hosting options.
- **Decision**: Use a special DynamoDB-specific design with `(shard_id, timer_id)` as primary key and `execute_at` as Local Secondary Index (LSI). This provides direct timer CRUD operations via primary key and timer execution queries via the LSI.
- **Rationale**: DynamoDB's single sort key limitation requires a different approach. Using timer_id as sort key enables direct CRUD operations without complex composite keys. LSI on execute_at provides cost-effective timer execution queries since LSI writes don't incur additional capacity costs. This design maintains consistency with the logical intent while adapting to DynamoDB's constraints.
- **Alternatives**: Use composite range key (executeAt#timerUuid), use Global Secondary Index instead of LSI, separate tables for different access patterns
- **Impact**: DynamoDB implementation differs from other databases but provides optimal cost and performance characteristics for the managed service environment, maintains logical consistency while adapting to platform constraints

---

*This log should be updated whenever significant decisions are made during the project development.* 