# Kubernetes Helm Charts

Helm charts for deploying the distributed timer service to Kubernetes clusters with production-ready configurations.

## Overview

This directory contains Helm charts for deploying the timer service stack to Kubernetes, including:
- Timer service server deployment
- Multi-database support configurations
- Web UI deployment and ingress
- Monitoring and observability stack
- Auto-scaling and resource management
- Security policies and network configurations

## Chart Structure

```
helmcharts/
├── timer-service/              # Main application chart
│   ├── Chart.yaml             # Chart metadata and dependencies
│   ├── values.yaml            # Default configuration values
│   ├── values-production.yaml # Production-specific overrides
│   ├── templates/             # Kubernetes resource templates
│   │   ├── deployment.yaml    # Server deployment
│   │   ├── service.yaml       # Service definitions
│   │   ├── ingress.yaml       # Ingress configuration
│   │   ├── configmap.yaml     # Configuration management
│   │   ├── secret.yaml        # Secrets management
│   │   ├── hpa.yaml           # Horizontal Pod Autoscaler
│   │   ├── pdb.yaml           # Pod Disruption Budget
│   │   ├── rbac.yaml          # Role-based access control
│   │   └── tests/             # Helm test templates
│   └── charts/                # Dependency charts
├── monitoring/                # Monitoring stack chart
│   ├── Chart.yaml            # Prometheus, Grafana, AlertManager
│   ├── values.yaml           # Monitoring configuration
│   └── templates/            # Monitoring resource templates
├── ingress-controller/       # Ingress controller chart
├── cert-manager/            # TLS certificate management
└── README.md                # This file
```

## Quick Start

### Prerequisites

```bash
# Install Helm 3.x
curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash

# Add required repositories
helm repo add ingress-nginx https://kubernetes.github.io/ingress-nginx
helm repo add jetstack https://charts.jetstack.io
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update
```

### Basic Deployment

```bash
# Deploy timer service with default values
helm install timer-service ./timer-service

# Deploy with custom values
helm install timer-service ./timer-service -f values-production.yaml

# Deploy to specific namespace
kubectl create namespace timer-service
helm install timer-service ./timer-service --namespace timer-service
```

### Production Deployment

```bash
# Create namespace
kubectl create namespace timer-service

# Deploy ingress controller (if not already present)
helm install ingress-nginx ingress-nginx/ingress-nginx \
  --namespace ingress-system \
  --create-namespace

# Deploy cert-manager for TLS
helm install cert-manager jetstack/cert-manager \
  --namespace cert-manager \
  --create-namespace \
  --set installCRDs=true

# Deploy monitoring stack
helm install monitoring ./monitoring \
  --namespace monitoring \
  --create-namespace

# Deploy timer service
helm install timer-service ./timer-service \
  --namespace timer-service \
  --values values-production.yaml
```

## Configuration

### Core Values (`values.yaml`)

```yaml
# Timer Service Configuration
timerService:
  image:
    repository: timer-service
    tag: "latest"
    pullPolicy: IfNotPresent
  
  replicaCount: 2
  
  resources:
    requests:
      memory: "256Mi"
      cpu: "250m"
    limits:
      memory: "512Mi"
      cpu: "500m"
  
  env:
    LOG_LEVEL: "info"
    DB_TYPE: "postgres"
    METRICS_ENABLED: "true"

# Database Configuration
database:
  type: postgres  # postgres, mysql, mongodb, cassandra
  
  postgres:
    host: "postgres-service"
    port: 5432
    database: "timer_service"
    username: "timer_user"
    # password: set via secret
    
  mysql:
    host: "mysql-service"
    port: 3306
    database: "timer_service"
    
  mongodb:
    uri: "mongodb://mongodb-service:27017/timer_service"
    
  cassandra:
    hosts: ["cassandra-0.cassandra", "cassandra-1.cassandra"]
    keyspace: "timer_service"

# Web UI Configuration
webui:
  enabled: true
  image:
    repository: timer-webui
    tag: "latest"
  
  replicaCount: 2
  
  env:
    REACT_APP_API_URL: "/api"

# Ingress Configuration
ingress:
  enabled: true
  className: "nginx"
  annotations:
    cert-manager.io/cluster-issuer: "letsencrypt-prod"
    nginx.ingress.kubernetes.io/rate-limit: "100"
    nginx.ingress.kubernetes.io/rate-limit-window: "1m"
  
  hosts:
    - host: timer.example.com
      paths:
        - path: /api
          pathType: Prefix
          service: timer-service
        - path: /
          pathType: Prefix
          service: timer-webui
  
  tls:
    - secretName: timer-service-tls
      hosts:
        - timer.example.com

# Auto-scaling Configuration
autoscaling:
  enabled: true
  minReplicas: 2
  maxReplicas: 10
  targetCPUUtilizationPercentage: 70
  targetMemoryUtilizationPercentage: 80

# Monitoring Configuration
monitoring:
  enabled: true
  prometheus:
    enabled: true
  grafana:
    enabled: true
  serviceMonitor:
    enabled: true

# Security Configuration
security:
  networkPolicy:
    enabled: true
  podSecurityPolicy:
    enabled: true
  rbac:
    enabled: true
```

