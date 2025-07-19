# Distributed Durable Timer Service - Repository Layout Design

This document describes the repository structure and organization of the distributed durable timer service project.

## Overview

The repository is organized into logical components that support the complete lifecycle of a distributed timer service, from core engine implementation to client SDKs, deployment tooling, and operational utilities.

## Repository Structure

```
durable-timer/
├── docs/                    # Documentation and design documents
│   └── design/             # Architecture and design specifications
├── server/                 # Core timer service implementation (Go)
│   ├── cmd/              # Main applications and entry points
│   ├── internal/         # Private application and library code
│   │   ├── engine/       # Timer execution engine and core logic
│   │   ├── databases/    # Database adapters and implementations
│   │   ├── config/       # Configuration management and validation
│   │   ├── api/          # HTTP handlers and routing
│   │   └── models/       # Data structures and domain models
│   ├── pkg/              # Library code that's ok for external use
│   ├── integTests/       # Integration tests for server components
│   ├── go.mod            # Go module definition
│   ├── go.sum            # Go module checksums
│   └── Makefile          # Build and development commands
├── sdks/                  # Client SDKs for multiple languages
│   ├── go/               # Go SDK implementation
│   ├── java/             # Java SDK implementation
│   ├── python/           # Python SDK implementation
│   ├── ts/               # TypeScript/JavaScript SDK implementation
│   ├── rust/             # Rust SDK implementation
│   ├── csharp/           # C# SDK implementation
│   ├── php/              # PHP SDK implementation
│   └── ruby/             # Ruby SDK implementation
├── webUI/                 # Web-based management interface
├── cli/                   # Command-line interface tools (Go)
│   ├── cmd/              # CLI command definitions
│   ├── internal/         # CLI-specific internal packages
│   ├── go.mod            # Go module definition
│   ├── go.sum            # Go module checksums
│   └── Makefile          # Build commands
├── benchmark/             # Performance benchmarking tools and tests
├── docker/                # Docker configurations and multi-stage builds
├── helmcharts/            # Kubernetes Helm charts for deployment
├── operations/            # Operational scripts, dashboards, and monitoring templates
├── .github/               # GitHub Actions workflows and templates
├── api.yaml              # OpenAPI 3.0.3 specification
├── REQUIREMENTS.md       # Project requirements documentation
├── DECISION_LOG.md       # Architecture and design decision log
├── PROJECT_RULES.md      # Project development rules and guidelines
└── README.md             # Project overview and getting started guide
```

## Component Details

### `/server/` - Core Service Implementation

The server directory contains the core distributed timer service implementation, built in **Go** for performance and concurrency.

**Technology Stack**:
- **Language**: Go (Golang)
- **Architecture**: Microservice with pluggable database adapters
- **Concurrency**: Goroutines for timer execution and callback processing
- **HTTP Framework**: Standard library or lightweight framework (Gin/Echo)
- **Configuration**: Viper for configuration management

#### `/server/cmd/`
- **Purpose**: Main application entry points and executables
- **Contents**:
  - `main.go` - Primary server application
  - Command-line argument parsing
  - Application initialization and startup
  - Graceful shutdown handling

#### `/server/internal/engine/`
- **Purpose**: Timer execution engine and core business logic
- **Contents**:
  - Timer lifecycle management (create, update, delete, execute)
  - Callback execution and retry logic
  - Shard management and load balancing
  - Timer scheduling and execution coordination
  - Metrics collection and health monitoring

#### `/server/internal/databases/`
- **Purpose**: Database abstraction layer and implementations
- **Contents**:
  - Database interface definitions
  - Specific implementations for each supported database:
    - Cassandra adapter
    - MongoDB adapter  
    - TiDB adapter
    - DynamoDB adapter
    - MySQL adapter
    - PostgreSQL adapter
    - Oracle adapter
    - SQL Server adapter
  - Connection pooling and management
  - Migration scripts and schema definitions

#### `/server/internal/config/`
- **Purpose**: Configuration management and validation
- **Contents**:
  - Configuration schema definitions
  - Environment-specific configurations
  - Group and shard configuration management
  - Database connection configurations
  - Validation logic for configuration files

#### `/server/internal/api/`
- **Purpose**: HTTP API handlers and routing
- **Contents**:
  - REST API endpoint handlers
  - Request/response serialization
  - Middleware (authentication, logging, metrics)
  - OpenAPI specification validation
  - HTTP server configuration

