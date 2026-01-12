# Hermod

Hermod is a high-performance, messaging system designed with SOLID principles and zero-allocation in mind.

## UI Management Platform

Hermod includes a web-based management platform built with React 19, Mantine UI, and Tailwind CSS. It provides a user-friendly interface for:
- Configuring Sources and Sinks.
- Creating and managing Connections.
- Visualizing data flow and monitoring logs.
- **Interactive Transformer Configuration**: Easily set up data transformations with built-in help and examples for renaming tables, filtering operations, and advanced field mapping.

### Getting Started with UI

1. Navigate to the `ui` directory:
   ```bash
   cd ui
   ```

2. Install dependencies:
   ```bash
   npm install
   ```

3. Run the development server:
   ```bash
   npm run dev
   ```

The management platform will be available at `http://localhost:5173`.

## Key Features

- **Multi-Tenant Support**: Support for multiple sources (via multiple engine instances) and broadcasting to multiple sinks per engine instance.
- **SOLID Principles**: Designed with clean interfaces and separation of concerns.
- **Reversible Roles**: Sinks can be sources and vice versa. Support for message brokers as sources and databases as sinks.
- **Zero Allocation**: Utilizes `sync.Pool` for message recycling to minimize GC pressure.
- **High Performance**: Optimized for heavy peak loads using efficient buffering.
- **Production Ready**: Built-in structured logging, retries with backoff, and health checks (Ping).
- **Programming by Interface**: Easy to extend and test with mockable interfaces.

## Project Structure

- `hermod.go`: Core interfaces (`Message`, `Producer`, `Consumer`, `Source`, `Sink`).
- `pkg/engine`: Engine that orchestrates data flow from `Source` to `Sink`.
- `pkg/message`: Implementation of recyclable messages.
- `pkg/source`: CDC and message broker source implementations.
  - Databases: PostgreSQL, MSSQL, MySQL, Oracle, DB2, MongoDB, MariaDB, Cassandra, Yugabyte, ScyllaDB, ClickHouse, SQLite.
  - Brokers: Kafka, NATS JetStream, Redis Streams, RabbitMQ Stream.
- `pkg/sink`: Message stream and database sink implementations.
  - Brokers/Streams: Stdout, File, NATS JetStream, RabbitMQ Stream, Redis, HTTP, Kafka, Pulsar, Pub/Sub, Kinesis, FCM.
  - Databases: PostgreSQL, MySQL, Cassandra, SQLite, ClickHouse, MongoDB.

### Source Specifics

#### MSSQL CDC
The MSSQL source supports automatically enabling CDC on the database and tables. This feature is enabled by default. To disable it, set `auto_enable_cdc: "false"` in the source configuration. This will:
1. Enable CDC on the database if not already enabled.
2. Enable CDC on each specified table if not already enabled.

### Sink Specifics

#### FCM (Firebase Cloud Messaging)

The FCM sink allows sending notifications via Google's Firebase Cloud Messaging. It requires a `credentials_json` string (the content of your service account JSON file).

Messages sent to the FCM sink must include one of the following in their metadata to determine the destination:
- `fcm_token`: Target device registration token.
- `fcm_topic`: Target topic name.
- `fcm_condition`: Target condition (e.g., `'TopicA' in topics && 'TopicB' in topics`).

Optional notification fields can also be provided in metadata:
- `fcm_notification_title`: Title of the notification.
- `fcm_notification_body`: Body of the notification.

- `pkg/formatter`: Message formatters (JSON).
- `internal/buffer`: High-performance in-memory and persistent messaging buffers.

## Transformations

Hermod supports a powerful transformation system that can be applied after reading from a source or before writing to a sink. Transformations can be simple operations or complex multi-step pipelines.

### Independent Transformations
Transformations can be defined independently and reused across multiple connections. This allows for centralizing logic like data masking, enrichment, or complex filtering.

### Multi-Step Pipelines
A transformation can consist of multiple steps executed in sequence:
1. **Filter**: Drop messages based on criteria (e.g., operation type).
2. **Enrich**: Call external APIs or query databases to add more data to the message.
3. **Map**: Restructure the data into a new format.

### Supported Transformers