### Production Overrides (`values-production.yaml`)

```yaml
timerService:
  replicaCount: 5
  
  image:
    tag: "v1.0.0"
    pullPolicy: Always
  
  resources:
    requests:
      memory: "512Mi"
      cpu: "500m"
    limits:
      memory: "1Gi"
      cpu: "1000m"
  
  env:
    LOG_LEVEL: "warn"
    DB_TYPE: "postgres"
    DB_MAX_CONNECTIONS: "100"
    CACHE_ENABLED: "true"

database:
  postgres:
    host: "postgres-cluster.db.svc.cluster.local"
    port: 5432
    ssl: true
    maxConnections: 100

autoscaling:
  enabled: true
  minReplicas: 5
  maxReplicas: 50
  behavior:
    scaleUp:
      stabilizationWindowSeconds: 60
      policies:
      - type: Percent
        value: 100
        periodSeconds: 15
    scaleDown:
      stabilizationWindowSeconds: 300
      policies:
      - type: Percent
        value: 10
        periodSeconds: 60

ingress:
  annotations:
    nginx.ingress.kubernetes.io/rate-limit: "1000"
    nginx.ingress.kubernetes.io/ssl-redirect: "true"
    nginx.ingress.kubernetes.io/force-ssl-redirect: "true"

monitoring:
  grafana:
    persistence:
      enabled: true
      size: 10Gi
  prometheus:
    retention: "30d"
    storage:
      size: 100Gi
```

## Database Deployment

### PostgreSQL

```yaml
# Deploy PostgreSQL using Bitnami chart
helm install postgres bitnami/postgresql \
  --namespace timer-service \
  --set auth.username=timer_user \
  --set auth.password=secure_password \
  --set auth.database=timer_service \
  --set primary.persistence.size=100Gi \
  --set metrics.enabled=true
```

### MongoDB

```yaml
# Deploy MongoDB using Bitnami chart
helm install mongodb bitnami/mongodb \
  --namespace timer-service \
  --set auth.username=timer_user \
  --set auth.password=secure_password \
  --set auth.database=timer_service \
  --set persistence.size=100Gi \
  --set metrics.enabled=true
```

### Cassandra

```yaml
# Deploy Cassandra cluster
helm install cassandra bitnami/cassandra \
  --namespace timer-service \
  --set replicaCount=3 \
  --set persistence.size=100Gi \
  --set metrics.enabled=true
```

## Monitoring Deployment

### Prometheus Stack

```yaml
# monitoring/values.yaml
prometheus:
  prometheusSpec:
    retention: 30d
    storageSpec:
      volumeClaimTemplate:
        spec:
          storageClassName: fast-ssd
          accessModes: ["ReadWriteOnce"]
          resources:
            requests:
              storage: 100Gi

grafana:
  adminPassword: "secure_admin_password"
  persistence:
    enabled: true
    size: 10Gi
  
  dashboardProviders:
    dashboardproviders.yaml:
      apiVersion: 1
      providers:
      - name: 'timer-service'
        orgId: 1
        folder: 'Timer Service'
        type: file
        disableDeletion: false
        editable: true
        options:
          path: /var/lib/grafana/dashboards/timer-service

alertmanager:
  config:
    global:
      smtp_smarthost: 'smtp.example.com:587'
      smtp_from: 'alerts@example.com'
    
    route:
      group_by: ['alertname', 'service']
      group_wait: 30s
      group_interval: 5m
      repeat_interval: 12h
      receiver: 'timer-service-alerts'
    
    receivers:
    - name: 'timer-service-alerts'
      email_configs:
      - to: 'ops-team@example.com'
        subject: 'Timer Service Alert: {{ .GroupLabels.alertname }}'
```

## Security Configuration

### Network Policies

```yaml
# templates/networkpolicy.yaml
{{- if .Values.security.networkPolicy.enabled }}
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: {{ include "timer-service.fullname" . }}
spec:
  podSelector:
    matchLabels:
      {{- include "timer-service.selectorLabels" . | nindent 6 }}
  policyTypes:
  - Ingress
  - Egress
  ingress:
  - from:
    - namespaceSelector:
        matchLabels:
          name: ingress-system
    ports:
    - protocol: TCP
      port: 8080
  - from:
    - namespaceSelector:
        matchLabels:
          name: monitoring
    ports:
    - protocol: TCP
      port: 8080
  egress:
  - to:
    - namespaceSelector:
        matchLabels:
          name: timer-service
    ports:
    - protocol: TCP
      port: 5432  # PostgreSQL
  - to: []  # Allow all egress for callback URLs
    ports:
    - protocol: TCP
      port: 80
    - protocol: TCP
      port: 443
{{- end }}
```

### Pod Security Policy

