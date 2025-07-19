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
- **Status**: Active, Superseded, or Deprecated

---

## Decisions

### [Date: 2025-07-19] Requirements Adherence Rule
- **Context**: During API design, I added features not explicitly specified in requirements (list API, execution history), which violates the principle of building only what's specified
- **Decision**: Add strict requirements adherence rule to project guidelines - implement only what is explicitly documented in requirements
- **Rationale**: Prevents scope creep, maintains project focus, ensures we build exactly what was specified rather than what seems "obvious" or "nice to have"
- **Alternatives**: Allow reasonable feature additions, rely on judgment calls for "obvious" features
- **Impact**: Requires explicit requirements updates for any new features, prevents uncontrolled feature growth
- **Status**: Active

### [Date: 2025-07-19] WebUI Inclusion Decision
- **Context**: Need to decide whether to include a web-based user interface for timer management and monitoring
- **Decision**: Include a comprehensive WebUI as part of the core service offering
- **Rationale**: A WebUI will significantly improve operator experience by providing visual timer management, real-time monitoring, and system health visibility. This reduces the learning curve and operational complexity.
- **Alternatives**: CLI-only interface, separate third-party monitoring tools, API-only approach
- **Impact**: Adds frontend development complexity but greatly improves usability and adoption potential
- **Status**: Active

### [Date: TBD] Initial Architecture Decision
- **Context**: Need to choose the overall architecture for the distributed timer service
- **Decision**: [To be determined]
- **Rationale**: [To be filled]
- **Alternatives**: [To be documented]
- **Impact**: [To be assessed]
- **Status**: Pending

### [Date: 2025-07-19] API Design Decision  
- **Context**: Need to define the REST API structure and data models for the timer service
- **Decision**: Minimal RESTful API with group-based scalability using composite keys {groupId, timerId}, standardized callback response protocol, and enhanced retry policies with duration limits. Full specification: [api.yaml](api.yaml), Design documentation: [docs/design/api-design.md](docs/design/api-design.md)
- **Rationale**: Group-based sharding enables horizontal scaling, composite keys support efficient lookups, CallbackResponse schema provides clear success/failure semantics with rescheduling capability, simplified timer model reduces complexity
- **Alternatives**: Single-key timers, complex status tracking, implicit callback responses, basic retry policies
- **Impact**: Enables horizontal scaling through sharding, clear callback semantics, flexible retry control, simplified client integration
- **Status**: Active

### [Date: 2025-07-19] Group-Based Scalability Decision
- **Context**: Need to design for horizontal scaling to support millions of concurrent timers as specified in requirements
- **Decision**: Implement group-based sharding with composite keys {groupId, timerId} for all timer operations
- **Rationale**: Groups enable horizontal scaling by distributing different groups across service instances, provide workload isolation, and support efficient lookups without expensive list operations
- **Alternatives**: Single global namespace, hash-based sharding, time-based partitioning
- **Impact**: Enables horizontal scaling, requires clients to specify groupId, simplifies sharding logic
- **Status**: Active

### [Date: 2025-07-19] Callback Response Protocol Decision
- **Context**: Need standardized way for callbacks to indicate success/failure and enable timer rescheduling (FR-2.4)
- **Decision**: Define CallbackResponse schema with required 'ok' boolean and optional 'nextExecuteAt' for rescheduling
- **Rationale**: Clear success/failure semantics prevent ambiguous callback responses, enables timer rescheduling capability, standardizes callback contract across all integrations
- **Alternatives**: HTTP status codes only, custom headers, implicit rescheduling logic
- **Impact**: Requires callback endpoints to return structured JSON response, enables powerful rescheduling workflows
- **Status**: Active

### [Date: 2025-07-19] Simplified Timer Model Decision
- **Context**: Need to balance functionality with simplicity to avoid over-engineering
- **Decision**: Remove timer status tracking and nextExecuteAt fields from timer model, handle rescheduling via callback responses only
- **Rationale**: Simplifies data model, reduces state management complexity, focuses on core timer functionality, makes rescheduling explicit via callback interaction
- **Alternatives**: Complex status state machine, persistent next execution tracking, hybrid approaches
- **Impact**: Simplified implementation, requires callback-driven rescheduling, reduces storage complexity
- **Status**: Active

### [Date: 2025-07-19] Database Partitioning Strategy Decision
- **Context**: Need efficient storage design to support millions of concurrent timers with deterministic lookups and horizontal scaling
- **Decision**: Use deterministic partitioning with shardId = hash(timerId) % group.numShards, where different groups have configurable shard counts based on scale requirements
- **Rationale**: Deterministic sharding eliminates scatter-gather queries, enables direct shard targeting for O(1) operations, supports different scale requirements per group, and provides predictable performance
- **Alternatives**: Random sharding, time-based partitioning, consistent hashing with virtual nodes, single database without partitioning
- **Impact**: Enables horizontal scaling, requires shard computation logic, provides predictable query performance, supports multi-tenant workload isolation
- **Status**: Active

