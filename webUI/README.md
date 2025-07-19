# Timer Service Web UI

Modern web-based management interface for the distributed timer service, providing intuitive timer management and system monitoring.

## Overview

The Web UI provides a comprehensive browser-based interface for:
- Timer creation and management
- Real-time timer status monitoring  
- Group and shard visualization
- System health and metrics dashboards
- Configuration management
- Callback testing and debugging tools

## Technology Stack

- **Frontend Framework**: React with TypeScript
- **UI Library**: Material-UI or Ant Design
- **State Management**: Redux Toolkit or Zustand
- **Charts & Visualization**: Chart.js or D3.js
- **HTTP Client**: Axios with interceptors
- **Build Tool**: Vite or Create React App
- **Testing**: Jest + React Testing Library

## Features

### Timer Management
- **Create Timers**: Intuitive form with validation
- **Timer List**: Searchable, filterable timer table
- **Timer Details**: Comprehensive timer information view
- **Bulk Operations**: Import/export and batch actions
- **Timer Execution**: Real-time execution status updates

### Monitoring & Visualization
- **Dashboard**: Key metrics and system overview
- **Charts**: Timer execution rates, success/failure metrics
- **Group Analytics**: Per-group performance insights
- **Shard Distribution**: Visual shard load balancing
- **Historical Data**: Timeline views and trend analysis

### System Administration
- **Configuration**: Server and group configuration management
- **Health Checks**: Service health monitoring
- **Database Status**: Connection and performance metrics
- **User Management**: Authentication and authorization
- **Audit Logs**: Action history and change tracking

### Developer Tools
- **API Explorer**: Interactive API testing interface
- **Callback Tester**: Test webhook endpoints
- **JSON Editor**: Advanced timer payload editing
- **Debug Console**: Real-time logs and debugging
- **Performance Profiler**: Client-side performance monitoring

## Quick Start

### Development Setup
```bash
# Install dependencies
npm install

# Start development server
npm run dev

# Run tests
npm test

# Build for production
npm run build
```

### Environment Configuration
```bash
# .env.local
REACT_APP_API_URL=http://localhost:8080
REACT_APP_AUTH_ENABLED=true
REACT_APP_REFRESH_INTERVAL=5000
```

### Docker Development
```bash
# Build container
docker build -t timer-webui .

# Run with development API
docker run -p 3000:3000 \
  -e REACT_APP_API_URL=http://localhost:8080 \
  timer-webui
```

## User Interface

### Main Dashboard
- **Summary Cards**: Active timers, execution rate, success rate
- **Quick Actions**: Create timer, bulk import, system health
- **Recent Activity**: Latest timer executions and status changes
- **Alerts**: System notifications and error conditions

### Timer Management
- **Timer List**: Paginated table with search and filters
- **Timer Form**: Step-by-step timer creation wizard
- **Timer Details**: Comprehensive view with execution history
- **Bulk Import**: CSV/JSON file upload with validation

### Monitoring Views
- **Real-time Dashboard**: Live updating metrics and charts
- **Group Analytics**: Per-group performance breakdown
- **Execution Timeline**: Visual timeline of timer executions
- **Error Analysis**: Error tracking and root cause analysis

### Administration Panel
- **System Configuration**: Service and database settings
- **User Management**: User roles and permissions
- **Audit Trail**: Complete action history
- **Maintenance**: Database operations and cleanup

## Authentication

### Supported Methods
- **JWT Tokens**: Bearer token authentication
- **API Keys**: Service-to-service authentication
- **OAuth 2.0**: Third-party authentication providers
- **Local Authentication**: Username/password for development

### Role-Based Access Control
- **Admin**: Full system access and configuration
- **Operator**: Timer management and monitoring
- **Developer**: API testing and debugging tools
- **Viewer**: Read-only access to dashboards

## Configuration

### Build-time Configuration
```typescript
// config/app.config.ts
export const config = {
  apiUrl: process.env.REACT_APP_API_URL || 'http://localhost:8080',
  authEnabled: process.env.REACT_APP_AUTH_ENABLED === 'true',
  refreshInterval: parseInt(process.env.REACT_APP_REFRESH_INTERVAL || '5000'),
  maxPageSize: parseInt(process.env.REACT_APP_MAX_PAGE_SIZE || '100'),
};
```

