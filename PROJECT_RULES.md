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

### 2. Code Quality Standards
- Follow language-specific best practices and conventions
- Write comprehensive tests (unit, integration, and end-to-end)
- Document all public APIs and interfaces
- Use meaningful variable and function names
- Include error handling for all failure scenarios

### 3. Distributed Systems Principles
- Design for failure - assume components will fail
- Implement proper retry mechanisms with exponential backoff
- Ensure idempotency for all operations
- Design for horizontal scalability
- Implement proper monitoring and observability

### 4. SDK Design Principles
- Provide simple, intuitive APIs
- Support multiple programming languages
- Include comprehensive documentation and examples
- Handle connection failures gracefully
- Provide both synchronous and asynchronous interfaces

### 5. Documentation Requirements
- Maintain up-to-date README with setup instructions
- Document API specifications (OpenAPI/Swagger)
- Provide getting started guides and tutorials
- Include performance benchmarks and limitations
- Document deployment and operational procedures

### 6. Performance Standards
- Optimize for both throughput and latency
- Support graceful degradation under load
- Benchmark against realistic workloads

---

## Future Considerations
- Disaster recovery and backup strategies
- Cross-region replication
- Integration with existing monitoring systems
- Migration strategies for data and configurations
- Compliance and regulatory requirements

---

*This document is a living guideline and should be updated as the project evolves.* 