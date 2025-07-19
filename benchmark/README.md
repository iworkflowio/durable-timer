# Timer Service Benchmarks

Performance benchmarking and load testing tools for the distributed timer service.

## Overview

This directory contains comprehensive benchmarking tools for:
- Performance regression testing
- Load testing and stress testing
- Database-specific performance comparisons
- Scaling characteristic measurements
- Callback latency and throughput analysis
- Resource utilization monitoring

## Benchmark Types

### 1. **Load Testing**
- **Timer Creation Rate**: Measure maximum timer creation throughput
- **Timer Execution Rate**: Test timer execution performance under load
- **Callback Latency**: Measure end-to-end callback execution time
- **Concurrent Operations**: Test multiple operations simultaneously

### 2. **Database Performance**
- **Write Performance**: Timer creation and update benchmarks
- **Read Performance**: Timer retrieval and query benchmarks
- **Query Optimization**: Compare different database query patterns
- **Connection Pool**: Test database connection efficiency

### 3. **Scaling Benchmarks**
- **Horizontal Scaling**: Performance across multiple instances
- **Vertical Scaling**: Resource scaling characteristics
- **Shard Distribution**: Load balancing effectiveness
- **Memory Usage**: Memory consumption under load

### 4. **Stress Testing**
- **Resource Exhaustion**: Test behavior under resource constraints
- **Error Conditions**: Performance during error scenarios
- **Recovery Testing**: Performance after failures
- **Long-running Tests**: Extended duration testing

## Tools and Frameworks

### Load Testing Tools
- **Artillery**: HTTP load testing with realistic scenarios
- **k6**: Developer-friendly load testing with JavaScript
- **JMeter**: Comprehensive performance testing
- **Custom Go Tools**: Language-specific benchmarks

### Monitoring Tools
- **Prometheus**: Metrics collection during tests
- **Grafana**: Real-time performance visualization
- **cAdvisor**: Container resource monitoring
- **Database Profilers**: Database-specific performance analysis

## Directory Structure

```
benchmark/
├── load-tests/              # Load testing scenarios
│   ├── artillery/          # Artillery.io test scripts
│   ├── k6/                 # k6 test scripts
│   └── custom/             # Custom load testing tools
├── database-benchmarks/     # Database-specific performance tests
│   ├── cassandra/          # Cassandra performance tests
│   ├── mongodb/            # MongoDB performance tests
│   ├── postgres/           # PostgreSQL performance tests
│   └── comparison/         # Cross-database comparisons
├── stress-tests/           # Stress testing scenarios
├── monitoring/             # Monitoring and reporting tools
├── results/               # Benchmark results and reports
├── scripts/               # Automation and utility scripts
└── README.md              # This file
```

## Quick Start

### Running Basic Load Tests
```bash
# Install dependencies
npm install

# Run basic timer creation test
npm run test:load-basic

# Run comprehensive test suite
npm run test:load-all

# Run database comparison
npm run test:db-comparison
```

### Custom Benchmark
```bash
# Build custom Go benchmark tool
cd custom && go build -o timer-bench

# Run custom benchmark
./timer-bench --target http://localhost:8080 \
              --concurrent 100 \
              --duration 60s \
              --operation create
```

## Load Testing Scenarios

### 1. **Timer Creation Benchmark**
```yaml
# artillery/timer-creation.yml
config:
  target: http://localhost:8080
  phases:
    - duration: 60
      arrivalRate: 10
      rampTo: 100
  defaults:
    headers:
      Authorization: Bearer {{ $env.API_TOKEN }}

scenarios:
  - name: Create timers
    weight: 100
    flow:
      - post:
          url: /groups/benchmark/timers
          json:
            timerId: "timer-{{ $randomString() }}"
            executeAt: "{{ $moment().add(1, 'hour').toISOString() }}"
            callbackUrl: "https://httpbin.org/post"
            payload:
              testData: "benchmark"
```