```yaml
# templates/podsecuritypolicy.yaml
{{- if .Values.security.podSecurityPolicy.enabled }}
apiVersion: policy/v1beta1
kind: PodSecurityPolicy
metadata:
  name: {{ include "timer-service.fullname" . }}
spec:
  privileged: false
  allowPrivilegeEscalation: false
  requiredDropCapabilities:
    - ALL
  volumes:
    - 'configMap'
    - 'emptyDir'
    - 'projected'
    - 'secret'
    - 'downwardAPI'
    - 'persistentVolumeClaim'
  runAsUser:
    rule: 'MustRunAsNonRoot'
  seLinux:
    rule: 'RunAsAny'
  fsGroup:
    rule: 'RunAsAny'
{{- end }}
```

## High Availability Configuration

### Pod Disruption Budget

```yaml
# templates/pdb.yaml
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: {{ include "timer-service.fullname" . }}
spec:
  minAvailable: {{ .Values.pdb.minAvailable | default "50%" }}
  selector:
    matchLabels:
      {{- include "timer-service.selectorLabels" . | nindent 6 }}
```

### Multi-Zone Deployment

```yaml
# Anti-affinity for high availability
affinity:
  podAntiAffinity:
    preferredDuringSchedulingIgnoredDuringExecution:
    - weight: 100
      podAffinityTerm:
        labelSelector:
          matchExpressions:
          - key: app.kubernetes.io/name
            operator: In
            values:
            - timer-service
        topologyKey: kubernetes.io/hostname
    - weight: 50
      podAffinityTerm:
        labelSelector:
          matchExpressions:
          - key: app.kubernetes.io/name
            operator: In
            values:
            - timer-service
        topologyKey: topology.kubernetes.io/zone
```

## Operations

### Deployment Commands

```bash
# Install/upgrade
helm upgrade --install timer-service ./timer-service \
  --namespace timer-service \
  --create-namespace \
  --values values-production.yaml \
  --wait \
  --timeout 10m

# Rollback
helm rollback timer-service 1 --namespace timer-service

# Check status
helm status timer-service --namespace timer-service

# Get values
helm get values timer-service --namespace timer-service
```

### Debugging

```bash
# Template rendering (dry-run)
helm template timer-service ./timer-service \
  --values values-production.yaml \
  --debug

# Lint chart
helm lint ./timer-service

# Test deployment
helm test timer-service --namespace timer-service

# Check resources
kubectl get all -l app.kubernetes.io/instance=timer-service -n timer-service
```

### Backup and Recovery

```bash
# Backup Helm release
helm get all timer-service --namespace timer-service > timer-service-backup.yaml

# Backup persistent volumes
kubectl get pv -o yaml > pv-backup.yaml

# Database backup (using CronJob)
kubectl create job --from=cronjob/postgres-backup backup-$(date +%Y%m%d) -n timer-service
```

## Troubleshooting

### Common Issues

#### Pod Startup Issues
```bash
# Check pod status
kubectl get pods -n timer-service

# Check pod logs
kubectl logs -f deployment/timer-service -n timer-service

# Describe pod for events
kubectl describe pod <pod-name> -n timer-service
```

#### Database Connection Issues
```bash
# Test database connectivity
kubectl run -it --rm debug --image=postgres:15 --restart=Never -n timer-service -- \
  psql -h postgres-service -U timer_user -d timer_service

# Check service endpoints
kubectl get endpoints -n timer-service
```

#### Ingress Issues
```bash
# Check ingress status
kubectl get ingress -n timer-service

# Check ingress controller logs
kubectl logs -f deployment/ingress-nginx-controller -n ingress-system

# Test internal connectivity
kubectl run -it --rm debug --image=curlimages/curl --restart=Never -n timer-service -- \
  curl -v http://timer-service:8080/health
```

### Performance Tuning

#### Resource Optimization
```yaml
# Fine-tune resource requests and limits
resources:
  requests:
    memory: "512Mi"
    cpu: "250m"
  limits:
    memory: "1Gi"
    cpu: "1000m"

# Optimize JVM settings for Java applications
env:
  - name: JAVA_OPTS
    value: "-Xms512m -Xmx1g -XX:+UseG1GC"
```

#### Auto-scaling Configuration
```yaml
# Advanced HPA configuration
autoscaling:
  enabled: true
  minReplicas: 3
  maxReplicas: 20
  metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: 70
  - type: Resource
    resource:
      name: memory
      target:
        type: Utilization
        averageUtilization: 80
  - type: Pods
    pods:
      metric:
        name: timer_queue_size
      target:
        type: AverageValue
        averageValue: "100"
```

## Best Practices

### Security
- Use secrets for sensitive configuration
- Enable network policies in production
- Run containers as non-root users
- Regularly update base images
- Implement proper RBAC

### Performance
- Set appropriate resource requests and limits
- Use horizontal pod autoscaling
- Implement pod disruption budgets
- Use persistent volumes for stateful data
- Monitor and optimize database connections

### Monitoring
- Enable Prometheus metrics collection
- Set up comprehensive alerting rules
- Use Grafana dashboards for visualization
- Implement health checks and readiness probes
- Monitor application and infrastructure metrics

### Maintenance
- Regularly update Helm charts and dependencies
- Test deployments in staging environments
- Implement automated testing and validation
- Maintain backup and disaster recovery procedures
- Document operational procedures and runbooks 