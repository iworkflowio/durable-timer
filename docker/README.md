# Docker Configurations

Docker containerization configurations for the distributed timer service, supporting development, testing, and production deployments.

## Overview

This directory contains:
- Multi-stage Dockerfiles for server components
- Development environment Docker Compose files
- Production-ready container configurations
- Database initialization scripts
- Health check implementations
- Container optimization and security configurations

## Container Images

### Timer Service Server
- **Base Image**: `golang:1.21-alpine` (build) + `alpine:latest` (runtime)
- **Size**: ~15MB (optimized)
- **Security**: Non-root user, minimal attack surface
- **Features**: Health checks, graceful shutdown, metrics endpoint

### Web UI
- **Base Image**: `node:18-alpine` (build) + `nginx:alpine` (runtime)
- **Size**: ~25MB (optimized)
- **Security**: Security headers, CSP policies
- **Features**: SPA routing, environment configuration

### CLI Tools
- **Base Image**: `golang:1.21-alpine` (build) + `alpine:latest` (runtime)
- **Size**: ~10MB (minimal)
- **Features**: All CLI commands, configuration support

## Directory Structure

```
docker/
├── Dockerfile.server        # Multi-stage server build
├── Dockerfile.webui         # Multi-stage web UI build
├── Dockerfile.cli           # CLI tools container
├── docker-compose.yml       # Development environment
├── docker-compose.prod.yml  # Production configuration
├── docker-compose.test.yml  # Testing environment
├── nginx/                   # Nginx configurations
│   ├── nginx.conf          # Main nginx config
│   └── default.conf        # Default site config
├── scripts/                # Container scripts
│   ├── entrypoint.sh       # Container entrypoint
│   ├── healthcheck.sh      # Health check script
│   └── init-db.sh          # Database initialization
├── security/               # Security configurations
│   ├── security.conf       # Security headers
│   └── ssl.conf           # SSL/TLS configuration
└── README.md              # This file
```

## Quick Start

### Development Environment
```bash
# Start all services
docker-compose up -d

# View logs
docker-compose logs -f

# Stop services
docker-compose down

# Rebuild and restart
docker-compose up --build -d
```

### Production Deployment
```bash
# Build production images
docker-compose -f docker-compose.prod.yml build

# Deploy to production
docker-compose -f docker-compose.prod.yml up -d

# Check health
docker-compose -f docker-compose.prod.yml ps
```

### Testing Environment
```bash
# Run integration tests
docker-compose -f docker-compose.test.yml up --abort-on-container-exit

# Cleanup test environment
docker-compose -f docker-compose.test.yml down -v
```

## Dockerfiles

### Server Container
```dockerfile
# docker/Dockerfile.server
FROM golang:1.21-alpine AS builder

# Install dependencies
RUN apk add --no-cache git ca-certificates tzdata

# Set working directory
WORKDIR /app

# Copy go mod files
COPY server/go.mod server/go.sum ./
RUN go mod download

# Copy source code
COPY server/ .

# Build binary
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-w -s" \
    -o timer-server \
    ./cmd

# Production stage
FROM alpine:latest

# Install ca-certificates for HTTPS calls
RUN apk --no-cache add ca-certificates tzdata

# Create non-root user
RUN addgroup -g 1000 timer && \
    adduser -D -s /bin/sh -u 1000 -G timer timer

# Set working directory
WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/timer-server .
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy scripts
COPY docker/scripts/entrypoint.sh ./
COPY docker/scripts/healthcheck.sh ./
RUN chmod +x entrypoint.sh healthcheck.sh

# Switch to non-root user
USER timer

# Expose port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD ./healthcheck.sh

# Set entrypoint
ENTRYPOINT ["./entrypoint.sh"]
CMD ["./timer-server"]
```

### Web UI Container
```dockerfile
# docker/Dockerfile.webui
FROM node:18-alpine AS builder

# Set working directory
WORKDIR /app

# Copy package files
COPY webUI/package*.json ./
RUN npm ci --only=production

# Copy source code
COPY webUI/ .

# Build application
RUN npm run build

# Production stage
FROM nginx:alpine

# Copy built application
COPY --from=builder /app/dist /usr/share/nginx/html

# Copy nginx configuration
COPY docker/nginx/nginx.conf /etc/nginx/nginx.conf
COPY docker/nginx/default.conf /etc/nginx/conf.d/default.conf
COPY docker/security/security.conf /etc/nginx/conf.d/security.conf

# Create non-root user
RUN addgroup -g 1000 nginx-user && \
    adduser -D -s /bin/sh -u 1000 -G nginx-user nginx-user

# Set permissions
RUN chown -R nginx-user:nginx-user /usr/share/nginx/html && \
    chown -R nginx-user:nginx-user /var/cache/nginx && \
    chown -R nginx-user:nginx-user /var/log/nginx && \
    chown -R nginx-user:nginx-user /etc/nginx/conf.d

# Switch to non-root user
USER nginx-user

# Expose port
EXPOSE 80

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD curl -f http://localhost/ || exit 1

# Start nginx
CMD ["nginx", "-g", "daemon off;"]
```

