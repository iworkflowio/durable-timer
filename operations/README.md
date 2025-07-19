# Operations

Operational scripts, monitoring templates, and dashboard configurations for the distributed timer service.

## Overview

This directory contains comprehensive operational tooling for:
- Monitoring dashboards and alerting rules
- Log aggregation and analysis configurations
- Performance monitoring and capacity planning
- Database maintenance and backup scripts
- Troubleshooting runbooks and diagnostic tools
- Security scanning and compliance automation
- SLA monitoring and reporting templates

## Directory Structure

```
operations/
├── monitoring/              # Monitoring configurations and dashboards
│   ├── grafana/            # Grafana dashboards and datasources
│   ├── prometheus/         # Prometheus rules and configuration
│   ├── alerts/             # Alerting rules and notification configs
│   └── datadog/            # DataDog dashboards and monitors
├── logging/                # Log aggregation and analysis
│   ├── elasticsearch/      # ELK stack configurations
│   ├── fluentd/           # Fluentd configuration files
│   ├── logstash/          # Logstash pipeline configurations
│   └── loki/              # Loki configuration and queries
├── scripts/               # Operational automation scripts
│   ├── backup/            # Database backup and restore scripts
│   ├── maintenance/       # Regular maintenance tasks
│   ├── deployment/        # Deployment automation scripts
│   ├── scaling/           # Auto-scaling and capacity management
│   └── diagnostics/       # Troubleshooting and diagnostic tools
├── runbooks/              # Operational procedures and guides
│   ├── incidents/         # Incident response procedures
│   ├── maintenance/       # Planned maintenance procedures
│   ├── troubleshooting/   # Common issue resolution guides
│   └── deployment/        # Deployment procedures and rollback
├── security/              # Security monitoring and compliance
│   ├── scanning/          # Vulnerability scanning configurations
│   ├── compliance/        # Compliance check scripts
│   ├── audit/             # Audit logging and analysis
│   └── policies/          # Security policies and configurations
├── sla/                   # SLA monitoring and reporting
│   ├── dashboards/        # SLA monitoring dashboards
│   ├── reports/           # Automated SLA reporting
│   └── thresholds/        # SLA threshold configurations
└── README.md              # This file
```

## Monitoring and Alerting

### Grafana Dashboards

#### Timer Service Overview Dashboard
```json
{
  "dashboard": {
    "title": "Timer Service - Overview",
    "tags": ["timer-service", "overview"],
    "panels": [
      {
        "title": "Timer Creation Rate",
        "type": "stat",
        "targets": [
          {
            "expr": "rate(timer_creations_total[5m])",
            "legendFormat": "Creations/sec"
          }
        ]
      },
      {
        "title": "Timer Execution Success Rate",
        "type": "stat",
        "targets": [
          {
            "expr": "rate(timer_executions_success_total[5m]) / rate(timer_executions_total[5m]) * 100",
            "legendFormat": "Success %"
          }
        ]
      },
      {
        "title": "Active Timers by Group",
        "type": "piechart",
        "targets": [
          {
            "expr": "sum by (group_id) (active_timers_total)",
            "legendFormat": "{{group_id}}"
          }
        ]
      }
    ]
  }
}
```

#### Database Performance Dashboard
```json
{
  "dashboard": {
    "title": "Timer Service - Database Performance", 
    "panels": [
      {
        "title": "Database Connection Pool",
        "targets": [
          {
            "expr": "database_connections_active",
            "legendFormat": "Active Connections"
          },
          {
            "expr": "database_connections_idle", 
            "legendFormat": "Idle Connections"
          }
        ]
      },
      {
        "title": "Query Performance",
        "targets": [
          {
            "expr": "histogram_quantile(0.95, rate(database_query_duration_seconds_bucket[5m]))",
            "legendFormat": "95th percentile"
          }
        ]
      }
    ]
  }
}
```

### Prometheus Alerting Rules

```yaml
# monitoring/prometheus/alerts.yml
groups:
  - name: timer-service
    rules:
      - alert: HighTimerCreationFailureRate
        expr: rate(timer_creation_failures_total[5m]) / rate(timer_creation_attempts_total[5m]) > 0.05
        for: 2m
        labels:
          severity: warning
          service: timer-service
        annotations:
          summary: "High timer creation failure rate"
          description: "Timer creation failure rate is {{ $value | humanizePercentage }} for the last 5 minutes"

      - alert: TimerExecutionBacklog
        expr: timer_execution_queue_size > 1000
        for: 5m
        labels:
          severity: critical
          service: timer-service
        annotations:
          summary: "Timer execution backlog is high"
          description: "Timer execution queue size is {{ $value }} timers"

      - alert: DatabaseConnectionPoolExhaustion
        expr: database_connections_active / database_connections_max > 0.9
        for: 1m
        labels:
          severity: critical
          service: timer-service
        annotations:
          summary: "Database connection pool near exhaustion"
          description: "Database connection pool usage is {{ $value | humanizePercentage }}"

      - alert: CallbackFailureRateHigh
        expr: rate(callback_failures_total[5m]) / rate(callback_attempts_total[5m]) > 0.1
        for: 3m
        labels:
          severity: warning
          service: timer-service
        annotations:
          summary: "High callback failure rate"
          description: "Callback failure rate is {{ $value | humanizePercentage }}"
```

