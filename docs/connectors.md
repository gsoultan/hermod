# Connectors

Which connectors are production-ready, and the evidence behind each tier.

## Connector maturity tiers

Hermod ships 42 source and 47 sink connectors. They are **not equally mature**, and a
list that presents a 2,500-line integration-tested Postgres CDC reader next to an
87-line HTTP poller as equals is not telling you anything useful.

Tiers are assigned on evidence, not intent:

| Tier | Criteria |
| :--- | :--- |
| **GA** | Substantial implementation, unit tests, **and** an integration test that runs against live infrastructure. Suitable for production. |
| **Beta** | Substantial implementation with unit tests, but no live-infrastructure test. Expect to validate against your own environment first. |
| **Experimental** | Thin implementation, no tests, or a known semantic limitation. Suitable for prototyping. Do not put data you cannot lose behind one. |

**Every connector** is covered by the contract suite in `pkg/comm/conformance`,
which checks lifecycle, nil-safety and context-deadline behaviour with no live
infrastructure. That suite proves a connector is not obviously broken; it does
not prove the data path, which is what separates GA from Beta.

The one exception is the `txgroup` sink itself, a composite that wraps other
sinks. It has its own suite and its own PostgreSQL integration tests, which
cover more than the generic contract could.

Connectors needing a collaborator — `batchsql` a `DBProvider`, `form` a
submission store, `grpc` a `.proto` on disk — are constructed against a stub.
That is deliberate: what is being checked is the connector's own lifecycle and
context handling, and the stub is only the seam that lets it be built. Excluding
them left six connectors with no coverage at all in order to avoid a theoretical
weakness in coverage they did not have.

Connectors that address a fixed vendor host — Slack, Discord, Telegram, Twitter/X,
LinkedIn, Facebook, Instagram, TikTok, Pinecone — expose `SetBaseURL` so they can be
pointed at a test server. Without that seam they dialled the live internet on
construction, which is why they were untested; anything new in that shape should
provide it too.

A connector whose request URL carries a credential — Telegram's bot token in the
path, the Graph API's `access_token` in the query, a webhook URL that is itself the
secret — passes every request error through `httpclient.RedactURLError`. net/http
quotes the full URL in a failed request's error, and that error becomes the
workflow's status and the alert sent to every notification channel.
`TestConnectorErrorsDoNotCarryCredentials` holds every social connector to it.

### GA