## Docker Compose Configurations

### Development Environment
```yaml
# docker-compose.yml
version: '3.8'

services:
  # Timer Service
  timer-server:
    build:
      context: .
      dockerfile: docker/Dockerfile.server
    ports:
      - "8080:8080"
    environment:
      - LOG_LEVEL=debug
      - DB_HOST=postgres
      - DB_USER=timer_user
      - DB_PASSWORD=timer_pass
      - DB_NAME=timer_db
    depends_on:
      - postgres
    volumes:
      - ./server/config:/app/config:ro
    networks:
      - timer-network

  # Web UI
  webui:
    build:
      context: .
      dockerfile: docker/Dockerfile.webui
    ports:
      - "3000:80"
    environment:
      - REACT_APP_API_URL=http://localhost:8080
    depends_on:
      - timer-server
    networks:
      - timer-network

  # PostgreSQL Database
  postgres:
    image: postgres:15-alpine
    environment:
      - POSTGRES_USER=timer_user
      - POSTGRES_PASSWORD=timer_pass
      - POSTGRES_DB=timer_db
    ports:
      - "5432:5432"
    volumes:
      - postgres_data:/var/lib/postgresql/data
      - ./docker/scripts/init-db.sh:/docker-entrypoint-initdb.d/init-db.sh
    networks:
      - timer-network

  # Redis (optional)
  redis:
    image: redis:7-alpine
    ports:
      - "6379:6379"
    volumes:
      - redis_data:/data
    networks:
      - timer-network

volumes:
  postgres_data:
  redis_data:

networks:
  timer-network:
    driver: bridge
```

### Production Configuration
```yaml
# docker-compose.prod.yml
version: '3.8'

services:
  timer-server:
    build:
      context: .
      dockerfile: docker/Dockerfile.server
    restart: unless-stopped
    environment:
      - LOG_LEVEL=info
      - DB_HOST=${DB_HOST}
      - DB_USER=${DB_USER}
      - DB_PASSWORD=${DB_PASSWORD}
      - DB_NAME=${DB_NAME}
    ports:
      - "8080:8080"
    deploy:
      replicas: 2
      resources:
        limits:
          memory: 512M
          cpus: '0.5'
        reservations:
          memory: 256M
          cpus: '0.25'
    healthcheck:
      test: ["CMD", "./healthcheck.sh"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 40s
    networks:
      - timer-network

  webui:
    build:
      context: .
      dockerfile: docker/Dockerfile.webui
    restart: unless-stopped
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - ./docker/security/ssl.conf:/etc/nginx/conf.d/ssl.conf:ro
      - /etc/letsencrypt:/etc/letsencrypt:ro
    deploy:
      resources:
        limits:
          memory: 128M
          cpus: '0.1'
    networks:
      - timer-network

networks:
  timer-network:
    driver: overlay
    attachable: true
```

## Health Checks

### Server Health Check
```bash
#!/bin/sh
# docker/scripts/healthcheck.sh

# Check if server is responding
curl -f http://localhost:8080/health || exit 1

# Check database connectivity
curl -f http://localhost:8080/health/db || exit 1

echo "Health check passed"
```

### Database Health Check
```bash
#!/bin/sh
# docker/scripts/db-healthcheck.sh

# Wait for database to be ready
until pg_isready -h "$DB_HOST" -U "$DB_USER"; do
  echo "Waiting for database..."
  sleep 2
done

echo "Database is ready"
```

## Container Scripts

### Entrypoint Script
```bash
#!/bin/sh
# docker/scripts/entrypoint.sh

set -e

# Wait for dependencies
if [ -n "$DB_HOST" ]; then
    echo "Waiting for database..."
    until nc -z "$DB_HOST" "${DB_PORT:-5432}"; do
        sleep 1
    done
    echo "Database is available"
fi

# Run database migrations
if [ "$RUN_MIGRATIONS" = "true" ]; then
    echo "Running database migrations..."
    ./timer-server migrate up
fi

# Execute the main command
exec "$@"
```

