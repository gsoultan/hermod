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
- **Splitting and merging lines**: a node may feed several nodes (one line split into many) and several nodes may feed one (many lines merged into one). A node that several lines lead into runs once, after every line into it has either delivered or been ruled out by a branch that was not taken. When more than one line delivers, what each brought is merged into one message before the node runs (nested objects key by key; the node's `strategy` setting can ask for `overwrite` or `if_missing` instead), so a line split in two and drawn back together loses neither half. Simulate merges the same way, with the same rule (`traversal.MergeData`).

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

## Sub-flows

A **sub-flow** is a workflow you reuse inside other workflows: a shared address clean-up, a
validation, a write-and-audit. It starts at a **Sub-flow input** step instead of a source and ends
at one or more **Return** steps. Another workflow runs it with a **Run sub-flow** step.

- **Inputs.** The Sub-flow input step lists the fields the sub-flow takes, each optionally
  required. The Run sub-flow step maps the caller's fields onto them (`customer.zip → zip`). The
  sub-flow sees only those inputs, not the rest of the caller's record.
- **Outputs.** The Run sub-flow step maps fields of the result back into the caller's record
  (`normalized → customer.address`). With no outputs, the whole result is added under `subflow`.
- **Returns.** With more than one Return step, each named one (`valid`, `needs_review`) is its own
  way out of the Run sub-flow step. Every Run sub-flow step also has an `error` way out, taken by a
  failure inside the sub-flow that the sub-flow does not handle itself, with the caller's record as
  it was before the call.
- **Versions.** A Run sub-flow step pins a saved version of the sub-flow (creating it makes version
  1 and every save after makes the next), so changing the sub-flow never changes a caller until the
  caller picks the new version. The step lists the steps of the version it pins and says when a
  newer one is saved. "Latest saved" follows the sub-flow instead, and is picked up the next time
  the caller starts. A save of the sub-flow that would stop a caller on "Latest saved" from starting
  (an input it does not map, a return it branches on that is gone) is refused, naming the caller.
- **Used by.** The Sub-flow input step lists every workflow in the virtual host that runs the
  sub-flow, and the version each runs (`GET /api/workflows/{id}/callers`).
- **Turn selection into sub-flow.** Select steps on the canvas and choose **Turn N steps into a
  sub-flow**. They move into a new sub-flow, saved as version 1, and a Run sub-flow step pinned to
  it takes their place, wired as they were: each distinct way out becomes a Return, and an error
  edge becomes the step's `error` way out. You list the fields the steps read (its inputs) and the
  fields copied back (its outputs); the sub-flow sees nothing else, so list every field it needs.
- **How it runs.** When the caller starts, the pinned version's steps are spliced into the caller in
  place of the Run sub-flow step. A sub-flow therefore runs on the caller's worker, in the caller's
  run, with no network hop, and the source record is acknowledged only once every sink inside and
  outside the sub-flow has written. Run history lists the steps inside a sub-flow under the Run
  sub-flow step, with IDs such as `call~normalize`.
- **Delivery guarantees.** Everything checked about a workflow is checked with its sub-flows spliced
  in: commit together, the Kafka exactly-once rule, cycles. A sink inside a sub-flow writes with the
  caller's idempotency key plus the Run sub-flow step's ID, so the same sub-flow called twice writes
  two rows and a redelivery overwrites the rows it wrote the first time.
- **Limits.** A sub-flow cannot have a source, cannot be turned on by itself, cannot call itself
  (directly or through another sub-flow), and sub-flows nest at most five deep. A sub-flow must be
  in the same virtual host as its caller.

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
`{{secret.NAME}}` reference, which is resolved for the failing workflow's vhost. Every string
in the sample is PII-masked before it is sent.

### Performance recommendations

A running workflow's **Tuning** tab has a Recommendations section: what would make the workflow
faster or steadier, most urgent first, each with the numbers it was drawn from.

Hermod reads every running workflow once every 30 seconds and keeps the last two hours of those
readings in memory. A reading holds counters and timings only (runs, failures and run times per
step, sink buffer fill and breaker state, lag), never message contents. The window starts again
when the workflow's engine restarts, and is gone when the server restarts. Recommendations that
compare readings over time appear after about two minutes and 100 messages.

| Recommendation | When it appears |
| --- | --- |
| Slowest step | One step takes 40% or more of the time all steps spend on messages |
| Run a filter earlier | A step drops half or more of its messages after a slower step has already processed them |
| Sink is backed up | A sink's buffer is 80% full or more in half of the readings |
| Sink breaker keeps opening | A sink's circuit breaker opened during the window |
| Step keeps failing | 5% or more of a step's runs failed |
| Messages are being dead-lettered | Dead letters grew during the window |
| Step is stuck | A step has had work in flight for 60 seconds without finishing any |
| Source is falling behind | Lag is above 10,000 and still growing |
| Step is sometimes very slow | 95% of a step's runs finish within a time at least 8 times its median, and at least 100 ms |
| Lookup cache is off | A DB or API lookup has `ttl` set to `0` |
| Runs are not being recorded | Trace sampling is off |

Run times are counted in doubling buckets, so "95% of runs finish within" is an upper bound
accurate to a factor of two.

- `GET /api/workflows/{id}/insights` returns the report (anyone with access to the workflow's vhost).
- `POST /api/workflows/{id}/proposals` with `{"rule": "...", "node_id": "..."}` turns a
  recommendation Hermod can apply into a pending proposal (Editor and up). Today that is
  `lookup_cache_off` (sets `ttl` to `5m`) and `trace_sampling` (sets the rate to 1%). The
  request names the recommendation only; the server decides the change, and answers `409` if the
  workflow no longer has that problem.

Hermod never tunes a workflow on its own. A proposed fix is approved, versioned and rolled back
exactly like a self-healing proposal. After approval the proposal shows step time per message,
failures and throughput before the fix and since, once the workflow has run long enough to
compare. Only the newest applied fix is measured.

A sink's batch size is recommended but never proposed: it belongs to the sink connection, which
other workflows may share.

#### Explaining the recommendations with AI

An explanation in plain words is **off** unless `HERMOD_ADVISOR_AI_PROVIDER` is set, with
`HERMOD_ADVISOR_AI_MODEL`, `HERMOD_ADVISOR_AI_BASE_URL` and `HERMOD_ADVISOR_AI_API_KEY`. The key
must be a `{{secret("NAME")}}` reference, which is resolved for the workflow's vhost; a plaintext
key is refused and the feature stays off.

When it is on, editors see an "Explain in plain words" button above the recommendations
(`POST /api/workflows/{id}/insights/explain`, Editor and up). One press makes one model call.

- What is sent: the window's length and message count, and each recommendation's title, text and
  numbers, up to 8 KB. Step names are in that text. Message contents are never sent; the advisor
  does not hold them.
- What it can do: return text. It cannot change a workflow or create a proposal, and the page says
  which model wrote it.
- What it costs: each call is checked against and counted in the vhost's and the workflow's
  [AI budget](ai-automation.md), and answers `429` when the budget is spent. The answer is capped
  at 600 tokens. Each call is written to the audit log as `AI_EXPLAIN_INSIGHTS`.

#### What Hermod did about each database, and what needs you

The Tuning tab's **Databases** section lists each PostgreSQL database the flow reaches (its source
nodes, and the sources its DB Lookup and Execute SQL steps query), from
`GET /api/workflows/{id}/insights/databases`. It reads what Hermod tracks in memory, refreshes
every 10 seconds and connects to nothing, so anyone who can see the workflow can read it.

Each database has two columns:

- **Fixed by Hermod**: what Hermod already did by itself. When a test query waits for a
  connection, Hermod learns how many connections it really gets there, through PgBouncer or not,
  and keeps a quarter of them free for lookups (see [PostgreSQL CDC](postgres-cdc.md)). When the
  database had no connection free in the last hour, it says that lookups were kept moving.
- **Needs you**: only what Hermod cannot change itself, each saying what Hermod already does
  about it. A long query (5 minutes or more) of this flow names its step and points to the SQL
  builder's Speed check. Other flows' long queries on the same database are named. When the
  database gives Hermod fewer connections than it is set to use, it says to raise PgBouncer's
  `pool_size` or the role's connection limit, and that until then Hermod runs safely, only slower.

#### Checking a DB Lookup's query and indexes

The Tuning tab's **Lookup queries and indexes** section checks each DB Lookup step's query on
its source when an editor presses **Check lookup queries**
(`POST /api/workflows/{id}/insights/lookup-plans`, Editor and up, written to the audit log as
`LOOKUP_PLAN_CHECK`). It works whether or not the workflow is running. For each step it shows:

- the statement the step sends, with `$1`, `$2` where message values go;
- each table the plan reads, and how: through which index and of what type (btree, hash, GIN,
  ...), or by reading the whole table;
- what to change, with the SQL to do it. Hermod never runs that SQL. It is
  `CREATE INDEX CONCURRENTLY`, which builds the index without blocking writes but reads the whole
  table, so run it in a quiet hour and outside a transaction.

What it flags, on a table of 10,000 rows or more (a smaller one is read whole as fast):

| Seen in the plan | What it proposes |
|---|---|
| A function around the key, such as `TRIM(LOWER(col)) = ...` in a filter or a join. An index on `col` cannot serve it. | Compare the plain column (clean the values when they are written), or an index on the expression exactly as the query writes it. |
| `LIKE '%...'` or any `ILIKE` | A trigram GIN index (`pg_trgm`). |
| `jsonb @>`, `?`, `?|`, `?&` | A GIN index (`jsonb_path_ops` for `@>`). |
| A range (`<`, `>=`, ...) on a column whose only index is a hash index | A btree index. A hash index answers equality only. |
| Equality and range conditions no index serves | One btree index, equality columns first and range columns last. |
| An index that fits, which the planner still did not use | Nothing to create. It expects too many rows to match; if the value is rare, run `ANALYZE` on the table. |
| `date_trunc('day', col) = value` where `col` has an index | No index. The range `col >= value AND col < value + interval '1 day'`, which selects the same rows and uses the existing index. For a message value the upper bound is cast to the column's type, because a parameter added to an interval would otherwise be taken for an interval. |

How the check reaches the database: the query is prepared and planned with
`EXPLAIN (VERBOSE) EXECUTE` under `plan_cache_mode = force_generic_plan`, in a `READ ONLY`
transaction with a 5 second `statement_timeout`. The transaction is rolled back and the prepared
statement is dropped afterwards. The query is never executed, and a query with two statements is
refused by the server. It works through PgBouncer in transaction mode, with or without
`max_prepared_statements`. Each step's check is limited to 15 seconds, and four steps are checked
at a time.

Limits:

- PostgreSQL sources only, for now. Any other source says so.
- The plan is the *generic* one, made without the message's values, because a check has no
  message. A running lookup may get a plan made for its actual values, which can differ when
  values are very unevenly spread.

#### Checking a query's speed while writing it

The SQL builder of DB Lookup, Execute SQL and Batch SQL has a **Speed** tab and a **Check speed**
button in its toolbar (`POST /api/sources/query/speed-check`, Editor and up, written to the
audit log as `SQL_SPEED_CHECK`). It plans the statement in the editor exactly as the check above
plans a lookup: `{{.path}}` templates become bound parameters, nothing is executed, and a write
statement is safe to check because the transaction is read-only and rolled back. It shows the
same findings, and the planner's estimated cost of the statement.

When a finding comes with a rewrite (today, a `date_trunc` equality), the builder also shows the
whole statement rewritten in its own terms, templates included, with **Apply to the query** and
**Copy**. The rewrite is planned before it is offered, so one the database refuses is never
shown, and the page says how many times less work the planner estimates for it. The cost is the
planner's estimate in its own units, not a duration: the page never shows a time it did not
measure. **Undo** under the editor puts the original statement back.

PostgreSQL only, for now; on another database the tab says so.

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

### SQL steps have a time limit

A DB Lookup gives up on its database after **30 seconds** unless its **Timeout** setting
(Advanced tab, a Go duration such as `45s` or `2m`) says otherwise. The limit covers
everything the lookup waits on: the source's settings, a free connection in the pool, a
lock, and the answer, including a batched lookup's shared query. When it runs out the
query is cancelled and the message fails with `db_lookup: no answer from the database
within 30s`, so it takes the node's retry, `onError` and dead-letter path. A cache hit
waits on nothing and is not affected.

Without a limit a lookup that never got an answer, for example one queued in PgBouncer
for a server connection (`SHOW POOLS`, column `cl_waiting`) or blocked by a lock, held
its message until the stall watchdog restarted the workflow after 60 seconds, and every
message in flight then failed with `failed to execute lookup query: context canceled`.

When the wait was for a free connection rather than for the query, the lookup does not fail.
That covers Hermod's own long queries holding the database, and PgBouncer queueing for a
server connection. Hermod waits its turn and asks again, as it does for a busy sink, and the
**Runs** tab shows the step as having waited: for how long, and which workflows' steps held the
database at the time, each named when you can see that workflow. Only the step's final result
is saved, so a run whose lookup waited and then succeeded is not shown as failed. A lookup that
ran out of time with no sign of queueing still fails, because the query itself may be what is
slow.

Execute SQL has the same **Timeout** setting (its step 5, *Time limit*). It has no
default: empty means no limit.

### Previews run in a rolled-back sandbox

**Simulate**, **Run Preview** and the SQL builder's preview never commit. A preview run
carries a sandbox (`pkg/infra/sqlutil/sandbox.go`): Execute SQL, DB Lookup and SCD get
their database through `sqlutil.Use`, which outside a preview is the connection pool as
before and inside one is a single transaction on that pool, opened on first use and
rolled back when the run ends. Later steps in the same run see the rows earlier steps
wrote, because they share the transaction, and nothing reaches the real table.

- On PostgreSQL each statement runs behind a savepoint, so one failing step does not
  abort the transaction for every step after it. A step's own transaction (SCD) becomes
  a savepoint too, so its commit stays inside the preview.
- Statements that would escape the rollback are refused in a preview: `COMMIT` and
  `ROLLBACK`, more than one statement, opening a transaction, and DDL or procedure calls
  on MySQL, Oracle and Snowflake, which commit on their own.
- ClickHouse cannot roll back a write, so a preview there runs reads only.
- Lock waits are capped at 5 seconds for the preview's connection (`SET LOCAL
  lock_timeout` on PostgreSQL; MySQL and SQL Server are reset before the connection goes
  back to the pool).
- DB Lookup skips the shared lookup cache and batching in a preview, so it never answers
  with, or keeps, a row the preview is about to roll back.
- A preview that fails after a write still rolls the write back. When a node after an
  Execute SQL fails, the run ends there: the failure is reported on that node, the nodes
  after it are reported skipped rather than run on its output, the transaction is rolled
  back and its connection returned to the pool, and the same preview can be run again
  straight away.