| Connector | Direction | Evidence |
| :--- | :--- | :--- |
| **PostgreSQL** | source + sink | Logical-replication CDC, 2,572 / 1,448 lines, 13 test files, live-DB integration + bulk-load + idempotency + PgBouncer e2e |
| **MySQL** | source + sink | Live-DB integration tests, idempotency coverage (`MYSQL_DSN`) |
| **SQLite** | source + sink | Local-file engine, tested in-process |
| **File** | source + sink | 1,238-line source with tests; used throughout the e2e suite. `raw`, `csv` and `parquet` formats; parquet rows carry their CDC operation and key, so a file drives insert/update/delete |
| **RabbitMQ** | source + sink | Queue integration tests against a live broker, both directions: the sink's messages are consumed back off the queue |
| **Redis** | sink | Integration test against a live server |
| **MongoDB** | sink | Live-server data-path tests: documents land, a repeated key does not duplicate, updates replace, deletes remove, batches arrive whole |
| **MongoDB** | source | Live replica-set change-stream tests (`MONGODB_RS_URI`): the resume position advances on acknowledgement rather than on read, unacknowledged messages are redelivered after a restart, the initial load carries existing documents once, and a write during the backfill is not lost between it and the tail |
| **SMTP** | sink | Live send against a mail catcher, read back through its API (`SMTP_HOST` + `SMTP_VERIFY_API`) — an SMTP server accepts a message long before anyone can read it, so a nil return is a weaker claim than it looks. Retry and duplicate-suppression covered too |
| **Kafka** | source + sink | Live-broker round trip (`KAFKA_BROKERS`): a record written by the sink comes back out of the source with its key intact, and acknowledging advances the consumer group's offset so a restart is not handed what was already delivered. With a `transactional_id`, a pipeline killed before its commit and after a rebalance still delivers each record once to a read_committed consumer |
| **ClickHouse** | sink | Live-server tests (`CLICKHOUSE_ADDR`): an insert lands; a delete in a batch that also inserts does not come back, which it used to; mapped columns insert and delete; and a mapped column name cannot break out of its quoting |
| **MSSQL** | sink | Live-server tests against Azure SQL Edge (`MSSQL_DSN`): insert, upsert on redelivery and delete through the mapped path, and a column name that cannot be quoted is refused by name rather than becoming an empty identifier |
| **Elasticsearch** | sink | Live-server tests (`ELASTICSEARCH_URL`): a document is indexed and deleted, and a document id cannot inject its own actions into the bulk stream |
| **pgvector** | sink | Live-server tests (`PGVECTOR_DSN`): a vector is stored, upserted and deleted, and an identifier needing quotes gets them |
| **S3 / S3-Parquet** | sink | Live MinIO tests (`S3_ENDPOINT`): an object is put, distinct messages land separately, the default key keeps every delivery while the idempotent key does not leave a second object, a batch becomes a Parquet object, and one undecodable message is named rather than wedging the batch. The sink writes the CDC operation into the column named by `operation_field` when the schema declares one, so a delete does not land as another insert |
| **Oracle** | sink | Live-server tests against `gvenzl/oracle-free` (`ORACLE_DSN`, local rather than CI — the image wants ~2GB and a slow first boot): insert, upsert on redelivery and delete through the mapped path; a write into an existing, conventionally-named table; and the identifier guard asserted against the server, where an injected table name is refused and no table of that shape appears. Standing the server up is what found that the sink could not write to an ordinary Oracle table at all — identifiers were quoted PostgreSQL-style, but Oracle folds unquoted names to UPPER case, so every statement failed with `ORA-00904` |
| **Cassandra** | sink | Live-node tests (`CASSANDRA_HOSTS`): a row lands and a delete removes it, and a table name arriving on a message is refused rather than interpolated into CQL. The Cassandra **source** is a different matter — see Experimental |
| **MQTT** | source | Live-broker tests (`MQTT_BROKER`): a published message comes out of Read with payload and topic intact, and a 200-message burst arrives whole — the silent drop-oldest this source once had would have eaten its head |

### Beta

Substantial and unit-tested, but unproven against live infrastructure in CI:

**Sources** — MSSQL, gRPC, WebSocket, HTTP, BatchSQL, Excel, Metis, Metis External Task.
**Sinks** — Snowflake, HTTP, WebSocket, Failover, Panmail, Metis.

The MSSQL **source** is genuinely a CDC source — it reads `CHANGETABLE` and emits
updates and deletes — so what it lacks is coverage, not capability. The polling
sources that were previously listed here are a different case and have moved to
Experimental: see the inserts-only row below.

**Snowflake** carries one caveat the others do not. Its identifiers are validated
like every other SQL sink's, but no warehouse is reachable from CI, so that guard
is the only one in this repository never watched failing against a real server.
It has tests that run without one — they cover the refusal rather than the
resulting SQL. Oracle used to sit here too; standing a real server up moved it to
GA and found a total-failure bug on the way (see its row above).

**Panmail** is unit-tested against an in-process gateway that speaks the real
wire contract, including the two refusals that matter — a rate limit, which is
safe to repeat, and a full backlog, which is not — but no panmail gateway is
reachable from CI. Its one novel decision is covered by a test that fails when
the decision is inverted: a send whose outcome is *unknown* keeps its
idempotency claim, so Hermod's retry cannot mail a recipient twice, while a
refusal the gateway *stated* releases it. With idempotency off, that protection
does not exist and the sink says so in the error rather than looking routine.