### Database Initialization
```sql
-- docker/scripts/init-db.sh
#!/bin/bash
set -e

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
    -- Create timer service schema
    CREATE SCHEMA IF NOT EXISTS timer_service;
    
    -- Create timer table
    CREATE TABLE IF NOT EXISTS timer_service.timers (
        shard_id INTEGER NOT NULL,
        timer_id VARCHAR(255) NOT NULL,
        group_id VARCHAR(255) NOT NULL,
        execute_at TIMESTAMP(3) NOT NULL,
        callback_url VARCHAR(2048) NOT NULL,
        payload JSONB,
        retry_policy JSONB,
        callback_timeout VARCHAR(32) DEFAULT '30s',
        created_at TIMESTAMP(3) NOT NULL DEFAULT NOW(),
        updated_at TIMESTAMP(3) NOT NULL DEFAULT NOW(),
        executed_at TIMESTAMP(3),
        PRIMARY KEY (shard_id, timer_id)
    );
    
    -- Create indexes
    CREATE INDEX IF NOT EXISTS idx_timers_execute_at 
    ON timer_service.timers (shard_id, execute_at);
    
    -- Grant permissions
    GRANT ALL PRIVILEGES ON SCHEMA timer_service TO $POSTGRES_USER;
    GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA timer_service TO $POSTGRES_USER;
EOSQL
```

## Security Configuration

### Nginx Security Headers
```nginx
# docker/security/security.conf
add_header X-Frame-Options "SAMEORIGIN" always;
add_header X-Content-Type-Options "nosniff" always;
add_header X-XSS-Protection "1; mode=block" always;
add_header Referrer-Policy "strict-origin-when-cross-origin" always;
add_header Content-Security-Policy "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; font-src 'self'; connect-src 'self' http://localhost:8080; frame-ancestors 'none';" always;

# Remove server tokens
server_tokens off;

# Limit request size
client_max_body_size 10M;

# Rate limiting
limit_req_zone $binary_remote_addr zone=api:10m rate=10r/s;
limit_req zone=api burst=20 nodelay;
```

### SSL Configuration
```nginx
# docker/security/ssl.conf
ssl_protocols TLSv1.2 TLSv1.3;
ssl_ciphers ECDHE-RSA-AES128-GCM-SHA256:ECDHE-RSA-AES256-GCM-SHA384;
ssl_prefer_server_ciphers off;
ssl_session_cache shared:SSL:10m;
ssl_session_timeout 10m;

# HSTS
add_header Strict-Transport-Security "max-age=31536000; includeSubDomains" always;
```

## Development Utilities

### Container Shell Access
```bash
# Access server container
docker exec -it timer-server sh

# Access database
docker exec -it timer-postgres psql -U timer_user -d timer_db

# View logs
docker logs -f timer-server
docker logs -f timer-webui
```

### Database Operations
```bash
# Backup database
docker exec timer-postgres pg_dump -U timer_user timer_db > backup.sql

# Restore database
docker exec -i timer-postgres psql -U timer_user -d timer_db < backup.sql

# Reset database
docker-compose down -v
docker-compose up -d postgres
```

### Image Management
```bash
# Build specific service
docker-compose build timer-server

# Pull latest images
docker-compose pull

# Remove unused images
docker image prune -f

# Check image sizes
docker images | grep timer
```

## Production Deployment

### Environment Variables
```bash
# .env.production
DB_HOST=timer-db-cluster.example.com
DB_USER=timer_prod_user
DB_PASSWORD=secure_password_here
DB_NAME=timer_production
LOG_LEVEL=info
METRICS_ENABLED=true
AUTH_SECRET=jwt_secret_key_here
```

### Resource Limits
```yaml
# Resource constraints for production
deploy:
  resources:
    limits:
      memory: 1G
      cpus: '1.0'
    reservations:
      memory: 512M
      cpus: '0.5'
```

### Monitoring Integration
```yaml
# Add monitoring services
  prometheus:
    image: prom/prometheus:latest
    ports:
      - "9090:9090"
    volumes:
      - ./monitoring/prometheus.yml:/etc/prometheus/prometheus.yml

  grafana:
    image: grafana/grafana:latest
    ports:
      - "3001:3000"
    environment:
      - GF_SECURITY_ADMIN_PASSWORD=admin123
``` 