### Runtime Configuration
```json
{
  "features": {
    "bulkOperations": true,
    "advancedMetrics": true,
    "callbackTesting": true,
    "auditLogging": true
  },
  "ui": {
    "theme": "light",
    "autoRefresh": true,
    "notifications": true
  }
}
```

## API Integration

### HTTP Client Configuration
```typescript
// services/api.ts
const apiClient = axios.create({
  baseURL: config.apiUrl,
  timeout: 10000,
  headers: {
    'Content-Type': 'application/json',
  },
});

// Request interceptor for authentication
apiClient.interceptors.request.use((config) => {
  const token = getAuthToken();
  if (token) {
    config.headers.Authorization = `Bearer ${token}`;
  }
  return config;
});
```

### Real-time Updates
```typescript
// hooks/useRealTimeUpdates.ts
export const useRealTimeUpdates = () => {
  useEffect(() => {
    const eventSource = new EventSource(`${config.apiUrl}/events`);
    
    eventSource.onmessage = (event) => {
      const data = JSON.parse(event.data);
      dispatch(updateTimerStatus(data));
    };

    return () => eventSource.close();
  }, []);
};
```

## Performance Optimization

### Code Splitting
```typescript
// Lazy load heavy components
const TimerDetailsModal = lazy(() => import('./TimerDetailsModal'));
const MetricsDashboard = lazy(() => import('./MetricsDashboard'));
```

### Virtualization
```typescript
// Handle large timer lists
import { FixedSizeList as List } from 'react-window';

const TimerList = ({ timers }) => (
  <List
    height={600}
    itemCount={timers.length}
    itemSize={80}
    itemData={timers}
  >
    {TimerListItem}
  </List>
);
```

### Caching Strategy
```typescript
// React Query for efficient caching
const { data: timers, isLoading } = useQuery(
  ['timers', groupId, filters],
  () => fetchTimers(groupId, filters),
  {
    staleTime: 30000,
    cacheTime: 300000,
    refetchOnWindowFocus: false,
  }
);
```

## Testing

### Unit Tests
```bash
# Run unit tests
npm test

# Run with coverage
npm run test:coverage

# Watch mode for development
npm run test:watch
```

### Integration Tests
```typescript
// __tests__/timer-management.test.tsx
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { TimerManagement } from '../TimerManagement';

test('creates new timer successfully', async () => {
  render(<TimerManagement />);
  
  await userEvent.click(screen.getByText('Create Timer'));
  await userEvent.type(screen.getByLabelText('Timer ID'), 'test-timer');
  await userEvent.click(screen.getByText('Save'));
  
  await waitFor(() => {
    expect(screen.getByText('Timer created successfully')).toBeInTheDocument();
  });
});
```

### End-to-End Tests
```typescript
// e2e/timer-lifecycle.spec.ts
import { test, expect } from '@playwright/test';

test('timer lifecycle flow', async ({ page }) => {
  await page.goto('/');
  await page.click('text=Create Timer');
  await page.fill('[data-testid=timer-id]', 'e2e-test-timer');
  await page.click('text=Save');
  
  await expect(page.locator('text=Timer created')).toBeVisible();
});
```

## Deployment

### Production Build
```bash
# Create optimized build
npm run build

# Analyze bundle size
npm run analyze

# Serve locally
npm run serve
```

### Docker Deployment
```dockerfile
# Multi-stage build
FROM node:18-alpine AS builder
WORKDIR /app
COPY package*.json ./
RUN npm ci --only=production

COPY . .
RUN npm run build

FROM nginx:alpine
COPY --from=builder /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/nginx.conf
EXPOSE 80
```

### Kubernetes Deployment
```yaml
# k8s/webui-deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: timer-webui
spec:
  replicas: 2
  selector:
    matchLabels:
      app: timer-webui
  template:
    spec:
      containers:
      - name: webui
        image: timer-webui:latest
        ports:
        - containerPort: 80
        env:
        - name: API_URL
          value: "http://timer-server-service:8080"
```

## Security

### Content Security Policy
```typescript
// Helmet configuration
app.use(helmet({
  contentSecurityPolicy: {
    directives: {
      defaultSrc: ["'self'"],
      styleSrc: ["'self'", "'unsafe-inline'"],
      scriptSrc: ["'self'"],
      imgSrc: ["'self'", "data:", "https:"],
    },
  },
}));
```

### Authentication Security
- JWT token validation and refresh
- Secure token storage (HttpOnly cookies)
- CSRF protection
- Input sanitization and validation
- API rate limiting 