**Metis** is the pair of connectors for a [Metis](https://github.com/gsoultan/metis)
BPMN 2.0 workflow engine, over the stdlib-only
[metis-sdk](https://github.com/gsoultan/metis-sdk). The **sink** turns a message
into one act on the engine — start a process instance, correlate a message into
an instance already waiting, or broadcast a signal — so a committed database
transaction is what begins the business process that answers it. The **source**
polls a project for process instances, human tasks or incidents, which is how
process history reaches a warehouse.

The **external-task source** is the third, and it runs in the opposite
direction from the other two: rather than watching the engine or driving it, it
*is* a step of the process. A service task marked with a topic is published by
the engine as work instead of being called out to, and this source locks that
work, hands it to the pipeline as a message whose data is the step's variables,
and completes the task when the message is acknowledged — sending back whatever
the pipeline produced as the step's output. The process then carries on to its
next node with that in scope. Because the engine publishes rather than calls,
nothing needs a route back in: a worker behind any firewall that can reach the
server can serve a step.

Both are unit-tested against an in-process engine speaking the real wire
contract; no engine is reachable from CI, which is what keeps them out of GA.
Two decisions are each covered by a test that fails when the decision is
inverted:

- The sink treats a **5xx as an unknown outcome, not a refusal**. Starting a
  process is not idempotent, and a 500 is an answer that says the engine broke —
  not that it broke *before* committing the instance. So the idempotency claim
  is kept and the retry suppressed, where a stated refusal (400, 401, 403, 404)
  releases it. This is where it differs from the panmail sink, whose refusals are
  all stated. With idempotency off, a retry may start a second instance of
  somebody's process, and the error says so.
- The source advances its persisted cursor on **Ack, not on Read** — the defect
  this repository has fixed in ten other polling sources. Reading moves a
  separate in-process position, which stops a running poll re-reading what it
  just handed out and is deliberately not persisted.
- The external-task source **distinguishes an acknowledgement from a success**.
  Hermod acknowledges a message it could not deliver but did preserve — a node
  that failed, or a sink that refused, parks it as a dead letter and then
  acknowledges so the source stops replaying it. Taken at face value that would
  complete the BPMN task, and the process would advance to its next step —
  approving the payment, shipping the order — on work that is sitting in a
  dead-letter queue. So a parked message fails the task instead, with the reason
  the pipeline recorded, and an operator gets an incident. The marker it reads
  is `_hermod_failed_at`, which the engine stamps on *every* park; the more
  obvious `_hermod_dead_lettered` is only set when a node failed, and a guard
  reading that one alone misses the sink-outage case entirely.

The external-task source has two properties to size before you rely on it. Its
**lock is the budget**: a task is locked when it is fetched, so a batch larger
than the pipeline can clear inside the lock duration ends in expired locks, and
acknowledging past the lock is refused rather than attempted — past that instant
another worker may hold the task. And because redelivery is the engine's job
rather than a persisted cursor's, **a task may be run twice**: one whose
pipeline succeeded but whose completion did not reach the engine is handed out
again, so the sinks downstream want to be idempotent.

The incidents stream has a limitation worth knowing before you rely on it: the
engine lists incidents per instance rather than per project, so the source finds
failed instances first and asks each one. That costs a request per failed
instance per poll, and it only sees instances still inside the configured scan
window.

**The Confluent Schema Registry format is Beta, even though Kafka is GA.** The
tier is set per *data path*, and `format: schema_registry` is a different one
from the Kafka connector carrying it. It has unit coverage of the wire format,
the client, both bounded caches, the formatter and the decoder, plus an abuse
suite and a fuzzer for the Avro decode — but every registry in those tests is an
`httptest` stub. Nothing here has been run against a real Confluent Schema
Registry, and a stub cannot disagree with you about subject naming, compatibility
checks, the `application/vnd.schemaregistry.v1+json` handling of a real server,
or Confluent Cloud's authentication. Validate it against your own registry before
putting data of record through it. Moving it to GA means adding that evidence,
not editing this paragraph.

**Kafka is GA for its data path, and Kafka to Kafka can be exactly-once.** Give
the Kafka sink a `transactional_id` and each batch is one Kafka transaction that
also commits the source consumer group's offsets: after a crash the input is
re-read from the last committed transaction, and an unfinished one is aborted
and never seen by a read_committed consumer. Kafka must be the
workflow's only sink, since a second sink that failed would not be handed the
records again: a workflow that reads Kafka and has a second sink next to a
transactional one is refused when it is saved and when it starts. Kafka still cannot join a transactional sink group, because
recovery there must be able to *commit* a transaction an earlier process
prepared and Kafka can only abort one. See [ROADMAP](../ROADMAP.md).

**TxGroup** stays here despite having live-PostgreSQL and live-MySQL tests that
cover more than most GA entries — commit landing in every member, rows staying
invisible after a crash between prepare and recovery, and a PostgreSQL + MySQL
group built from stored configuration. The two things this entry used to cite
have moved: PostgreSQL and MySQL/MariaDB now both implement
`hermod.TwoPhaseCommit`, though a group still cannot include any other sink; and
the window in which a crash stranded a prepared transaction the coordinator could
not name is closed (see [Residual risk, stated plainly](transactional-sink-groups.md#residual-risk-stated-plainly)).
The tier has not been revisited since. Read the operational hazard notes before
enabling it — a member left in doubt holds locks until it is resolved.

### Experimental

Thin, untested, or semantically limited. Specific caveats where they matter:

| Connector | Caveat |
| :--- | :--- |
| **Oracle** (incremental polling), **ClickHouse**, **MariaDB**, **YugabyteDB** (sources) | Watermark polling, **not** log-based CDC. (Oracle's **LogMiner** method is log-based and captures updates and deletes; see [Oracle CDC](oracle-cdc.md).) Inserts only — updates and deletes are invisible. The limitation is structural rather than a gap: these sources can only construct `OpCreate` and `OpSnapshot`, so there is no code path by which an update or a delete could reach a sink. A row changed after it was read is never re-emitted, and a deleted row is never retracted downstream. **MariaDB is the easiest to be caught by**: it has a binlog and Hermod's MySQL source does read one, so the MariaDB source looks like it should do CDC too. It does not. Use the MySQL source against MariaDB if you need updates and deletes. ClickHouse is listed here despite having live-infrastructure tests, because the tier is set by semantics rather than by coverage. |
| **DB2** (source) | Cannot connect in a release build: it needs the `go_ibm_db` driver, which needs CGO and IBM's native client library, and neither is compiled in. |
| **Cassandra**, **ScyllaDB** (sources) | Inserts only, as in the row above — updates and deletes are invisible. On top of that, CQL cannot `ORDER BY` an arbitrary column, so incremental polling returns an *arbitrary* qualifying row and the cursor can skip rows permanently. Sound **only** when the id field is a clustering column inside a restricted partition. The source logs a warning on first use. The Cassandra **sink** is unaffected and is GA. |
| **SAP** | OData polling client (~180 lines). No IDoc, BAPI or delta queues. OData is SAP's sanctioned direction for third parties after Note 3255746, but it is roughly 10× slower than ODP-RFC for bulk extraction. |
| **Mainframe**, **Dynamics 365**, **ServiceNow**, **Salesforce** | Thin REST/OData clients, no tests. |
| **Social / SaaS** — Slack, Discord, Telegram, Twitter/X, LinkedIn, Facebook, Instagram, TikTok, Google Sheets, Google Analytics, Firebase | Small API wrappers. Contract-tested, but no data-path coverage. Fine for notifications; not for data of record. **Their resume cursors advance on read, not on acknowledgement** — Twitter/X, LinkedIn, Facebook, Instagram and TikTok still carry the defect fixed in the thirteen database and OData sources: a crash between reading and the sinks writing loses those items from the resume, because the persisted cursor is already past them. They are left as-is deliberately rather than changed mechanically: each advances a vendor pagination token (`since_id`, `since`, an opaque cursor) whose semantics differ per API, and no test here can exercise them. Treat a restart as potentially lossy for these. Dynamics 365 and SAP were in this list and are now fixed — both compare a configured ID field with OData's `gt`, which is a watermark rather than a vendor token, so the same fix applied. |
| **FCM** (sink) | A full Firebase Cloud Messaging client: templated destinations and notification fields, multicast, per-platform Android/APNs/Web Push options, topic subscription, dry run. Its wire format is asserted against a stand-in FCM server, and refusals FCM calls permanent are reported as permanent rather than retried. Delivery is still at-least-once and FCM has no idempotency key, so batching is off by default — see [the sink's package comment](../pkg/comm/sink/fcm/fcm.go). |
| **Pinecone**, **Milvus**, **Kinesis**, **Pub/Sub**, **Pulsar**, **FTP**, **generic CDC**, **GraphQL**, **cron**, **webhook**, **form**, **batchsql**, **gRPC** | Minimal implementations. Contract-tested; no data-path coverage. |

GA describes the *data path* — that changes are captured and land correctly. It does
not mean a connector carries existing rows across when a workflow starts: only the
PostgreSQL source does, and only when asked. See
[Maturity, up front](../README.md#maturity-up-front).

Moving a connector up a tier means adding the missing evidence, not editing this
table. If you depend on one, an integration test is the most useful contribution you
can make.

## Connector reference

Every `type` value the factory can build, read out of `createSourceBase` and
`createSinkBase` in `internal/factory/factory.go`. Names are the `type` you set on a
source or sink, not display names. `TestConnectorReferenceMatchesTheFactory` fails
when this table and the factory disagree, so it cannot drift; regenerate it with
`go test ./internal/factory/ -run TestConnectorReferenceMatchesTheFactory -update-connector-doc`.

The `txgroup` and `failover` sinks are not here: they wrap other sinks and are built
by the workflow registry, not the factory.

The counts are of `type` values, so they differ from the connector counts at the top of
this page: some types share one connector, such as the `mysql` and `mariadb` sinks and
the `postgres` and `yugabyte` sinks.

<!-- BEGIN connector-types: generated by TestConnectorReferenceMatchesTheFactory, do not edit -->

44 source types and 50 sink types.

| `type` | Source | Sink |
| :--- | :---: | :---: |
| `cassandra` | ✓ | ✓ |
| `chat` | ✓ |  |
| `clickhouse` | ✓ | ✓ |
| `cron` | ✓ |  |
| `db2` | ✓ |  |
| `discord` | ✓ | ✓ |
| `dynamics365` | ✓ | ✓ |
| `elasticsearch` |  | ✓ |
| `eventstore` | ✓ | ✓ |
| `excel` | ✓ |  |
| `facebook` | ✓ | ✓ |
| `fcm` |  | ✓ |
| `file` | ✓ | ✓ |
| `firebase` | ✓ |  |
| `form` | ✓ |  |
| `ftp` |  | ✓ |
| `googleanalytics` | ✓ |  |
| `googlesheets` | ✓ | ✓ |
| `graphql` | ✓ |  |
| `grpc` | ✓ |  |
| `http` | ✓ | ✓ |
| `instagram` | ✓ | ✓ |
| `kafka` | ✓ | ✓ |
| `kinesis` |  | ✓ |
| `linkedin` | ✓ | ✓ |
| `mainframe` | ✓ |  |
| `mariadb` | ✓ | ✓ |
| `metis` | ✓ | ✓ |
| `metis_task` | ✓ |  |
| `ml_dataset` |  | ✓ |
| `mongodb` | ✓ | ✓ |
| `mqtt` | ✓ | ✓ |
| `mssql` | ✓ | ✓ |
| `mysql` | ✓ | ✓ |
| `nats` | ✓ | ✓ |
| `oracle` | ✓ | ✓ |
| `panmail` |  | ✓ |
| `pgvector` |  | ✓ |
| `postgres` | ✓ | ✓ |
| `pubsub` |  | ✓ |
| `pulsar` |  | ✓ |
| `rabbitmq` | ✓ | ✓ |
| `rabbitmq_queue` | ✓ | ✓ |
| `redis` | ✓ | ✓ |
| `s3` |  | ✓ |
| `s3-parquet` |  | ✓ |
| `salesforce` |  | ✓ |
| `sap` | ✓ | ✓ |
| `scylladb` | ✓ |  |
| `servicenow` |  | ✓ |
| `slack` | ✓ | ✓ |
| `smtp` |  | ✓ |
| `snowflake` |  | ✓ |
| `sqlite` | ✓ | ✓ |
| `sse` |  | ✓ |
| `stdout` |  | ✓ |
| `telegram` |  | ✓ |
| `tiktok` | ✓ | ✓ |
| `twitter` | ✓ | ✓ |
| `wasm` |  | ✓ |
| `webhook` | ✓ |  |
| `websocket` | ✓ | ✓ |
| `yugabyte` | ✓ | ✓ |

<!-- END connector-types -->