### DataDog Monitors

```yaml
# monitoring/datadog/monitors.yml
monitors:
  - name: "Timer Service - High Error Rate"
    type: "metric alert"
    query: "avg(last_5m):rate(timer_service.errors.total{*}) > 0.05"
    message: "Timer service error rate is above 5%"
    tags:
      - "service:timer-service"
      - "env:production"
    
  - name: "Timer Service - Memory Usage High"
    type: "metric alert" 
    query: "avg(last_5m):system.mem.pct_usable{service:timer-service} < 0.1"
    message: "Timer service memory usage is above 90%"
    
  - name: "Timer Service - Response Time High"
    type: "metric alert"
    query: "avg(last_5m):timer_service.response_time.95percentile > 1000"
    message: "Timer service 95th percentile response time is above 1 second"
```

## Log Management

### ELK Stack Configuration

#### Elasticsearch Index Template
```json
{
  "index_patterns": ["timer-service-*"],
  "template": {
    "settings": {
      "number_of_shards": 3,
      "number_of_replicas": 1,
      "index": {
        "lifecycle": {
          "name": "timer-service-policy",
          "rollover_alias": "timer-service"
        }
      }
    },
    "mappings": {
      "properties": {
        "@timestamp": {"type": "date"},
        "level": {"type": "keyword"},
        "service": {"type": "keyword"},
        "group_id": {"type": "keyword"},
        "timer_id": {"type": "keyword"},
        "operation": {"type": "keyword"},
        "duration_ms": {"type": "long"},
        "error": {"type": "text"}
      }
    }
  }
}
```

#### Logstash Pipeline
```ruby
# logging/logstash/timer-service.conf
input {
  beats {
    port => 5044
  }
}

filter {
  if [fields][service] == "timer-service" {
    json {
      source => "message"
    }
    
    date {
      match => [ "timestamp", "ISO8601" ]
    }
    
    if [timer_id] {
      mutate {
        add_field => { "shard_id" => "%{[timer_id]}" }
      }
      ruby {
        code => "
          timer_id = event.get('timer_id')
          group_id = event.get('group_id')
          shard_id = timer_id.hash % 32  # Assuming 32 shards
          event.set('shard_id', shard_id)
        "
      }
    }
  }
}

output {
  elasticsearch {
    hosts => ["elasticsearch:9200"]
    index => "timer-service-%{+YYYY.MM.dd}"
  }
}
```

### Fluentd Configuration

```ruby
# logging/fluentd/fluent.conf
<source>
  @type forward
  port 24224
  bind 0.0.0.0
</source>

<filter timer-service.**>
  @type parser
  key_name message
  reserve_data true
  <parse>
    @type json
  </parse>
</filter>

<filter timer-service.**>
  @type record_transformer
  <record>
    shard_id ${record['timer_id'] ? record['timer_id'].hash % 32 : nil}
    environment #{ENV['ENVIRONMENT'] || 'development'}
  </record>
</filter>

<match timer-service.**>
  @type elasticsearch
  host elasticsearch
  port 9200
  index_name timer-service
  type_name _doc
  logstash_format true
  logstash_prefix timer-service
  logstash_dateformat %Y.%m.%d
</match>
```

## Operational Scripts

### Database Backup and Restore

