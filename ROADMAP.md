# Hermod Development Roadmap & Ideas

This document outlines the planned features, development directions, and future ideas for the Hermod platform to transform it from a "Data Mover" into a comprehensive "Data Orchestration Platform."

## 🚀 Enhanced Control Flow & Reliability (Implemented ✅)
- **Sequential Sink Chaining**: Introduced sequential execution for sink nodes. Currently, sinks execute in parallel. Sequential chaining allows Sink B to trigger only if Sink A returns a success.
- **Sinks as Transformers**: Sequential sinks return their output message to the next node in the pipeline.
- **Circuit Breaker with Intelligent Recovery**: Implemented a Circuit Breaker node that monitors failures and switches to a failure branch when a threshold is reached. Supports CLOSED, OPEN, and HALF_OPEN states.
- **Stateful Event Correlation (Join/Zip)**: Implemented a Join node that waits for messages from multiple sources based on a common key and merges them.

## 🤖 AI & Data Intelligence
- **Implemented ✅ in 1.18.0** — see [docs/ai-automation.md](./docs/ai-automation.md) and [docs/ml.md](./docs/ml.md):
  - **Semantic Routing**: the AI Classify node labels a message by intent or sentiment, and a router sends each label to its own branch.
  - **Natural Language Workflow Generation**: the workflow builder drafts a workflow from a plain-language description.
  - AI Prompt, Extract, Embed, Retrieve and Agent nodes; budgets and a kill switch per vhost; models trained, served and monitored for drift.
- **Autonomous Schema Evolution** (open): when a source schema changes (e.g., a new column in Postgres CDC), Hermod could automatically update the downstream SQL sink schema or alert the user with a "Smart Mapping" suggestion. The analysis exists (`AnalyzeSchemaChange`); nothing triggers it from a CDC schema change yet.

## 🏢 Enterprise Governance & Observability
- **Data Lineage & Impact** (Implemented ✅): A per-vhost graph of the assets every workflow reads and writes, derived from saved configuration, with the downstream impact of any source — see [Data lineage](docs/lineage.md). Still open: a heatmap overlay weighted by live throughput.
- **Hermod Edge Orchestration**: Lightweight Hermod instances for local filtering, masking, and pre-processing before sending data to a central Hermod cluster.

## ✨ "Zero-Entry" Ease of Use
- **Live "Dry-Run" Preview** (Implemented ✅): each node's settings show a Live Preview of how the sample from the node before it is transformed, re-run as the configuration changes.
- **Workflow "Blueprints" Marketplace**: An integrated store where users can download and share pre-built templates for common scenarios (e.g., "GDPR Compliance Pipeline," "E-commerce Sync").

## 🔗 Infrastructure & Connectivity
- **Expanded Connector Library**: Continued development of native, high-performance connectors for enterprise systems.
- **More 2PC-capable sinks**: The coordinator is **built** (`pkg/engine/twopc`), with a durable
  transaction log, a recovery path that resolves in-doubt transactions at start-up, and a reaper
  that sweeps ones left past their deadline. It is driven by the transactional sink group
  (`pkg/comm/sink/txgroup`), wired in `internal/engine/registry/txgroup.go`, and covered by unit,
  crash-point and live-PostgreSQL tests. See
  [Distributed transactions](./README.md#distributed-transactions-transactional-sink-groups),
  which also states the residual risk plainly.

  What limits it now is participants, not the coordinator: **PostgreSQL is the only sink that
  implements `hermod.TwoPhaseCommit`**, so a group can only span PostgreSQL destinations. A group
  refuses to start if any member cannot participate, rather than silently offering no atomicity.
  The next step is a second implementation — and closing the one window the README documents,
  which needs `hermod.TwoPhaseCommit` to accept a coordinator-supplied transaction ID.
- **Kafka exactly-once**: Worth separating into the two different things it could mean, because
  only one of them is reachable.

  **Exactly-once within Kafka** — transactional produce, with consumer offsets committed inside
  the same transaction — is **implemented**. The connectors moved from `segmentio/kafka-go` to
  `franz-go`, and a Kafka sink with a `transactional_id` writes each batch as one transaction that
  also commits the source consumer group's offsets, fenced by the group generation. A live-broker
  test kills the pipeline before a commit and after a rebalance and reads the output back
  read_committed: each record once. What remains open is the multi-sink case: the offsets commit
  with the Kafka sink's output, so a workflow with a second sink should not turn it on.

  **Kafka as a member of a transactional sink group** is *not* reachable, and no client library
  changes that. The coordinator's recovery path has to be able to commit a transaction that a
  previous process prepared. Kafka has no such operation: a producer restarting with the same
  `transactional.id` calls `InitProducerID`, which fences the old epoch and **aborts** whatever
  it left in flight. Abort is the only outcome recovery can choose, so `CommitPrepared` cannot
  be honoured and `hermod.TwoPhaseCommit` cannot be implemented truthfully.

  `pkg/comm/sink/kafka/twopc_test.go` already holds that line, and should keep holding it: it
  fails if the sink ever starts claiming `TwoPhaseCommit` or `Transactional`, because no-op
  methods would let a coordinator read a failed rollback as a successful one.