### 2. **Mixed Operations Test**
```javascript
// k6/mixed-operations.js
import http from 'k6/http';
import { check, group } from 'k6';

export let options = {
  stages: [
    { duration: '2m', target: 100 },
    { duration: '5m', target: 100 },
    { duration: '2m', target: 200 },
    { duration: '5m', target: 200 },
    { duration: '2m', target: 0 },
  ],
};

export default function () {
  const baseUrl = 'http://localhost:8080';
  const groupId = 'load-test';
  
  group('Timer Operations', function () {
    // Create timer
    const createResponse = http.post(`${baseUrl}/groups/${groupId}/timers`, {
      timerId: `timer-${__VU}-${__ITER}`,
      executeAt: new Date(Date.now() + 3600000).toISOString(),
      callbackUrl: 'https://httpbin.org/post',
      payload: { vu: __VU, iter: __ITER },
    });
    check(createResponse, { 'create status 201': (r) => r.status === 201 });
    
    if (createResponse.status === 201) {
      const timerId = JSON.parse(createResponse.body).timerId;
      
      // Get timer
      const getResponse = http.get(`${baseUrl}/groups/${groupId}/timers/${timerId}`);
      check(getResponse, { 'get status 200': (r) => r.status === 200 });
      
      // Update timer
      const updateResponse = http.put(`${baseUrl}/groups/${groupId}/timers/${timerId}`, {
        executeAt: new Date(Date.now() + 7200000).toISOString(),
      });
      check(updateResponse, { 'update status 200': (r) => r.status === 200 });
      
      // Delete timer
      const deleteResponse = http.del(`${baseUrl}/groups/${groupId}/timers/${timerId}`);
      check(deleteResponse, { 'delete status 204': (r) => r.status === 204 });
    }
  });
}
```

### 3. **Database Stress Test**
```go
// custom/database-stress.go
package main

import (
    "context"
    "fmt"
    "sync"
    "time"
)

type DatabaseStressTest struct {
    concurrency int
    duration    time.Duration
    target      string
}

func (t *DatabaseStressTest) Run() error {
    ctx, cancel := context.WithTimeout(context.Background(), t.duration)
    defer cancel()
    
    var wg sync.WaitGroup
    results := make(chan Result, t.concurrency)
    
    for i := 0; i < t.concurrency; i++ {
        wg.Add(1)
        go func(worker int) {
            defer wg.Done()
            t.worker(ctx, worker, results)
        }(i)
    }
    
    go func() {
        wg.Wait()
        close(results)
    }()
    
    return t.collectResults(results)
}

func (t *DatabaseStressTest) worker(ctx context.Context, id int, results chan<- Result) {
    client := NewTimerClient(t.target)
    
    for {
        select {
        case <-ctx.Done():
            return
        default:
            start := time.Now()
            err := t.executeOperation(client, id)
            duration := time.Since(start)
            
            results <- Result{
                Worker:   id,
                Duration: duration,
                Error:    err,
            }
        }
    }
}
```

## Database Benchmarks

### Comparison Framework
```bash
# Run comparative benchmark across databases
./scripts/db-comparison.sh \
  --databases "postgres,mongodb,cassandra" \
  --operations "create,read,update,delete" \
  --concurrent 50 \
  --duration 300s
```

### Performance Metrics
```yaml
# Database performance comparison
metrics:
  throughput:
    unit: operations/second
    databases:
      postgres: 1250
      mongodb: 1180
      cassandra: 1420
      dynamodb: 980
  
  latency_p95:
    unit: milliseconds
    databases:
      postgres: 45
      mongodb: 52
      cassandra: 38
      dynamodb: 78
  
  memory_usage:
    unit: MB
    databases:
      postgres: 256
      mongodb: 312
      cassandra: 198
      dynamodb: 145
```

## Monitoring Integration

