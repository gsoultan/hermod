# HTTP Sink Webhook Examples

This directory contains examples of how to create Go HTTP servers that act as webhook receivers for Hermod's `HttpSink`.

## Examples

### 1. Simple Webhook Receiver
A basic receiver that logs incoming messages and responds to health checks.
Location: `examples/http-sink/simple/main.go`

**How to run:**
```bash
go run examples/http-sink/simple/main.go
```
The server will start listening on `http://localhost:8080/webhook`.

### 2. Authenticated Webhook Receiver
Demonstrates how to handle a webhook with a simple API Key authentication via custom headers.
Location: `examples/http-sink/auth/main.go`

**How to run:**
```bash
go run examples/http-sink/auth/main.go
```
The server will start listening on `http://localhost:8081/webhook`.

## Configuration in Hermod

To use these examples, configure an HTTP Sink in Hermod.

### For the Simple example:
```yaml
sinks:
  my_webhook:
    type: "http"
    config:
      url: "http://localhost:8080/webhook"
```

### For the Authenticated example:
```yaml
sinks:
  secure_webhook:
    type: "http"
    config:
      url: "http://localhost:8081/webhook"
      headers:
        X-API-Key: "your-secret-api-key"
```

## Payload Structure

When using the `JSONFormatter`, the payload sent to this webhook will look like this:

```json
{
  "id": "msg-123",
  "operation": "create",
  "table": "users",
  "schema": "public",
  "before": null,
  "after": {
    "id": 1,
    "name": "John Doe",
    "email": "john@example.com"
  },
  "metadata": {
    "source_type": "postgres"
  }
}
```