- **Rename Table**: Renames the table or collection name in the message.
- **Filter Operation**: Filters messages based on the operation type (create, update, delete, snapshot).
- **Mapping**: Transforms the message payload (JSON) by renaming or selecting specific fields.
- **Advanced Mapping**: Powerful mapper supporting dotted paths, system variables (now, uuid), and constants.
- **HTTP Transformer**: Calls an external REST API. Supports template replacement in URL using message fields (e.g., `/users/{id}`).
- **SQL Transformer**: Queries a database to enrich the message. Supports parameter replacement (e.g., `SELECT * FROM profiles WHERE user_id = :id`).

Example configuration for a multi-step Pipeline:
```yaml
transformations:
  - type: pipeline
    steps:
      - type: filter_operation
        config:
          create: "true"
      - type: http
        config:
          url: "https://api.example.com/users/{user_id}"
          method: "GET"
          header.Authorization: "Bearer token"
      - type: advanced
        config:
          column.user_name: source.name
          column.user_role: source.role
          column.app: const.Hermod
```

Example configuration for Mapping transformer:
```yaml
transformations:
  - type: mapping
    config:
      map.old_field_name: new_field_name
      map.another_field: target_field
```

Example configuration for Advanced Mapping transformer:
```yaml
transformations:
  - type: advanced
    config:
      column.id: source.user_id
      column.full_name: source.name
      column.created_at: system.now
      column.app_name: const.Hermod
      column.status: const.1
```

## Architecture

Hermod works by reading data from a `Source`, buffering it in a high-performance buffer (in-memory `RingBuffer` or persistent `FileBuffer`), and then writing it to a `Sink`. This architecture allows it to handle peak loads and provide a flexible way to connect different databases to various message streams.

```
[Source] -> [Buffer] -> [Sink]
   ^           |          ^
   |        [Engine]      |
   +-----------+----------+
```

## Usage

### As a Library

```go
import (
    "context"
    "github.com/user/hermod/pkg/engine"
    "github.com/user/hermod/pkg/sink/stdout"
    "github.com/user/hermod/pkg/buffer"
    // ... import your source
)

func main() {
    src := // initialize your source
    sinks := []hermod.Sink{stdout.NewStdoutSink(), // ... other sinks}
    buf := buffer.NewRingBuffer(1024)

    eng := engine.NewEngine(src, sinks, buf)
    
    // Optional: Configure engine for production
    eng.SetConfig(engine.Config{
        MaxRetries:    5,
        RetryInterval: 200 * time.Millisecond,
    })
    
    eng.Start(context.Background())
}
```

### As an Application

Hermod can be run as a standalone application. By default, it starts in **API Mode**, which includes a web-based management platform for configuring sources, sinks, and engines.

