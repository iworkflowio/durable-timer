# Timer Service SDKs

Multi-language client SDKs for the distributed timer service, providing consistent APIs across 8 programming languages.

## Overview

This directory contains client libraries that enable applications to interact with the timer service from various programming languages. All SDKs provide consistent functionality while following language-specific idioms and best practices.

## Supported Languages

| Language | Directory | Status | Package Manager |
|----------|-----------|--------|-----------------|
| Go | [`go/`](go/) | ✅ Ready | Go Modules |
| Java | [`java/`](java/) | 🚧 In Progress | Maven/Gradle |
| Python | [`python/`](python/) | 🚧 In Progress | PyPI |
| TypeScript/JavaScript | [`ts/`](ts/) | 🚧 In Progress | npm |
| Rust | [`rust/`](rust/) | 🚧 In Progress | Crates.io |
| C# | [`csharp/`](csharp/) | 🚧 In Progress | NuGet |
| PHP | [`php/`](php/) | 🚧 In Progress | Composer |
| Ruby | [`ruby/`](ruby/) | 🚧 In Progress | RubyGems |

## Common SDK Structure

Each SDK follows a consistent structure:

```
sdks/{language}/
├── src/                   # Source code implementation
├── tests/                 # Unit and integration tests
├── examples/              # Usage examples and samples
├── docs/                  # Language-specific documentation
├── {build-config}         # Language-specific build configuration
└── README.md              # Language-specific instructions
```

## Core Functionality

All SDKs provide the following features:

### Timer Management
- **Create Timer**: Schedule a new timer with callback URL and payload
- **Get Timer**: Retrieve timer details and status
- **Update Timer**: Modify timer execution time, payload, or callback URL
- **Delete Timer**: Cancel a scheduled timer

### Configuration
- **Server Endpoint**: Configure timer service URL
- **Authentication**: API key/token support
- **Retry Logic**: Configurable retry policies for HTTP requests
- **Timeout Handling**: Request timeout configuration

### Error Handling
- **Typed Exceptions**: Language-specific error types
- **Status Codes**: HTTP status code mapping
- **Validation**: Client-side validation for requests

### Utilities
- **Shard Computation**: Automatic shard ID calculation
- **Callback Response**: Helper for callback endpoint implementation
- **Group Management**: Group-based timer organization

## Quick Start Examples

### Go
```go
import "github.com/iworkflowio/durable-timer/sdks/go"

client := timer.NewClient("https://timer-service.example.com")
timer, err := client.CreateTimer(ctx, "notifications", "user-123", &timer.CreateRequest{
    ExecuteAt:   time.Now().Add(1 * time.Hour),
    CallbackURL: "https://api.example.com/webhook",
    Payload:     map[string]interface{}{"userId": "123"},
})
```

### Python
```python
from timer_sdk import TimerClient
from datetime import datetime, timedelta

client = TimerClient("https://timer-service.example.com")
timer = client.create_timer(
    group_id="notifications",
    timer_id="user-123", 
    execute_at=datetime.now() + timedelta(hours=1),
    callback_url="https://api.example.com/webhook",
    payload={"userId": "123"}
)
```

### JavaScript/TypeScript
```typescript
import { TimerClient } from '@iworkflowio/timer-sdk';

const client = new TimerClient('https://timer-service.example.com');
const timer = await client.createTimer('notifications', 'user-123', {
  executeAt: new Date(Date.now() + 60 * 60 * 1000),
  callbackUrl: 'https://api.example.com/webhook',
  payload: { userId: '123' }
});
```

### Java
```java
import com.indeed.timer.TimerClient;
import com.indeed.timer.CreateTimerRequest;

TimerClient client = new TimerClient("https://timer-service.example.com");
Timer timer = client.createTimer("notifications", "user-123", 
    CreateTimerRequest.builder()
        .executeAt(Instant.now().plus(1, ChronoUnit.HOURS))
        .callbackUrl("https://api.example.com/webhook")
        .payload(Map.of("userId", "123"))
        .build());
```

## Authentication

All SDKs support multiple authentication methods:

### API Key
```bash
# Environment variable
export TIMER_API_KEY="your-api-key"

# Or configure in code
client.setApiKey("your-api-key")
```

### Bearer Token
```bash
# Environment variable  
export TIMER_AUTH_TOKEN="your-bearer-token"

# Or configure in code
client.setAuthToken("your-bearer-token")
```

## Configuration Options

Common configuration options across all SDKs:

| Option | Default | Description |
|--------|---------|-------------|
| `serverUrl` | Required | Timer service endpoint URL |
| `apiKey` | None | API key for authentication |
| `timeout` | 30s | HTTP request timeout |
| `retries` | 3 | Number of retry attempts |
| `retryDelay` | 1s | Delay between retries |
| `userAgent` | `timer-sdk-{lang}/{version}` | HTTP User-Agent header |

## Error Handling

All SDKs provide consistent error types:

- **`ValidationError`**: Invalid request parameters
- **`AuthenticationError`**: Invalid or missing credentials
- **`NotFoundError`**: Timer or group not found
- **`ConflictError`**: Timer ID already exists
- **`RateLimitError`**: Rate limit exceeded
- **`ServerError`**: Internal server error
- **`NetworkError`**: Network connectivity issues

## Testing

Each SDK includes comprehensive tests:

### Unit Tests
- Request/response serialization
- Error handling and validation
- Configuration management
- Utility functions

### Integration Tests
- End-to-end API interactions
- Authentication flows
- Error scenarios
- Performance benchmarks

## Development

### Building All SDKs
```bash
# From repository root
make build-sdks

# Or individually
cd sdks/go && make build
cd sdks/python && make build
cd sdks/java && make build
```

### Running Tests
```bash
# Test all SDKs
make test-sdks

# Or individually
cd sdks/go && make test
cd sdks/python && make test
cd sdks/java && make test
```

## Release Management

Each SDK follows semantic versioning and independent release cycles:

- **Major**: Breaking API changes
- **Minor**: New features, backward compatible
- **Patch**: Bug fixes, backward compatible

Release automation through GitHub Actions ensures consistent:
- Version tagging
- Package publishing
- Documentation updates
- Changelog generation

## Contributing

1. Choose your preferred language directory
2. Follow the language-specific development setup
3. Implement features following existing patterns
4. Add comprehensive tests
5. Update documentation and examples
6. Submit pull request

See individual SDK README files for language-specific contribution guidelines.

## Support

- **Documentation**: Language-specific docs in each SDK directory
- **Examples**: Working examples in `examples/` directories
- **Issues**: GitHub issues with language-specific labels
- **Discussions**: GitHub discussions for questions and feedback 