# Timer Service Go SDK

Go client library for the distributed timer service, providing idiomatic Go APIs for timer management.

## Overview

The Go SDK provides a comprehensive client library for interacting with the timer service from Go applications. It includes:
- Full CRUD operations for timers
- Automatic retry logic with exponential backoff
- Connection pooling and request optimization
- Structured error handling
- Context support for cancellation and timeouts
- Built-in metrics and observability

## Installation

```bash
go get github.com/iworkflowio/durable-timer/sdks/go
```

## Quick Start

### Basic Usage

```go
package main

import (
    "context"
    "fmt"
    "time"

    timer "github.com/iworkflowio/durable-timer/sdks/go"
)

func main() {
    // Create client
    client := timer.NewClient("https://timer-service.example.com")
    
    // Create a timer
    ctx := context.Background()
    req := &timer.CreateTimerRequest{
        ExecuteAt:   time.Now().Add(1 * time.Hour),
        CallbackURL: "https://api.example.com/webhook",
        Payload: map[string]interface{}{
            "userId": "123",
            "action": "reminder",
        },
    }
    
    timer, err := client.CreateTimer(ctx, "notifications", "user-123", req)
    if err != nil {
        fmt.Printf("Failed to create timer: %v\n", err)
        return
    }
    
    fmt.Printf("Timer created: %s\n", timer.TimerID)
}
```

### Configuration

```go
// Advanced configuration
client := timer.NewClient("https://timer-service.example.com",
    timer.WithAPIKey("your-api-key"),
    timer.WithTimeout(30*time.Second),
    timer.WithRetryPolicy(timer.RetryPolicy{
        MaxRetries:  3,
        InitialDelay: time.Second,
        MaxDelay:    10 * time.Second,
        Multiplier:  2.0,
    }),
    timer.WithHTTPClient(customHTTPClient),
)
```

## API Reference

### Client Configuration

#### `NewClient(serverURL string, opts ...ClientOption) *Client`
Creates a new timer service client.

**Parameters:**
- `serverURL`: Base URL of the timer service
- `opts`: Optional configuration options

**Example:**
```go
client := timer.NewClient("https://timer-service.example.com")
```

#### Configuration Options

```go
// Authentication
timer.WithAPIKey("your-api-key")
timer.WithBearerToken("your-bearer-token")

// Timeouts and retries
timer.WithTimeout(30 * time.Second)
timer.WithRetryPolicy(timer.RetryPolicy{
    MaxRetries:   3,
    InitialDelay: time.Second,
    MaxDelay:     10 * time.Second,
    Multiplier:   2.0,
})

// HTTP configuration
timer.WithHTTPClient(customClient)
timer.WithUserAgent("my-app/1.0.0")

// Debugging
timer.WithDebug(true)
timer.WithLogger(customLogger)
```

### Timer Operations

#### `CreateTimer(ctx context.Context, groupID, timerID string, req *CreateTimerRequest) (*Timer, error)`
Creates a new timer.

**Parameters:**
- `ctx`: Context for cancellation and timeouts
- `groupID`: Timer group identifier
- `timerID`: Unique timer identifier
- `req`: Timer creation request

**Example:**
```go
req := &timer.CreateTimerRequest{
    ExecuteAt:       time.Now().Add(1 * time.Hour),
    CallbackURL:     "https://api.example.com/webhook",
    Payload:         map[string]interface{}{"key": "value"},
    CallbackTimeout: 30 * time.Second,
    RetryPolicy: &timer.RetryPolicy{
        MaxRetries:   3,
        InitialDelay: time.Second,
        MaxDelay:     10 * time.Second,
        Multiplier:   2.0,
    },
}

timer, err := client.CreateTimer(ctx, "notifications", "user-123", req)
```

#### `GetTimer(ctx context.Context, groupID, timerID string) (*Timer, error)`
Retrieves timer details.

**Example:**
```go
timer, err := client.GetTimer(ctx, "notifications", "user-123")
if err != nil {
    if timer.IsNotFound(err) {
        fmt.Println("Timer not found")
        return
    }
    // Handle other errors
}
```

#### `UpdateTimer(ctx context.Context, groupID, timerID string, req *UpdateTimerRequest) (*Timer, error)`
Updates an existing timer.

**Example:**
```go
req := &timer.UpdateTimerRequest{
    ExecuteAt: time.Now().Add(2 * time.Hour), // Reschedule
    Payload: map[string]interface{}{
        "userId": "123",
        "action": "updated_reminder",
    },
}

timer, err := client.UpdateTimer(ctx, "notifications", "user-123", req)
```