1. Run the application:
   ```bash
   go run cmd/hermod/main.go
   ```

   This will automatically build the UI (if not already built) and start the Go backend. The UI assets are served from the binary (or disk in dev mode).

   If you want to force a UI rebuild:
   ```bash
   go run cmd/hermod/main.go --build-ui
   ```

   The UI will be available at `http://localhost:8080`.

   #### API Mode (Default)
   To start Hermod in API mode (which also serves the UI):
   ```bash
   go run cmd/hermod/main.go
   ```

   The UI will be available at `http://localhost:8080`.

   You can customize the port and database for storing state:
   ```bash
   go run cmd/hermod/main.go --port=8080 --db-type=sqlite --db-conn=hermod.db
   ```

   Hermod supports multiple databases for storing its state (Sources, Sinks, Connections):
   - **SQLite**: `--db-type=sqlite --db-conn=hermod.db`
   - **PostgreSQL**: `--db-type=postgres --db-conn="postgres://user:pass@localhost:5432/hermod?sslmode=disable"`
   - **MySQL/MariaDB**: `--db-type=mysql --db-conn="user:pass@tcp(localhost:3306)/hermod"`

   When running in API mode, Hermod saves its database configuration to `db_config.yaml` after the first successful setup or when updated via the UI. Subsequent starts will automatically use this configuration.

   #### Standalone Mode
   In Standalone mode, both the API/UI and a worker are started in the same process:
   ```bash
   go run cmd/hermod/main.go --mode=standalone
   ```

   #### Worker Scaling and Sharding
   Hermod supports horizontal scaling of workers. You can run multiple worker processes that share the same platform and automatically shard connections between them.

   To start a worker-only process connected to the platform:
   ```bash
   go run cmd/hermod/main.go --mode=worker --platform-url="http://localhost:8080" --worker-id=0 --total-workers=2
   ```

   - `--mode=worker`: Runs only the engine worker (no API/UI).
   - `--platform-url`: The URL of the Hermod platform API.
   - `--worker-id`: The unique ID of this worker (starting from 0).
   - `--total-workers`: The total number of workers in the cluster.

   Connections are automatically assigned to workers based on a hash of their ID. If the number of workers changes, the connections will be re-sharded across the available workers.

   #### Explicit Worker Assignment
   You can also register workers in the Hermod platform and explicitly assign Sources, Sinks, and Connections to a specific worker. This is useful when workers are running on different servers or in different vhosts.

   1. Register a worker via the API or UI. Each worker should have a unique GUID.
   2. Start the worker process with the `--worker-guid` and `--platform-url` flags:
      ```bash
      go run cmd/hermod/main.go --mode=worker --worker-guid="my-server-1" --platform-url="http://localhost:8080"
      ```

   #### Worker Self-Registration
   Instead of manually registering a worker in the UI, you can let the worker register itself upon its first run by providing additional flags:

   ```bash
   go run cmd/hermod/main.go --mode=worker --worker-guid="my-server-1" --platform-url="http://localhost:8080" --worker-host="192.168.1.10"
   ```

   - `--worker-host`: The hostname or IP address where the worker is running.
   - `--worker-port`: The port the worker is using.
   - `--worker-description`: Optional description of the worker.

   If a worker with the provided `--worker-guid` is not found in the database, it will be automatically created using the provided information. Name will default to the GUID.

   3. When creating or updating a Source, Sink, or Connection, specify the `worker_id` to pin it to that worker.

   If a component has a `worker_id` assigned, only the worker with the matching `--worker-guid` will process it. If no `worker_id` is assigned, the component is subject to the default hash-based sharding.


## Production Considerations

- **Logging**: Hermod uses a `Logger` interface and provides a default implementation using `zerolog` for zero-allocation structured logging. You can provide your own implementation via `eng.SetLogger(myLogger)`.
- **Retries**: The `Engine` automatically retries failed `Sink.Write` operations. Configure this via `eng.SetConfig`.
- **Health Checks**: Sources and Sinks implement a `Ping` method. The `Engine` performs pre-flight checks using `Ping` before starting.
- **Persistence**: For production use cases requiring absolute durability, use the `file_buffer` option. This ensures that even if the process crashes, messages read from the source but not yet written to the sink are persisted on disk.
- **Graceful Shutdown**: The `Engine.Start` method respects the provided `context.Context`. When the context is cancelled, the engine will stop reading from the source, signal the buffer to close, and wait for all pending messages in the buffer to be written to the sink before exiting. This ensures no data loss during normal shutdown procedures.

## Reliability and Data Loss Prevention

Hermod is designed to minimize data loss during operation and shutdown:

- **Graceful Draining**: During shutdown, Hermod drains its internal buffer to ensure all messages already read from the source reach the sink.
- **At-Least-Once Delivery**: The engine acknowledges messages to the source only after they have been successfully written to the sink. This ensures that if the process crashes, the source can re-deliver unacknowledged messages upon restart (depending on source implementation).
- **Retries with Backoff**: Failed writes to the sink are automatically retried with configurable exponential backoff.
- **Memory Safety**: Uses a bounded `RingBuffer` to prevent out-of-memory issues under high pressure.

**Important Note**: Since the default `RingBuffer` is in-memory, sudden process termination (e.g., `SIGKILL` or power failure) can result in the loss of messages currently held in the buffer. For use cases requiring absolute durability, consider implementing a persistent `Producer`/`Consumer` (buffer) interface (e.g., using a file-backed queue or a dedicated message broker).

## Benchmarks

```
BenchmarkAcquireRelease-12      56807960                22.36 ns/op            0 B/op          0 allocs/op
```