#### `/server/internal/models/`
- **Purpose**: Data structures and domain models
- **Contents**:
  - Timer domain models
  - Request/response DTOs
  - Database entity definitions
  - Validation tags and logic
  - JSON serialization annotations

#### `/server/pkg/`
- **Purpose**: Library code that external applications can use
- **Contents**:
  - Public interfaces and contracts
  - Utility functions
  - Common constants and enums
  - Shared data structures

#### `/server/integTests/`
- **Purpose**: Integration testing for server components
- **Contents**:
  - End-to-end API tests
  - Database integration tests
  - Multi-database compatibility tests
  - Performance and load tests
  - Failure scenario and recovery tests

### `/sdks/` - Client Libraries

Multi-language SDK implementations following consistent patterns across languages.

#### Common SDK Structure (per language)
```
sdks/{language}/
├── src/                   # Source code implementation
├── tests/                 # Unit and integration tests
├── examples/              # Usage examples and samples
├── docs/                  # Language-specific documentation
└── {build-config}         # Language-specific build configuration
```

#### SDK Responsibilities
- Timer management operations (create, get, update, delete)
- Group and shard ID computation
- HTTP client with retry logic and timeout handling
- Error handling and response parsing
- Configuration management
- Callback response utilities

### `/webUI/` - Web Management Interface

- **Purpose**: Browser-based management and monitoring interface
- **Technology**: Modern frontend framework (React/Vue/Angular)
- **Features**:
  - Timer creation and management
  - Real-time timer status monitoring
  - Group and shard visualization
  - System health and metrics dashboards
  - Configuration management interface
  - Callback testing and debugging tools

### `/cli/` - Command Line Interface

- **Purpose**: CLI tools for operations and development, implemented in **Go** for consistency with the server
- **Technology**: Go (Golang) using Cobra CLI framework
- **Features**:
  - Timer CRUD operations from command line
  - Bulk timer import/export utilities
  - System administration commands
  - Configuration validation tools
  - Development and testing helpers
  - Database migration utilities

### `/benchmark/` - Performance Testing

- **Purpose**: Performance benchmarking and load testing
- **Contents**:
  - Load testing scenarios and scripts
  - Performance regression tests
  - Database-specific performance comparisons
  - Scaling characteristic measurements
  - Callback latency and throughput tests
  - Resource utilization monitoring

### `/docker/` - Containerization

- **Purpose**: Docker configurations for deployment and development
- **Contents**:
  - Multi-stage Dockerfiles for server components
  - Development environment Docker Compose files
  - Production-ready container configurations
  - Database initialization scripts
  - Health check implementations

### `/helmcharts/` - Kubernetes Deployment

- **Purpose**: Kubernetes deployment configurations
- **Contents**:
  - Helm charts for server deployment
  - ConfigMaps and Secrets management
  - Service discovery and networking configurations
  - Horizontal Pod Autoscaler (HPA) configurations
  - Database connectivity and persistence
  - Monitoring and logging configurations

### `/operations/` - Operational Scripts and Templates

- **Purpose**: Operational tooling, monitoring templates, and dashboard configurations
- **Contents**:
  - Monitoring dashboards (Grafana, DataDog, etc.)
  - Alerting rules and notification templates
  - Log aggregation configurations (ELK, Fluentd, etc.)
  - Performance monitoring scripts and utilities
  - Database maintenance and backup scripts
  - Capacity planning and scaling automation scripts
  - Troubleshooting runbooks and diagnostic tools
  - SLA monitoring and reporting templates
  - Security scanning and compliance check scripts
  - Operational metrics collection and analysis tools

### `/.github/` - CI/CD Workflows

- **Purpose**: GitHub Actions workflows for automation
- **Contents**:
  - Multi-language SDK build and test workflows
  - Server component testing and integration
  - Docker image building and publishing
  - Security scanning and vulnerability checks
  - Documentation generation and deployment
  - Release automation and version management

## Design Principles

### 1. **Separation of Concerns**
- **Server**: Core business logic isolated from client concerns
- **SDKs**: Language-specific client implementations without server dependencies
- **WebUI**: Presentation layer separate from business logic
- **Infrastructure**: Deployment and operational concerns isolated

### 2. **Multi-Language Support**
- Consistent API surface across all SDK languages
- Language-specific idioms and best practices
- Independent versioning and release cycles
- Shared test scenarios for consistency validation