```bash
#!/bin/bash
# scripts/backup/backup-database.sh

set -e

# Configuration
DB_HOST=${DB_HOST:-localhost}
DB_PORT=${DB_PORT:-5432}
DB_NAME=${DB_NAME:-timer_service}
DB_USER=${DB_USER:-timer_user}
BACKUP_DIR=${BACKUP_DIR:-/var/backups/timer-service}
RETENTION_DAYS=${RETENTION_DAYS:-30}

# Create backup directory
mkdir -p "$BACKUP_DIR"

# Generate backup filename
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
BACKUP_FILE="$BACKUP_DIR/timer_service_$TIMESTAMP.sql"

echo "Starting database backup..."
echo "Database: $DB_HOST:$DB_PORT/$DB_NAME"
echo "Backup file: $BACKUP_FILE"

# Create backup
pg_dump -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" \
  --verbose --clean --no-owner --no-privileges \
  > "$BACKUP_FILE"

# Compress backup
gzip "$BACKUP_FILE"
BACKUP_FILE="$BACKUP_FILE.gz"

echo "Backup completed: $BACKUP_FILE"

# Upload to S3 (optional)
if [ -n "$S3_BUCKET" ]; then
    aws s3 cp "$BACKUP_FILE" "s3://$S3_BUCKET/backups/timer-service/"
    echo "Backup uploaded to S3"
fi

# Cleanup old backups
find "$BACKUP_DIR" -name "timer_service_*.sql.gz" -mtime +$RETENTION_DAYS -delete
echo "Cleaned up backups older than $RETENTION_DAYS days"

echo "Backup process completed successfully"
```

### Database Restore Script

```bash
#!/bin/bash
# scripts/backup/restore-database.sh

set -e

if [ $# -ne 1 ]; then
    echo "Usage: $0 <backup_file>"
    exit 1
fi

BACKUP_FILE="$1"

# Configuration
DB_HOST=${DB_HOST:-localhost}
DB_PORT=${DB_PORT:-5432}
DB_NAME=${DB_NAME:-timer_service}
DB_USER=${DB_USER:-timer_user}

echo "Restoring database from: $BACKUP_FILE"
echo "Target database: $DB_HOST:$DB_PORT/$DB_NAME"

# Confirm restore
read -p "This will overwrite the existing database. Continue? (y/N) " -n 1 -r
echo
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    echo "Restore cancelled"
    exit 1
fi

# Stop timer service to prevent conflicts
echo "Stopping timer service..."
docker-compose stop timer-server || true

# Restore database
if [[ "$BACKUP_FILE" == *.gz ]]; then
    gunzip -c "$BACKUP_FILE" | psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME"
else
    psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" < "$BACKUP_FILE"
fi

echo "Database restore completed"

# Restart timer service
echo "Starting timer service..."
docker-compose start timer-server

echo "Restore process completed successfully"
```

### Maintenance Scripts

```bash
#!/bin/bash
# scripts/maintenance/cleanup-expired-timers.sh

set -e

# Configuration
DB_HOST=${DB_HOST:-localhost}
DB_PORT=${DB_PORT:-5432}
DB_NAME=${DB_NAME:-timer_service}
DB_USER=${DB_USER:-timer_user}
CLEANUP_BATCH_SIZE=${CLEANUP_BATCH_SIZE:-1000}
RETENTION_DAYS=${RETENTION_DAYS:-90}

echo "Starting expired timer cleanup..."
echo "Retention period: $RETENTION_DAYS days"
echo "Batch size: $CLEANUP_BATCH_SIZE"

# Calculate cutoff date
CUTOFF_DATE=$(date -d "$RETENTION_DAYS days ago" '+%Y-%m-%d %H:%M:%S')

echo "Cutoff date: $CUTOFF_DATE"

# Count timers to be deleted
TOTAL_COUNT=$(psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" -t -c \
  "SELECT COUNT(*) FROM timer_service.timers WHERE executed_at IS NOT NULL AND executed_at < '$CUTOFF_DATE';")

echo "Timers to be deleted: $TOTAL_COUNT"

if [ "$TOTAL_COUNT" -eq 0 ]; then
    echo "No expired timers to cleanup"
    exit 0
fi

# Delete in batches to avoid long-running transactions
DELETED_TOTAL=0
while [ $DELETED_TOTAL -lt $TOTAL_COUNT ]; do
    DELETED_BATCH=$(psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" -t -c \
      "WITH deleted AS (
         DELETE FROM timer_service.timers 
         WHERE ctid IN (
           SELECT ctid FROM timer_service.timers 
           WHERE executed_at IS NOT NULL AND executed_at < '$CUTOFF_DATE'
           LIMIT $CLEANUP_BATCH_SIZE
         )
         RETURNING 1
       ) SELECT COUNT(*) FROM deleted;")
    
    DELETED_TOTAL=$((DELETED_TOTAL + DELETED_BATCH))
    echo "Deleted $DELETED_BATCH timers (total: $DELETED_TOTAL/$TOTAL_COUNT)"
    
    if [ "$DELETED_BATCH" -eq 0 ]; then
        break
    fi
    
    # Brief pause between batches
    sleep 1
done

echo "Cleanup completed. Deleted $DELETED_TOTAL expired timers"

# Update statistics
psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" -c \
  "ANALYZE timer_service.timers;"

echo "Database statistics updated"
```

