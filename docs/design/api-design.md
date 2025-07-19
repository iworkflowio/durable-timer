# Timer Service API Design

## Overview

This document describes the design decisions and rationale behind the Distributed Durable Timer Service REST API. The API provides a minimal, focused interface for managing one-time timers with HTTP webhook callbacks.

## Core Design Principles

### 1. Minimal API Surface
- **Decision**: Only 4 endpoints for core CRUD operations
- **Rationale**: Strict adherence to requirements prevents scope creep and maintains focus on essential timer functionality
- **Endpoints**:
  - `POST /timers` - Create timer
  - `GET /timers/{groupId}/{timerId}` - Get timer details
  - `PUT /timers/{groupId}/{timerId}` - Update timer
  - `DELETE /timers/{groupId}/{timerId}` - Delete timer

### 2. Group-Based Scalability
- **Decision**: All timers belong to a `groupId` which is required in all operations(optional for creating API, but it has a default value as fallback)
- **Rationale**: Enables horizontal scaling through sharding - different groups can be handled by different service instances
- **Implementation**:
  - Path parameter: `/timers/{groupId}/{timerId}`
  - Required field in timer creation
  - Default value: `"default"`
  - Must be one of the enabled groups in the system

### 3. No Built-in Authentication
- **Decision**: Removed API key authentication from the specification
- **Rationale**: Keeps the core API simple - authentication can be handled at the infrastructure level (reverse proxy, API gateway)

## Key API Components

### Timer Creation (`POST /timers`)

**Response Strategy**:
- Returns `200` for both new timer creation and existing timer (deduplication)
- Returns `409` when timer ID already exists with different parameters
- Simplifies client logic - clients don't need to distinguish between creation and existing

**Required Fields**:
```json
{
  "id": "unique-timer-id",
  "groupId": "scalability-group", 
  "executeAt": "2024-12-20T15:30:00Z",
  "callbackUrl": "https://api.example.com/webhook"
}
```

**Optional Fields**:
- `payload` - Custom JSON data included in callback
- `retryPolicy` - Configurable retry behavior
- `callbackTimeout` - HTTP request timeout (default: "30s")

### Timer Identification

**Composite Key Design**:
- Timers are identified by `{groupId, timerId}` combination
- Enables:
  - Scalable sharding by group
  - Simplified lookup operations
  - Isolation between different use cases/tenants

### Callback Response Protocol

**CallbackResponse Schema**:
```json
{
  "ok": true,  // Required: indicates success/failure
  "nextExecuteAt": "2024-12-21T15:30:00Z"  // Optional: reschedule timer
}
```

**Success Semantics**:
- HTTP 200 + `{"ok": true}` = Success, timer completes
- HTTP 200 + `{"ok": false, "nextExecuteAt": "..."}` = Reschedule timer
- Any other HTTP code = Failure, retry according to policy

**Design Benefits**:
- Clear success/failure semantics
- Enables timer rescheduling from callback response
- Standardized response format across all callbacks

### Retry Policy Configuration

**Enhanced Retry Control**:
```json
{
  "maxRetries": 3,
  "maxRetryAttemptsDuration": "24h",
  "initialInterval": "30s", 
  "backoffMultiplier": 2.0,
  "maxInterval": "10m"
}
```

**Key Features**:
- `maxRetryAttemptsDuration` - Limits total retry window (prevents infinite retries)
- Exponential backoff with configurable multiplier
- Per-timer retry policy customization

### Simplified Timer Model

**Removed Complexity**:
- No `status` field in Timer response
- No `nextExecuteAt` field (handled via callback response)
- Focus on essential timer data only

**Retained Fields**:
- Core identification: `id`, `groupId`
- Scheduling: `executeAt`
- Callback: `callbackUrl`, `payload`
- Metadata: `createdAt`, `updatedAt`, `executedAt`
- Configuration: `retryPolicy`, `callbackTimeout`

## Error Handling Strategy

### HTTP Status Code Usage

| Code | Usage | Description |
|------|--------|-------------|
| 200 | Success | Timer created/updated/retrieved successfully |
| 204 | Success | Timer deleted successfully |
| 400 | Client Error | Invalid request parameters |
| 404 | Client Error | Timer not found |
| 409 | Conflict | Timer already exists (creation only) |
| 500 | Server Error | Internal server error |

### Error Response Format
```json
{
  "error": "ERROR_CODE",
  "message": "Human-readable description", 
  "details": {},
  "timestamp": "2024-12-19T10:15:30Z"
}
```

## Scalability Considerations

### Group-Based Sharding
- Each `groupId` can be assigned to specific service instances
- Enables horizontal scaling by adding more groups/instances
- Supports isolation between different workloads

### No List Operations
- Intentionally no `GET /timers` endpoint
- Prevents expensive full-table scans at scale
- Encourages client-side timer tracking
- Focused on individual timer operations

## API Evolution Strategy

### Versioning
- URL-based versioning: `/api/v1/`
- Allows for breaking changes in future versions
- Current API is v1.0.0

### Backward Compatibility
- Required fields will not be removed
- Optional fields may be added
- Endpoint signatures will remain stable within major version

## Implementation Notes

### Timer Execution Flow
1. Timer reaches `executeAt` time
2. Service makes HTTP POST to `callbackUrl`
3. Request includes timer `id` and `payload` 
4. Response parsed as `CallbackResponse`
5. Based on response:
   - `ok: true` → Timer completes
   - `ok: false` + `nextExecuteAt` → Timer reschedules
   - HTTP error or timeout → Retry per policy

### Deduplication Strategy
- Timer IDs must be unique within a group
- Creating with existing ID returns existing timer (200)
- Different parameters for same ID return conflict (409)

### Time Precision
- ISO 8601 timestamps with second precision
- Supports timezone specification
- Timer execution within ±1 second of scheduled time

## Security Considerations

### Input Validation
- Timer ID: Max 255 characters
- Callback URL: Max 2048 characters, valid URI format
- Payload: Valid JSON, reasonable size limits
- Timestamps: Valid ISO 8601 format, future dates only

### Rate Limiting
- Should be implemented at infrastructure level
- Recommended limits per group/client
- Prevents abuse and ensures fair resource usage

### Callback Security
- HTTPS recommended for callback URLs
- Callback endpoints should validate timer authenticity
- Timeout protection prevents hanging requests

## API Examples

### Create Timer
```bash
POST /api/v1/timers
{
  "id": "user-reminder-123",
  "groupId": "notifications",
  "executeAt": "2024-12-20T15:30:00Z",
  "callbackUrl": "https://app.example.com/webhooks/timer",
  "payload": {
    "userId": "user123",
    "action": "send_reminder"
  },
  "retryPolicy": {
    "maxRetries": 3,
    "maxRetryAttemptsDuration": "1h"
  }
}
```

### Get Timer
```bash
GET /api/v1/timers/notifications/default/user-reminder-123
```

### Update Timer
```bash
PUT /api/v1/timers/notifications/default/user-reminder-123
{
  "executeAt": "2024-12-20T16:00:00Z",
  "payload": {
    "userId": "user123", 
    "action": "send_urgent_reminder"
  }
}
```

### Delete Timer
```bash
DELETE /api/v1/timers/notifications/default/user-reminder-123
```

---

*This design document reflects the current API specification and will be updated as the API evolves.* 