### 3. **Database Agnostic**
- Pluggable database implementations
- Consistent interface across all database types
- Database-specific optimizations while maintaining compatibility
- Isolated testing for each database adapter

### 4. **Operational Excellence**
- Comprehensive monitoring and observability
- Automated testing across all components
- Infrastructure as Code for repeatable deployments
- Security scanning and vulnerability management

### 5. **Developer Experience**
- Clear separation between development and production configurations
- Comprehensive examples and documentation
- Local development environment automation
- Fast feedback loops in CI/CD pipelines

## Development Workflow

### 1. **API-First Development**
- `api.yaml` serves as the contract between server and clients
- OpenAPI specification drives both server implementation and SDK generation
- API changes require documentation updates and cross-language testing

### 2. **Independent Component Development**
- Server components can be developed and tested independently
- SDKs can be implemented in parallel once API is stable
- WebUI development can proceed with mock backends
- Database adapters can be implemented and tested in isolation

### 3. **Testing Strategy**
```
Unit Tests     → Individual component testing
Integration    → Cross-component interaction testing
End-to-End     → Full system workflow testing
Performance    → Load and stress testing
Compatibility  → Multi-database and multi-language testing
```

### 4. **Release Management**
- **Server**: Single versioned release with all database adapters
- **SDKs**: Independent versioning per language following semantic versioning
- **WebUI**: Versioned releases compatible with server API versions
- **Infrastructure**: Versioned Helm charts and Docker images

## Build and Deployment Architecture

### Local Development
```
docker-compose up     → Start all services locally
make test            → Run all tests across components
make build           → Build all components
make lint            → Code quality checks across languages
```

### CI/CD Pipeline
```
1. Code Change → GitHub Actions Trigger
2. Parallel Builds → Server + SDKs + WebUI + Infrastructure
3. Unit Tests → All components independently
4. Integration Tests → Cross-component testing
5. Security Scans → Vulnerability and compliance checks
6. Build Artifacts → Docker images, SDK packages, documentation
7. Deployment → Staging environment validation
8. Release → Production deployment with rollback capability
```

### Production Deployment
```
Kubernetes Cluster
├── Timer Service Pods (server/)
├── WebUI Service (webUI/)
├── Database (external or in-cluster)
├── Monitoring (Prometheus, Grafana)
└── Ingress (Load balancer, TLS termination)
```

## Configuration Management

### Environment-Specific Configurations
- **Development**: Local database connections, debug logging, hot reload
- **Testing**: In-memory databases, mock services, comprehensive logging
- **Staging**: Production-like environment, real databases, performance monitoring
- **Production**: Optimized configurations, security hardening, operational monitoring

### Configuration Hierarchy
```
1. Default configurations (server/config/defaults)
2. Environment-specific overrides (server/config/{env})
3. Deployment-specific settings (helmcharts/values-{env}.yaml)
4. Runtime environment variables (Kubernetes ConfigMaps/Secrets)
```

## Security Considerations

### Code Security
- **Dependency Scanning**: Automated vulnerability detection across all languages
- **Static Analysis**: Code quality and security issue detection
- **Secret Management**: No secrets in repository, proper secret rotation

### Deployment Security
- **Container Security**: Non-root containers, minimal base images
- **Network Security**: Service mesh, TLS encryption, network policies
- **Access Control**: RBAC, service accounts, least privilege principles

## Monitoring and Observability

All monitoring configurations, dashboards, and operational templates are maintained in the `/operations/` directory for easy deployment and customization.

### Metrics Collection
- **Server Metrics**: Timer execution rates, callback success/failure, database performance
- **SDK Metrics**: Client-side latency, error rates, retry patterns
- **Infrastructure Metrics**: CPU, memory, network, disk utilization

### Logging Strategy
- **Structured Logging**: JSON format for consistent parsing
- **Correlation IDs**: Request tracing across components
- **Log Aggregation**: Centralized logging with searchable indexes

### Health Monitoring
- **Health Checks**: Kubernetes readiness and liveness probes
- **Dependency Health**: Database connectivity, external service availability
- **SLA Monitoring**: Timer execution SLA compliance tracking

---

This repository layout design ensures scalability, maintainability, and operational excellence while supporting the complex requirements of a distributed timer service across multiple languages, databases, and deployment environments. 