#### `DeleteTimer(ctx context.Context, groupID, timerID string) error`
Deletes a timer.

**Example:**
```go
err := client.DeleteTimer(ctx, "notifications", "user-123")
if err != nil {
    if timer.IsNotFound(err) {
        fmt.Println("Timer already deleted")
        return
    }
    // Handle other errors
}
```

## Data Models

### Timer

```go
type Timer struct {
    TimerID         string                 `json:"timerId"`
    GroupID         string                 `json:"groupId"`
    ExecuteAt       time.Time              `json:"executeAt"`
    CallbackURL     string                 `json:"callbackUrl"`
    Payload         map[string]interface{} `json:"payload,omitempty"`
    CallbackTimeout time.Duration          `json:"callbackTimeout,omitempty"`
    RetryPolicy     *RetryPolicy           `json:"retryPolicy,omitempty"`
    CreatedAt       time.Time              `json:"createdAt"`
    UpdatedAt       time.Time              `json:"updatedAt"`
    ExecutedAt      *time.Time             `json:"executedAt,omitempty"`
}
```

### CreateTimerRequest

```go
type CreateTimerRequest struct {
    ExecuteAt       time.Time              `json:"executeAt"`
    CallbackURL     string                 `json:"callbackUrl"`
    Payload         map[string]interface{} `json:"payload,omitempty"`
    CallbackTimeout time.Duration          `json:"callbackTimeout,omitempty"`
    RetryPolicy     *RetryPolicy           `json:"retryPolicy,omitempty"`
}
```

### UpdateTimerRequest

```go
type UpdateTimerRequest struct {
    ExecuteAt       *time.Time             `json:"executeAt,omitempty"`
    CallbackURL     *string                `json:"callbackUrl,omitempty"`
    Payload         map[string]interface{} `json:"payload,omitempty"`
    CallbackTimeout *time.Duration         `json:"callbackTimeout,omitempty"`
    RetryPolicy     *RetryPolicy           `json:"retryPolicy,omitempty"`
}
```

### RetryPolicy

```go
type RetryPolicy struct {
    MaxRetries   int           `json:"maxRetries"`
    InitialDelay time.Duration `json:"initialDelay"`
    MaxDelay     time.Duration `json:"maxDelay"`
    Multiplier   float64       `json:"multiplier"`
}
```

## Error Handling

### Error Types

```go
// Check error types
if timer.IsNotFound(err) {
    // Timer doesn't exist
}

if timer.IsConflict(err) {
    // Timer ID already exists
}

if timer.IsValidation(err) {
    // Invalid request parameters
}

if timer.IsAuthentication(err) {
    // Authentication failed
}

if timer.IsRateLimit(err) {
    // Rate limit exceeded
}

if timer.IsServerError(err) {
    // Internal server error
}

if timer.IsNetworkError(err) {
    // Network connectivity issue
}
```

### Error Handling Best Practices

```go
timer, err := client.CreateTimer(ctx, groupID, timerID, req)
if err != nil {
    switch {
    case timer.IsConflict(err):
        // Timer already exists, decide whether to update or skip
        return client.UpdateTimer(ctx, groupID, timerID, updateReq)
    
    case timer.IsValidation(err):
        // Invalid input, log and return user-friendly error
        log.Errorf("Invalid timer request: %v", err)
        return fmt.Errorf("invalid timer parameters")
    
    case timer.IsRateLimit(err):
        // Implement backoff and retry
        time.Sleep(time.Second)
        return client.CreateTimer(ctx, groupID, timerID, req)
    
    case timer.IsNetworkError(err):
        // Network issue, consider retry with circuit breaker
        return err
    
    default:
        // Unexpected error
        log.Errorf("Unexpected error creating timer: %v", err)
        return err
    }
}
```

## Advanced Features

### Context Usage

```go
// Request timeout
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()

timer, err := client.CreateTimer(ctx, groupID, timerID, req)

// Request cancellation
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

// Cancel request if condition is met
go func() {
    if someCondition {
        cancel()
    }
}()

timer, err := client.CreateTimer(ctx, groupID, timerID, req)
```

### Bulk Operations