### Scaling Automation

```bash
#!/bin/bash
# scripts/scaling/auto-scale.sh

set -e

# Configuration
NAMESPACE=${NAMESPACE:-default}
DEPLOYMENT=${DEPLOYMENT:-timer-server}
MIN_REPLICAS=${MIN_REPLICAS:-2}
MAX_REPLICAS=${MAX_REPLICAS:-10}
CPU_THRESHOLD=${CPU_THRESHOLD:-70}
MEMORY_THRESHOLD=${MEMORY_THRESHOLD:-80}

echo "Auto-scaling check for $DEPLOYMENT in namespace $NAMESPACE"

# Get current metrics
CURRENT_CPU=$(kubectl top pods -n "$NAMESPACE" -l app="$DEPLOYMENT" --no-headers | \
  awk '{sum+=$2} END {print int(sum/NR)}' | sed 's/m//')

CURRENT_MEMORY=$(kubectl top pods -n "$NAMESPACE" -l app="$DEPLOYMENT" --no-headers | \
  awk '{sum+=$3} END {print int(sum/NR)}' | sed 's/Mi//')

CURRENT_REPLICAS=$(kubectl get deployment "$DEPLOYMENT" -n "$NAMESPACE" -o jsonpath='{.spec.replicas}')

echo "Current metrics:"
echo "  CPU: ${CURRENT_CPU}m"
echo "  Memory: ${CURRENT_MEMORY}Mi"
echo "  Replicas: $CURRENT_REPLICAS"

# Check queue depth
QUEUE_DEPTH=$(curl -s http://timer-server-service:8080/metrics | \
  grep timer_execution_queue_size | awk '{print $2}')

echo "  Queue depth: $QUEUE_DEPTH"

# Scaling decision logic
NEW_REPLICAS=$CURRENT_REPLICAS

# Scale up conditions
if [ "$CURRENT_CPU" -gt "$CPU_THRESHOLD" ] || \
   [ "$CURRENT_MEMORY" -gt "$MEMORY_THRESHOLD" ] || \
   [ "$QUEUE_DEPTH" -gt 500 ]; then
    
    if [ "$CURRENT_REPLICAS" -lt "$MAX_REPLICAS" ]; then
        NEW_REPLICAS=$((CURRENT_REPLICAS + 1))
        echo "Scaling UP to $NEW_REPLICAS replicas"
    fi

# Scale down conditions  
elif [ "$CURRENT_CPU" -lt 30 ] && \
     [ "$CURRENT_MEMORY" -lt 40 ] && \
     [ "$QUEUE_DEPTH" -lt 100 ]; then
    
    if [ "$CURRENT_REPLICAS" -gt "$MIN_REPLICAS" ]; then
        NEW_REPLICAS=$((CURRENT_REPLICAS - 1))
        echo "Scaling DOWN to $NEW_REPLICAS replicas"
    fi
fi

# Apply scaling if needed
if [ "$NEW_REPLICAS" -ne "$CURRENT_REPLICAS" ]; then
    kubectl scale deployment "$DEPLOYMENT" -n "$NAMESPACE" --replicas="$NEW_REPLICAS"
    echo "Scaling command executed: $CURRENT_REPLICAS -> $NEW_REPLICAS"
else
    echo "No scaling action required"
fi
```

## Troubleshooting and Diagnostics

### Health Check Script

```bash
#!/bin/bash
# scripts/diagnostics/health-check.sh

set -e

echo "=== Timer Service Health Check ==="
echo "Timestamp: $(date)"
echo

# Service availability
echo "1. Service Availability"
if curl -f -s http://localhost:8080/health > /dev/null; then
    echo "✓ Timer service is responding"
else
    echo "✗ Timer service is not responding"
fi

# Database connectivity
echo
echo "2. Database Connectivity"
if curl -f -s http://localhost:8080/health/db > /dev/null; then
    echo "✓ Database is accessible"
else
    echo "✗ Database connection failed"
fi

# Memory usage
echo
echo "3. Memory Usage"
MEMORY_USAGE=$(ps -o pid,ppid,cmd,%mem,%cpu --sort=-%mem -C timer-server | tail -n +2)
if [ -n "$MEMORY_USAGE" ]; then
    echo "$MEMORY_USAGE"
else
    echo "✗ Timer server process not found"
fi

# Disk space
echo
echo "4. Disk Space"
df -h | grep -E "(Filesystem|/var/lib/docker|/tmp|/$)"

# Active timers count
echo
echo "5. Active Timers"
ACTIVE_TIMERS=$(curl -s http://localhost:8080/metrics | grep active_timers_total | awk '{print $2}')
echo "Active timers: ${ACTIVE_TIMERS:-unknown}"

# Recent errors
echo
echo "6. Recent Errors (last 5 minutes)"
docker logs timer-server --since=5m | grep -i error | tail -5

echo
echo "=== Health Check Complete ==="
```

