# Workflows

Control flow, fan-out, versioning and rollback, workflows as MCP tools, blueprints, and the advanced transformation nodes.

## Advanced Logic & Control Flow

Hermod provides a suite of advanced nodes for complex data orchestration:

- **Switch Node**: Branching logic based on message content. Supports multiple cases and a default path.
- **Foreach Node (Fan-out)**: Splits a single message into multiple independent messages based on an array field. Adds `_fanout_group`, `_fanout_index`, and `_fanout_total` metadata.
- **Collect Node (Fan-in)**: Synchronizes parallel branches from a `Foreach` node. Waits for all items in a group to arrive before emitting a single merged message.
- **Deduplicate Node**: High-speed, in-memory deduplication using rotating Bloom filters. Prevents processing of duplicate messages within a rolling window.
- **Wait Node**: Pauses execution for a specified duration (e.g., `10s`, `1h`). Durations > 30s are automatically suspended to the database for reliability.
- **Approval Node (HITL)**: Halts the workflow and creates a manual approval request. Supports custom form definitions (JSON) that users fill out in the Approvals UI.
- **Log Node**: Explicitly sends data or fields to the live logging system, helpful for debugging production workflows.
- **Error Branching**: All nodes support an `error` output branch. If a node fails, the engine automatically routes the message along the `error` edge if configured.

## Execution‑Level Fan‑out (Foreach Node)

Hermod supports an execution‑level Foreach node that splits a single message into multiple independent messages based on an array path in the message data.

- Configure in UI: Add a node of type `Foreach` and set the required "Array Path" (e.g., `items`, `data.rows`).
- Runtime semantics:
  - For each element in the array, the engine emits a new message downstream.
  - Each emitted message includes helper fields in `data`:
    - `_item`: the current array element
    - `_index`: the 0‑based index
  - And correlation metadata for observability/idempotency:
    - `_fanout_group`: original message ID
    - `_fanout_index`: current index (string)
    - `_fanout_total`: total number of items (string)
- Error handling: If the node fails (e.g., path missing or not an array), the engine routes the message along edges labeled `error` from the Foreach node.

Notes:
- This Foreach node is different from the legacy data‑level "foreach transformer" which materializes arrays inside a single message. The execution‑level node produces N downstream executions.

Default is 15000 ms. WAL mode and other safe pragmas are enabled by default.

## Workflow Versioning & Rollback

Every time you save a workflow, Hermod automatically creates an immutable version in the database. This provides a complete audit trail and enables safe, rapid recovery:

- **Immutable History**: View all previous versions of a workflow, including the author, timestamp, and a summary of changes.
- **One-Click Rollback**: Instantly revert a production workflow to any previous stable version via the **History** tab in the Workflow Detail page.
- **GitOps Readiness**: Versioning ensures that workflow configurations can be managed as code and safely promoted across environments.

### Self-healing proposals

The optimizer's self-correction gate never changes a workflow on its own. When a node keeps
failing, it stores a **proposal**: a small patch with the value each field has now and the value it
would get (today, more retries with a longer interval on the workflow's `max_retries` /
`retry_interval`, capped at 10 retries and 10s).

- `GET /api/workflows/{id}/proposals` lists them, newest first (Viewer and up).
- `POST /api/workflows/{id}/proposals/{pid}/approve` applies one through the normal update
  path: it is validated, saved as a new workflow version and can be rolled back from the
  **History** tab. If the workflow has changed since the proposal was made, it is refused with
  `409` and marked `stale` (Editor and up).
- `POST /api/workflows/{id}/proposals/{pid}/reject` closes it without changing anything.

AI mapping suggestions for nodes that keep failing validation are **off** unless
`HERMOD_SELF_HEALING_AI_PROVIDER` is set, with `HERMOD_SELF_HEALING_AI_MODEL`,
`HERMOD_SELF_HEALING_AI_BASE_URL` and `HERMOD_SELF_HEALING_AI_API_KEY`. The key must be a
`{{secret("NAME")}}` reference, which is resolved for the failing workflow's vhost. Every string
in the sample is PII-masked before it is sent.

## Workflows as MCP tools

Hermod serves the [Model Context Protocol](https://modelcontextprotocol.io) at `POST /api/mcp`
(Streamable HTTP, stateless), so an MCP client such as Claude or an IDE agent can use your
workflows as tools:

| Tool | What it does | Roles |
| --- | --- | --- |
| `list_workflows` | The exposed workflows you may see, with whether each can be run and whether it replies | any |
| `get_workflow_status` | Active flag, status, processed / error / lag counters | any |
| `run_workflow` | Sends `input` to the workflow's webhook source; a source in `response_mode: sync` answers with the workflow's result (`status`, `error`, `record`) | Editor, Administrator |

- **Opt-in per workflow.** Only workflows tagged `mcp` are offered. Others, and workflows in a
  vhost you cannot access, answer "not found".
- **Same authentication as the API.** Send a Hermod session token as `Authorization: Bearer ...`.
  There is no separate MCP credential and no anonymous access. The webhook's own API key and
  signature are not asked for: the caller is already an authenticated user.
- **Audited.** Every run is written to the audit log (`MCP_RUN_WORKFLOW`) and to the webhook
  request log, where it can be replayed.

## Workflow Blueprints & Templates

Hermod provides a library of pre-built "Blueprints" to jumpstart common data integration patterns. These can be imported with a single click and customized to your needs.

Examples include:
- **CDC to Elasticsearch**: Real-time synchronization of database changes to a search index.
- **API Aggregator**: Consolidate data from multiple external APIs into a single stream.
- **GDPR Masking & Routing**: Automatically redact PII and route high-value data to specialized sinks.

Browse the available templates in the `examples/templates/` directory or directly via the **Import Template** button in the Workflow Dashboard.

## Advanced Transformation Nodes

Beyond simple mapping and filtering, Hermod supports complex business logic within the pipeline:

- **WebAssembly (WASM)**: Execute logic compiled from Go, Rust, or C++ at near-native speed. Ideal for CPU-intensive transformations or proprietary algorithms.
- **Lua Scripting**: Embed lightweight, flexible scripts for dynamic data manipulation without external dependencies.
- **PII Masking**: Automatically discover and redact sensitive information (Credit Cards, Emails, SSNs) using a built-in regex-based scanner.
- **Stateful Aggregations**: Maintain running totals, counts, or windowed averages directly in the stream.
- **Database/API Lookups**: Enrich incoming messages by querying external databases or HTTP APIs in real-time.

### SQL Query Builder

The DB Lookup transformation and SQL sources/sinks share an interactive SQL Query Builder in the Workflow Editor (`ui/src/components/forms/SQLQueryBuilder.tsx`). It is optimized for writing and editing long queries:

- **Database Explorer**: Load tables, drill into columns (with types), and filter the table list. Click a table/column or the `+` icon to insert its name at the caret.
- **Caret-aware Quick Insert**: Keyword shortcuts (`SELECT`, `FROM`, `WHERE`, `AND`, `OR`, `JOIN`, `LEFT/INNER JOIN`, `GROUP BY`, `ORDER BY`, `HAVING`, `LIMIT`, `OFFSET`, `DISTINCT`) and the dynamic `{{.last_value}}` variable are inserted at the current cursor position instead of appended at the end.
- **Format Query**: A one-click formatter normalizes whitespace and breaks major clauses onto their own lines for readability.
- **Fullscreen Editor**: Expand the editor into a large modal for comfortable editing of long, multi-line statements.
- **Convenience**: Copy the query to the clipboard, see a live character/line counter, and run with `Cmd/Ctrl + Enter`.