```go
// Create multiple timers
timers := []*timer.CreateTimerRequest{
    {ExecuteAt: time.Now().Add(1*time.Hour), CallbackURL: "https://api.example.com/webhook1"},
    {ExecuteAt: time.Now().Add(2*time.Hour), CallbackURL: "https://api.example.com/webhook2"},
}

for i, req := range timers {
    timerID := fmt.Sprintf("bulk-timer-%d", i)
    _, err := client.CreateTimer(ctx, "bulk-group", timerID, req)
    if err != nil {
        log.Errorf("Failed to create timer %s: %v", timerID, err)
    }
}
```

### Custom HTTP Client

```go
// Custom HTTP client with connection pooling
transport := &http.Transport{
    MaxIdleConns:        100,
    MaxIdleConnsPerHost: 10,
    IdleConnTimeout:     90 * time.Second,
}

httpClient := &http.Client{
    Transport: transport,
    Timeout:   30 * time.Second,
}

client := timer.NewClient("https://timer-service.example.com",
    timer.WithHTTPClient(httpClient),
)
```

## Testing

### Unit Testing with Mocks

```go
// Mock client for testing
mockClient := timer.NewMockClient()
mockClient.On("CreateTimer", mock.Anything, "test-group", "test-timer", mock.Anything).
    Return(&timer.Timer{TimerID: "test-timer"}, nil)

// Use mock in tests
service := NewMyService(mockClient)
err := service.ScheduleReminder("user-123")
assert.NoError(t, err)

mockClient.AssertExpectations(t)
```

### Integration Testing

```go
func TestTimerIntegration(t *testing.T) {
    // Start test server
    server := timer.NewTestServer()
    defer server.Close()
    
    client := timer.NewClient(server.URL)
    
    // Test timer lifecycle
    req := &timer.CreateTimerRequest{
        ExecuteAt:   time.Now().Add(1 * time.Hour),
        CallbackURL: "https://httpbin.org/post",
    }
    
    created, err := client.CreateTimer(ctx, "test", "timer-1", req)
    assert.NoError(t, err)
    assert.Equal(t, "timer-1", created.TimerID)
    
    retrieved, err := client.GetTimer(ctx, "test", "timer-1")
    assert.NoError(t, err)
    assert.Equal(t, created.TimerID, retrieved.TimerID)
    
    err = client.DeleteTimer(ctx, "test", "timer-1")
    assert.NoError(t, err)
}
```

## Examples

### E-commerce Order Reminder

```go
func ScheduleOrderReminder(orderID string, userEmail string) error {
    client := timer.NewClient(os.Getenv("TIMER_SERVICE_URL"))
    
    req := &timer.CreateTimerRequest{
        ExecuteAt:   time.Now().Add(24 * time.Hour),
        CallbackURL: "https://api.mystore.com/order-reminder",
        Payload: map[string]interface{}{
            "orderID":   orderID,
            "userEmail": userEmail,
            "type":      "payment_reminder",
        },
        CallbackTimeout: 30 * time.Second,
        RetryPolicy: &timer.RetryPolicy{
            MaxRetries:   3,
            InitialDelay: time.Second,
            MaxDelay:     10 * time.Second,
            Multiplier:   2.0,
        },
    }
    
    _, err := client.CreateTimer(context.Background(), "orders", orderID, req)
    return err
}
```

### Subscription Renewal Reminder

```go
func ScheduleSubscriptionReminder(userID string, renewalDate time.Time) error {
    client := timer.NewClient(os.Getenv("TIMER_SERVICE_URL"))
    
    // Schedule 7 days before renewal
    reminderTime := renewalDate.Add(-7 * 24 * time.Hour)
    
    req := &timer.CreateTimerRequest{
        ExecuteAt:   reminderTime,
        CallbackURL: "https://api.myapp.com/subscription-reminder",
        Payload: map[string]interface{}{
            "userID":      userID,
            "renewalDate": renewalDate.Format(time.RFC3339),
            "type":        "subscription_reminder",
        },
    }
    
    timerID := fmt.Sprintf("sub-reminder-%s", userID)
    _, err := client.CreateTimer(context.Background(), "subscriptions", timerID, req)
    return err
}
```

## Best Practices

### Error Handling
- Always check for specific error types before generic handling
- Implement appropriate retry logic for transient errors
- Log errors with sufficient context for debugging

### Performance
- Reuse client instances across requests
- Use connection pooling for high-throughput scenarios
- Implement circuit breakers for resilience

### Security
- Use HTTPS in production
- Protect API keys and tokens
- Validate all input parameters
- Implement rate limiting on the client side

### Monitoring
- Add metrics for timer operations
- Track error rates and latencies
- Monitor retry patterns and success rates

## Contributing

See the main [SDK documentation](../README.md) for contribution guidelines and development setup. 