### Performance Diagnostic

```bash
#!/bin/bash
# scripts/diagnostics/performance-check.sh

echo "=== Timer Service Performance Diagnostics ==="
echo "Timestamp: $(date)"
echo

# Response time test
echo "1. API Response Time Test"
for i in {1..5}; do
    TIME=$(curl -w "@curl-format.txt" -o /dev/null -s http://localhost:8080/health)
    echo "Request $i: $TIME"
done

# Database query performance
echo
echo "2. Database Query Performance"
psql -h localhost -U timer_user -d timer_service -c "
EXPLAIN ANALYZE 
SELECT COUNT(*) FROM timer_service.timers 
WHERE execute_at <= NOW();"

# Active connections
echo
echo "3. Database Connections"
psql -h localhost -U timer_user -d timer_service -c "
SELECT state, COUNT(*) 
FROM pg_stat_activity 
WHERE datname = 'timer_service' 
GROUP BY state;"

# Queue metrics
echo
echo "4. Timer Queue Metrics"
curl -s http://localhost:8080/metrics | grep -E "(queue_size|execution_rate|creation_rate)"

echo
echo "=== Performance Diagnostics Complete ==="
```

## SLA Monitoring

### SLA Threshold Configuration

```yaml
# sla/thresholds/production.yml
sla_thresholds:
  availability:
    target: 99.9  # 99.9% uptime
    measurement_window: "30d"
    
  response_time:
    api_p95: 500ms    # 95th percentile API response time
    api_p99: 1000ms   # 99th percentile API response time
    
  timer_execution:
    accuracy: 99.5    # 99.5% of timers execute within SLA
    on_time_window: 30s  # Execute within 30 seconds of scheduled time
    
  error_rate:
    api_errors: 0.1   # Less than 0.1% API error rate
    callback_failures: 5.0  # Less than 5% callback failure rate
```

### SLA Reporting

```python
#!/usr/bin/env python3
# sla/reports/generate-sla-report.py

import requests
import json
from datetime import datetime, timedelta

def generate_sla_report():
    """Generate SLA compliance report"""
    
    # Time range (last 30 days)
    end_time = datetime.now()
    start_time = end_time - timedelta(days=30)
    
    report = {
        "period": {
            "start": start_time.isoformat(),
            "end": end_time.isoformat()
        },
        "metrics": {}
    }
    
    # Availability metric
    uptime_query = f"avg_over_time(up{{job='timer-service'}}[30d])"
    availability = query_prometheus(uptime_query)
    report["metrics"]["availability"] = {
        "value": availability * 100,
        "target": 99.9,
        "status": "PASS" if availability >= 0.999 else "FAIL"
    }
    
    # Response time metrics
    p95_query = f"histogram_quantile(0.95, rate(http_request_duration_seconds_bucket[30d]))"
    p95_latency = query_prometheus(p95_query) * 1000  # Convert to ms
    
    report["metrics"]["response_time_p95"] = {
        "value": p95_latency,
        "target": 500,
        "status": "PASS" if p95_latency <= 500 else "FAIL"
    }
    
    # Error rate
    error_rate_query = f"rate(http_requests_total{{status=~'5..}}[30d]) / rate(http_requests_total[30d])"
    error_rate = query_prometheus(error_rate_query)
    
    report["metrics"]["error_rate"] = {
        "value": error_rate * 100,
        "target": 0.1,
        "status": "PASS" if error_rate <= 0.001 else "FAIL"
    }
    
    # Overall SLA status
    all_pass = all(m["status"] == "PASS" for m in report["metrics"].values())
    report["overall_status"] = "PASS" if all_pass else "FAIL"
    
    return report

def query_prometheus(query):
    """Query Prometheus for metrics"""
    response = requests.get(f"http://prometheus:9090/api/v1/query", 
                          params={"query": query})
    data = response.json()
    if data["status"] == "success" and data["data"]["result"]:
        return float(data["data"]["result"][0]["value"][1])
    return 0

if __name__ == "__main__":
    report = generate_sla_report()
    print(json.dumps(report, indent=2))
```

This comprehensive operations directory provides all the necessary tools and configurations for effectively monitoring, maintaining, and troubleshooting the distributed timer service in production environments. 