### Prometheus Metrics
```yaml
# monitoring/prometheus.yml
global:
  scrape_interval: 15s

scrape_configs:
  - job_name: 'timer-service'
    static_configs:
      - targets: ['localhost:8080']
    scrape_interval: 5s
    
  - job_name: 'load-test-metrics'
    static_configs:
      - targets: ['localhost:9090']
```

### Grafana Dashboard
```json
{
  "dashboard": {
    "title": "Timer Service Load Test",
    "panels": [
      {
        "title": "Request Rate",
        "targets": [
          {
            "expr": "rate(http_requests_total[5m])",
            "legendFormat": "{{method}} {{status}}"
          }
        ]
      },
      {
        "title": "Response Time",
        "targets": [
          {
            "expr": "histogram_quantile(0.95, rate(http_request_duration_seconds_bucket[5m]))",
            "legendFormat": "95th percentile"
          }
        ]
      }
    ]
  }
}
```

## Automated Testing

### CI/CD Integration
```yaml
# .github/workflows/performance.yml
name: Performance Tests

on:
  pull_request:
    branches: [main]
  schedule:
    - cron: '0 2 * * *'  # Daily at 2 AM

jobs:
  performance:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      
      - name: Start services
        run: docker-compose up -d
        
      - name: Wait for services
        run: ./scripts/wait-for-services.sh
        
      - name: Run performance tests
        run: |
          cd benchmark
          npm install
          npm run test:regression
          
      - name: Upload results
        uses: actions/upload-artifact@v3
        with:
          name: performance-results
          path: benchmark/results/
```

### Regression Detection
```javascript
// scripts/regression-check.js
const fs = require('fs');
const baseline = JSON.parse(fs.readFileSync('results/baseline.json'));
const current = JSON.parse(fs.readFileSync('results/current.json'));

const thresholds = {
  throughput: 0.95,  // 5% degradation threshold
  latency_p95: 1.2,  // 20% increase threshold
  error_rate: 0.01,  // 1% error rate threshold
};

function checkRegression(baseline, current, thresholds) {
  const results = {};
  
  if (current.throughput < baseline.throughput * thresholds.throughput) {
    results.throughput = 'REGRESSION';
  }
  
  if (current.latency_p95 > baseline.latency_p95 * thresholds.latency_p95) {
    results.latency = 'REGRESSION';
  }
  
  if (current.error_rate > thresholds.error_rate) {
    results.errors = 'REGRESSION';
  }
  
  return results;
}
```

## Results and Reporting

### Report Generation
```bash
# Generate comprehensive performance report
./scripts/generate-report.sh \
  --input results/latest/ \
  --output reports/performance-report.html \
  --format html,json,csv
```

### Historical Tracking
```sql
-- Store benchmark results for trending
CREATE TABLE benchmark_results (
  id SERIAL PRIMARY KEY,
  test_date TIMESTAMP DEFAULT NOW(),
  test_type VARCHAR(50),
  database_type VARCHAR(50),
  throughput_ops_per_sec INTEGER,
  latency_p95_ms INTEGER,
  latency_p99_ms INTEGER,
  error_rate DECIMAL(5,4),
  memory_usage_mb INTEGER,
  cpu_usage_percent DECIMAL(5,2)
);
```

## Best Practices

### Test Design
- **Realistic Workloads**: Model actual usage patterns
- **Gradual Ramp-up**: Avoid overwhelming systems instantly
- **Baseline Establishment**: Consistent baseline measurements
- **Environment Isolation**: Dedicated test environments

### Monitoring
- **Comprehensive Metrics**: System and application metrics
- **Real-time Monitoring**: Live performance tracking
- **Alert Thresholds**: Automated issue detection
- **Resource Tracking**: CPU, memory, network, disk usage

### Result Analysis
- **Statistical Significance**: Multiple test runs for reliability
- **Outlier Detection**: Identify and handle anomalies
- **Trend Analysis**: Historical performance tracking
- **Root Cause Analysis**: Performance bottleneck identification 