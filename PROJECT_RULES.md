# Distributed Durable Timer Service - Project Rules & Guidelines

## Core Project Rules

### 1. Decision Recording
**Rule**: All major architectural and design decisions made during interactions between collaborators must be recorded in the [DECISION_LOG.md](DECISION_LOG.md) file with:
- Date of decision
- Context/problem being solved
- Decision made
- Rationale
- Alternative options considered
- Impact and status

### 2. Requirements Adherence
**Rule**: Strictly implement only what is explicitly specified in the requirements document. Do not add features, endpoints, or functionality that are not clearly stated in the requirements, even if they seem like "obvious" additions or improvements.
- Only build what is documented in REQUIREMENTS.md
- If additional features are needed, first update the requirements document
- Avoid scope creep and feature assumptions
- When in doubt, ask for clarification rather than assuming intent

### 3. Code Quality Standards
- Follow language-specific best practices and conventions
- Write comprehensive tests (unit, integration, and end-to-end)
- Document all public APIs and interfaces
- Use meaningful variable and function names
- Include error handling for all failure scenarios

### 4. Distributed Systems Principles
- Design for failure - assume components will fail
- Implement proper retry mechanisms with exponential backoff
- Ensure idempotency for all operations
- Design for horizontal scaling by adding more shards and service instances
- Implement proper monitoring and observability

### 5. SDK Design Principles
- Provide simple, intuitive APIs
- Support multiple programming languages
- Include comprehensive documentation and examples
- Handle connection failures gracefully
- Provide both synchronous and asynchronous interfaces

### 6. Documentation Requirements
- Maintain up-to-date README with setup instructions
- Document API specifications (OpenAPI/Swagger)
- Provide getting started guides and tutorials
- Include performance benchmarks and limitations
- Document deployment and operational procedures

### 7. Performance Standards
- Optimize for both throughput and latency
- Support graceful degradation under load
- Benchmark against realistic workloads

### 8. Development and Testing Guidelines
**Rule**: When running tests, assume that the required database environments are already set up on the machine. Do not attempt to run Docker commands or set up database containers as part of the testing process.
- Assume databases (Cassandra, PostgreSQL, MySQL, MongoDB, DynamoDB, etc.) are running and accessible
- Focus on test execution rather than environment setup
- If tests fail due to missing database connections, report the issue without attempting automated setup

---

## Future Considerations
- Disaster recovery and backup strategies
- Cross-region replication
- Integration with existing monitoring systems
- Migration strategies for data and configurations
- Compliance and regulatory requirements

---

*This document is a living guideline and should be updated as the project evolves.* 