### [Date: 2025-07-19] Primary Key Size Optimization Decision
- **Context**: Need to balance query performance optimization with primary key size efficiency. Larger primary keys impact index size, storage overhead, and query performance. Some databases support non-unique primary keys with separate unique constraints.
- **Decision**: Use database-specific primary key strategies - smaller primary keys for databases supporting non-unique indexes (MongoDB), and include timer_id in primary key only when required for uniqueness (Cassandra, TiDB, DynamoDB)
- **Rationale**: Smaller primary keys improve index performance, reduce storage overhead, and enhance cache efficiency. For databases that support it, separating query optimization from uniqueness constraints provides better performance.
- **Alternatives**: Use same primary key strategy across all databases, always include timer_id in primary key, use separate tables for different access patterns
- **Impact**: MongoDB gets optimized smaller primary index (shardId, executeAt) with separate unique constraint, while Cassandra/TiDB/DynamoDB retain (shardId, executeAt, timerId) primary keys for uniqueness requirements
- **Status**: Active

### [Date: 2025-07-19] Query Frequency Optimization Decision
- **Context**: Need to choose between optimizing for timer CRUD operations (by timer_id) vs timer execution queries (by execute_at). Analysis shows execution queries happen continuously every few seconds per shard, while CRUD operations are occasional user-driven actions.
- **Decision**: Optimize all database schemas for high-frequency execution queries: use execute_at as primary clustering/sort key with secondary indexes on timer_id for CRUD operations. Applied to Cassandra, MongoDB, TiDB, and DynamoDB.
- **Rationale**: Timer execution is the core service operation happening continuously at scale, while CRUD operations are infrequent user actions. Optimizing for the most frequent operation provides better overall system performance across all database types.
- **Alternatives**: Optimize for CRUD operations with execute_at secondary indexes, dual table design, hybrid clustering approaches, database-specific optimizations
- **Impact**: Extremely fast execution queries using primary key/index order across all databases, CRUD operations use secondary indexes with acceptable performance cost, consistent optimization strategy across all supported databases
- **Status**: Active

### [Date: 2025-07-19] Timestamp Data Type Decision
- **Context**: Need efficient time representation for execute_at field that is both performant and human-readable across different database types
- **Decision**: Use native timestamp data types in each database (TIMESTAMP in SQL databases, ISODate in MongoDB, etc.) for efficiency and human readability
- **Rationale**: Native timestamp types provide efficient storage and indexing, enable database-native time operations, maintain human readability for debugging, and support timezone handling
- **Alternatives**: Unix epoch integers, string-based ISO timestamps, separate date/time fields, UTC-only timestamps
- **Impact**: Efficient time-based queries, database-specific timestamp handling, human-readable storage, requires consistent timezone handling across systems
- **Status**: Active

### [Date: 2025-07-19] API Documentation Decision
- **Context**: Need to document API design decisions and rationale for future reference and team alignment
- **Decision**: Create comprehensive API design document at [docs/design/api-design.md](docs/design/api-design.md) covering all design decisions, principles, and examples
- **Rationale**: Centralized documentation ensures team understanding of design choices, facilitates onboarding, and provides reference for future API evolution decisions
- **Alternatives**: Inline API comments only, separate architecture document, wiki-based documentation
- **Impact**: Clear design documentation supports consistent implementation and informed future decisions
- **Status**: Active

### [Date: 2025-07-19] DynamoDB LSI Cost Optimization Decision
- **Context**: Need to balance DynamoDB costs vs storage flexibility. Original design used GSI to avoid LSI 10GB partition limit, but GSI incurs significant additional costs for write-heavy timer workloads due to separate write capacity consumption on both base table and GSI.
- **Decision**: Revert to Local Secondary Index (LSI) instead of Global Secondary Index (GSI) for DynamoDB implementation to minimize costs. Manage the 10GB partition limit through administrative controls - when partitions approach the limit, create new groups with higher shard counts to redistribute load.
- **Rationale**: LSI is significantly more cost-effective for write-heavy workloads since writes are included in base table capacity. The 10GB limit is manageable through proper capacity planning and administrative resharding. Cost savings outweigh the operational overhead of monitoring partition sizes.
- **Alternatives**: Continue using GSI for unlimited storage, hybrid approach with both LSI and GSI options, separate storage backends for large partitions
- **Impact**: Substantial cost reduction for DynamoDB deployments, requires monitoring of partition sizes and administrative resharding procedures, establishes foundation for future premium GSI features
- **Status**: Active

### [Date: TBD] Technology Stack Selection
- **Context**: Choose programming languages, frameworks, and core technologies
- **Decision**: [To be determined]
- **Rationale**: [To be filled]
- **Alternatives**: [To be documented]
- **Impact**: [To be assessed]
- **Status**: Pending

### [Date: TBD] Storage Backend Choice
- **Context**: Select persistent storage solution for timer data
- **Decision**: [To be determined]
- **Rationale**: [To be filled]
- **Alternatives**: [To be documented]
- **Impact**: [To be assessed]
- **Status**: Pending

---

## Decision Categories
- **Architecture**: High-level system design decisions
- **Technology**: Choice of languages, frameworks, libraries, and tools
- **Design**: API design, data models, and interfaces
- **Infrastructure**: Deployment, scaling, and operational decisions
- **Security**: Authentication, authorization, and security measures
- **Performance**: Optimization and scalability decisions

---

*This log should be updated whenever significant decisions are made during the project development.* 