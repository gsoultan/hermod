# Changelog

Notable changes to Hermod, newest first. Dates are ISO-8601.

This file starts at 1.0.0. Everything published before it was withdrawn — see
[The releases before this one are gone](#the-releases-before-this-one-are-gone).

## [Unreleased]

### Spans and metadata reads that cost more than what they carried

Two per-message costs, both the same shape as the rest of the 1.7.0 work —
building something for a consumer that was not there.

**A span was allocated for every message and every node even with no
TracerProvider installed**, which is the default. otel has no way to ask "is
one installed", but it hands back the same default object until someone
replaces it, so identity answers the question: `tracing.Installed()` compares
the global provider against the one captured at package initialisation.
`tracing.StartSpan` returns the context untouched when nothing is installed,
and the span it hands back is the noop one already in the context — `End`,
`SetAttributes`, `RecordError` and `SetStatus` are all safe on it, so no caller
needs a branch.

The capture depends on nothing installing a provider from an `init()`. Nothing
in Hermod does, and `TestGateDetectsAnInstalledProvider` fails loudly if that
changes.

**Reading one metadata entry cloned the whole map.** `Metadata()` copies under
the read lock, and it must — handing out the live map would race with a
concurrent `SetMetadata` — but almost every caller wants one key
(`_outbox_id`, `_source_node_id`, `traceparent`, the delivery markers) and paid
for a copy of every other entry to get it: 11% of everything a workflow
allocated. `hermod.MetadataValue` does the lookup under the same lock. It lives
beside the interface because three packages had each grown a private copy of it
and a fourth was about to.

Per message through a real workflow, against the 1.7.0 baseline: **27.6
allocations in the engine alone** (was 50), and **60.6, 72.4 and 120.4 at 8, 32
and 128 columns** (were 116, 158 and 326).

The allocation budgets now refuse to run when a TracerProvider is installed in
the same test binary. Span creation is gated on one being present, so a
provider installed by an earlier test would quietly measure a configuration
production never runs in — the kind of order-dependence that makes a gate worse
than no gate.


### A healthy workflow rewrote its status rows every second, saying the same thing

The engine notifies on every status *write*, not on every status *change*.
`checkHealth` pings each sink once a second and calls `setSinkStatus` for every
one of them, and `SetEngineStatusUnless` publishes "running" over "running" —
each of which notifies. The registry's callback then writes a workflow row, a
source row and one row per sink, **synchronously, on the health-check
goroutine**. With two sinks that is 12 storage writes a second, per workflow,
for the life of the workflow, and the value written is almost always the one
already there.

The callback's own comment — "update status in storage as they change rarely"
— is the assumption it was written on, and it was not true.

The notifications themselves are left alone, deliberately. `BroadcastStatus` is
wired only to this callback and is the only thing that pushes per-workflow
status to the UI; unlike the dashboard, which has `runDashboardSampler` as a
floor, there is nothing behind it. Firing less often would freeze the editor's
live node metrics whenever the status strings happened not to move, which is
the healthy case. So the redundancy is absorbed at the storage boundary
instead: `statusWriteGate` remembers what each row holds and skips a write that
would store the same value.

A write that fails is un-recorded so the next tick retries it. Recording a
value storage had refused would drop that status permanently — the UI would
show the previous one for the life of the workflow, which is the opposite of
what a status row is for.

Not changed: `SetEngineStatusUnless` returns true when it *wrote*, not when it
*changed*. That reads like the bug, and it is what makes the engine notify on
an unchanged status — but it is what the method documents and what its test
pins, and the notification it produces is load-bearing for the UI. The waste
was never the notification; it was the writes behind it.


### Per-message tracing spans built attributes nobody read

`writeToSink` already deferred its span attributes behind `IsRecording()`.
Three more sites did not: `RunWorkflowNode` (per node, per message),
and the two `source.receive` spans in the registry's multi-source reader and
the engine's runner. With no TracerProvider installed — the default — that is
four or five attribute values, a slice and an option wrapper built per message
for a span that goes nowhere. `trace.WithAttributes` was 8.7 allocations a
message at 32 columns and is now absent from the profile entirely.

The two `source.receive` spans stamp the message with `tracing.Inject`, which
depends on the span context rather than on its attributes, so a non-recording
span stamps exactly as before. `TestSourceReceiveSpanStillCarriesItsAttributes`
is the guard, alongside the existing one for the write span.

`EvaluateConditions` also stopped formatting both sides of every condition
through `fmt.Sprintf("%v", ...)` before it knew which operator it was applying,
so a numeric comparison no longer pays for two strings it never reads: 250ns
and 5 allocations down to 165ns and 3. `stringify` has to agree with `%v`
exactly — a condition is a filter, so a formatting difference is a data
difference — and is held against it over NaN, the infinities, the 1e21
exponent threshold and float32 widening.

Allocations per message through a real workflow are now 89, 101 and 149 at 8,
32 and 128 columns, against 116, 158 and 326 before. Part of that drop is not
a speedup: the benchmark's own source fixture was formatting column names and
values per message — 64 `Sprintf` calls a message at 32 columns — which
inflated every figure and buried the pipeline's costs under the harness's. It
builds them once now, so the numbers measure what they claim to.

Not done, deliberately: the single field/operator/value condition form still
builds a slice and a map per message, about 4% of the total. Avoiding it means
handing callers a cached slice they could mutate, and one workflow's filter
silently rewriting another's is a worse failure than the allocation.

### The MySQL sink wrote one statement per message

`WriteBatch` opened a transaction and then executed one statement per message,
so a 2,000-row batch was 2,000 round trips. An insert-only batch into a single
table now goes as multi-row `INSERT ... VALUES (...),(...)`.

Against MariaDB 11.4 in a local container: **8.1x at 100 rows, 27.5x at 500,
46.9x at 2,000** (5,289 -> 42,579, 7,364 -> 202,567 and 6,571 -> 307,933
rows/s). The old path plateaus around 6,000 rows/s whatever the batch size
because it is round-trip bound rather than CPU bound — and that is measured on
localhost, so over a real network the gap is larger, not smaller.

`classifyBatch` is conservative by construction, mirroring the Postgres sink's:
the bulk path is taken only where per-row ordering cannot be observed, and
anything it cannot establish falls back to the ordered path. It declines a
batch under 50 rows, without mappings, using soft delete, with an operation
mode other than auto/insert, routed per message, or containing anything that is
not an insert.

One condition has no Postgres equivalent. `upsertMapped` drops an identity
column whose value is empty, *per message*, so two messages in one batch can
contribute different column lists — and emitting those as a single multi-row
INSERT would shift values into the wrong columns, with no error. The shape is
established from the first row and the batch is refused if any later row
disagrees.

Guarded by a differential against a real server: the same batch written both
ways must produce identical table contents, including last-wins on a key
repeated within one batch, and including a batch large enough to cross a chunk
boundary.


### A healthy pipeline wrote one database log row per message

`DatabaseLogger` had no level filter at all. Moving the per-write success line
from `Info` to `Debug` — done to stop a healthy pipeline producing an unbounded
log stream — silenced the process logger and changed nothing here: every
`Debug` line was still built and buffered for the `logs` table. Sampling was
the only brake, and it is off by default.

It is worse than a log-volume problem. The engine skips building a `Debug`
line's arguments only when the logger can say the level is off, and a logger
that cannot answer is assumed to want the line. `DatabaseLogger` could not
answer — so the payload measurement in that line marshalled **every message's
data map to JSON**, per message, to report a size for a row nobody reads. At 32
columns that was 754k allocations on top of the logger's own 295k, together
about 30% of everything a workflow allocated.

`DatabaseLogger` now honours `HERMOD_LOG_LEVEL` and reports `DebugEnabled()`.

Two more per-message costs went with it:

- **`ParseConditions` re-parsed its JSON for every message.** A condition node
  calls it before evaluating anything, so the same bytes were unmarshalled for
  the life of the workflow to produce the same answer: 1,126ns and 24
  allocations a message, now 117ns and 5. The parse is cached per distinct
  config, bounded and evicting, and the result is copied out so one workflow's
  filter cannot reach into another's.
- **A pooled message kept whatever it grew to.** `clear()` empties a map but
  keeps its bucket array, and re-slicing a buffer to `[:0]` keeps its capacity —
  which is the point of pooling, but it meant a single 8 MB payload or one
  10,000-column row pinned that footprint in the pool for the life of the
  process. Ordinary messages still reuse their allocation; anything past 1 MiB
  of buffer or 512 map entries is handed back.

Cumulative, end to end through a real `source -> condition -> mapping -> sink`
workflow, against the same baseline as the previous entry: **+30% throughput,
-69% bytes allocated, -69% allocations** (geomean over 8-, 32- and 128-column
rows; +71% throughput and -75% allocations at 128 columns). Throughput reads
between +30% and +50% depending on machine load; the allocation figures are
deterministic across runs.

The traversal's goroutine-per-node model was **not** changed. An earlier
reading of the engine profile put 75% of CPU in scheduler wait and blamed it —
but the engine benchmark runs no workflow, so it never exercised the traversal,
and in the workflow profile those symbols are mostly idle threads rather than
contention. Measured properly the whole traversal is about 2% of CPU, so the
code that owns the fan-out ownership contract was left alone. `Traverse` no
longer spawns a goroutine only to wait for it immediately.


Reading one field no longer costs a whole row.

`evaluator.GetValByPath` — the function under every transformation, router
condition and sink column mapping — marshalled the **entire** data map to JSON
and parsed it back with gjson to extract one field. Reading a field was
therefore O(row), and a sink with N mapped columns was O(N x row): 17.9us and
294 allocations for one field of a 128-column row, 106us and 1,763 allocations
to resolve a six-placeholder template. Twelve sinks resolve their column
mappings this way, at 44 call sites.

It now walks the map and normalises only the leaf, which is flat in row width:
32ns and one allocation, whatever the row. The round trip was not pure overhead
— it is what turns an `int` into a `float64` and a `[]byte` into a base64
string, and everything downstream is written against that shape — so the fast
path is held against the old implementation, kept verbatim as an oracle, over a
matrix of rows x paths, and falls back to gjson for anything it cannot
reproduce exactly.

Four more of the same shape, each a cheap number computed the expensive way and
then discarded, all found by allocation profile rather than by reading:

- The batching loop cloned every message's payload to add its length to
  `batchBytes` — a total nothing reads unless `batch_bytes` is configured, which
  is not the default. 1.86 GB of the 4.9 GB a 150k-message benchmark allocated.
- The per-write success log is at `Debug` and the default level is `Info`, but Go
  evaluates a call's arguments regardless, and one of them measured the payload —
  which for a message carrying a data map means marshalling it to JSON. 0.94 GB,
  produced and thrown away. Guarded by a level check now; `Payload()` also gained
  a `PayloadLen()` that reports the size without copying the bytes.
- A payload that cannot be a JSON object — a CSV line, a text body, a protobuf
  frame, which is every message from a file or queue source — took three decode
  attempts to establish that, one of which copied the whole body into a string to
  look at its first character. 0.83 GB.
- The trace propagator cloned the message's whole metadata map to read one
  header, twice per write.
- A regex router condition called `regexp.Compile` once per message: 1,931ns and
  69 allocations against 294ns and 4 with the pattern cached. The cache is
  bounded and evicts, because a condition value containing `{{ }}` is resolved
  against the message's own data first — so the pattern can be attacker-derived.

End to end through a real `source -> condition -> mapping -> sink` workflow:
**+19.8% throughput, −56.5% bytes allocated, −48.5% allocations** (geomean over
8-, 32- and 128-column rows, benchstat n=7, p=0.001). Numbers and method in
`BENCHMARKS.md`, which gains a workflow-level benchmark — the existing engine
benchmark runs no workflow at all, so it never touched the evaluator and could
not see any of this.

The write side got the same treatment. `SetValByPath` marshalled the map,
`sjson`-set one field, unmarshalled it all back, then cleared the map and
refilled it: 44us and 694 allocations to write one field of a 128-column row,
now 731ns and 2. Its fast path is narrower than the read side's, because the
round trip there also JSON-normalises every *untouched* value in the map — so
the targeted write is taken only when the map is already all-JSON-native,
which is exactly when that side effect would have changed nothing. Anything
else still goes the long way.

The parity matrix for it (8 map shapes x 34 value-and-path cases) caught sjson
and `json.Marshal` disagreeing about `[]byte`: sjson writes the literal string
where `json.Marshal` writes base64. The fast path now handles only value types
it has been proved equivalent for.

`SetValByPath` has no production caller — its only caller is a test-only
wrapper in `internal/engine/registry/registry_routing.go` — so nothing in a
running pipeline was paying that cost. It is exported from `pkg/`, which is
why it was made fast rather than deleted.

### A condition whose regex does not compile no longer drops every message in silence

`EvaluateConditions` starts `match` at false and swallowed the compile error, so
a pattern that does not compile is not a condition that matches nothing — it is
one that rejects *everything*, for ever, with no error and no log. A typo in a
router filter was indistinguishable from "nothing matched" while the workflow
stayed green and delivered none of its traffic. Same shape as the `7d` retention
parse that silently switched off the trace purge.

It is now caught in three places: the editor's validation flags it as an error
before the workflow is saved, the node fails loudly at run time so the engine's
normal failure path dead-letters the message, and the condition parser is shared
between the two so they cannot drift. A pattern containing a template token is
left alone — it is resolved per message, so there is nothing to judge up front.

### Trace recording can no longer take the process with it

Recording a trace step spawned a goroutine and armed a five-second timer, per
node, per message, with nothing bounding either. That costs nothing at the
default `trace_sample_rate` of 0 — but tracing gets switched on precisely when a
workflow is busy, and a five-node graph at 50k msgs/s is 250k goroutines and
250k timers outstanding against a recorder writing to PostgreSQL. Recording now
has a fixed number of slots and drops a step it cannot place, counted by
`TraceStepsDroppedCount()`. `internal/engine/registry` already made this call for
its own trace recording; the engine now matches it.

### Fixed: span recording in tests only worked for the first test that asked

`recordSpans` built a `TracerProvider` per test. otel's global tracer takes its
delegate exactly once per process and the engine's tracer is package-level, so
the first test to call the helper bound it and every later one silently recorded
nothing — surfacing as "no source.receive span was recorded; spans seen: []",
which reads like a defect in trace propagation rather than a broken fixture.
Reproducible on the old helper with `-count=2` on a single test.


A parquet file can now drive insert, update and delete, in both directions.

Parquet was write-only and operation-blind. There was no parquet source at all —
the generic file source parsed `raw` and `csv`, so a `.parquet` object arrived as
a block of bytes — and the `s3-parquet` sink wrote `msg.Data()` and nothing else,
so a delete landed as a row indistinguishable from the insert of the same record.
Replaying such a file re-created rows that had been removed upstream, and nothing
in the log said so.

The file source gains a `parquet` format. It reads column-wise in chunks of 1,024
rows and transposes them, so the file's own schema is whatever the producer wrote
and nothing is compiled in; it works over every backend the source already has
(local, HTTP, S3, FTP, SFTP). `op_field` names the column carrying the CDC
operation — spelled out or as Debezium's single letters, defaulting to
`operation` — and `key_field` names the column that becomes the message ID, which
is what a sink without column mappings targets the row by. A file with no
operation column is read as all inserts, which is what a plain data file is.

Two failures are refused rather than absorbed. An operation value the mapping
does not recognise stops the read instead of falling back to "insert", because a
misspelt `delete` that quietly becomes an insert is a row that comes back. And an
update or delete with no `key_field` configured is refused, because the synthetic
per-row ID it would otherwise carry produces a statement that matches nothing and
still reports success. Nested and repeated schemas are refused too: their values
do not line up one-per-row with the flat columns beside them, and reading them as
if they did would shift every later column onto the wrong record.

The `s3-parquet` sink writes the operation back out, into a column named by
`operation_field`. It is materialised from the envelope only when a column was
set aside for it, so a schema written before this keeps producing exactly the
columns it always has; a value the pipeline computed itself is left alone; and a
column named explicitly but absent from the schema fails at construction, while
the operator is still looking at the form, rather than silently dropping every
operation.

### Three S3 sinks that could not be configured from the editor

Found while wiring the above, all the same defect: a gate keyed to a name nothing
writes.

`S3SinkConfig` writes `s3_region`, `s3_bucket`, `s3_key` — the keys the S3
*source* reads. Both the `s3` and `s3-parquet` sink factories read `region`,
`bucket`, `key_prefix`, `access_key`, `secret_key` and `endpoint`, so an S3 sink
configured from the editor was built with every field empty, and SinkWizard's
Next button could never enable. The form now writes the keys the sink factory
reads.

The `s3-parquet` sink also had nowhere at all to put the parquet schema it cannot
write a single row without, and its requirements entry was keyed `s3parquet`
while every sink of that type is `s3-parquet` — so the gate that would have
caught the mismatch never ran. It now has its own form, and the entry is keyed by
the type sinks actually have.

The elasticsearch sink was gated on a `url` key neither its form nor its factory
has; both use `addresses`.

`sinkConfigCoverage` promised a check comparing the keys each form writes against
the keys its type is gated on, and described it in a comment, but no such check
existed — which is how all of the above survived. It exists now, along with one
that every requirements entry is keyed by a type some sink can actually have.


### Fixed — the workflow's Reliability Policy mostly did not do what it said

Four settings sit under Reliability Policy in the editor. The dead-letter sink
worked. The other three did not, and one of them destroyed the data it was
meant to protect.

**Dry-Run Mode consumed the source.** The dry-run branch returned "written"
from the sink write, so the engine acknowledged the message — which for a CDC
source is the replication slot advancing past a row nothing had written
anywhere. Enabling the safety feature was how you lost the data. It also sat
*below* the safe-mode and failed-validation diverts, both of which write to the
dead-letter sink, so a "dry" run performed real writes against a real
destination. A dry run now writes nowhere, the DLQ included, and acknowledges
nothing: the message stays on the source and comes back on the next read. It is
no longer counted as a sink failure either, so a preview cannot trip a circuit
breaker on a sink it never called, and the stall watchdog stands down for its
duration — outstanding work that never completes is the mode working, not a
wedge. The one exception is a resumed message, which has no source row left
holding it; declining to park that would destroy it rather than preserve it, so
it is parked and the log says why.

**Saving the policy did not reach the running engine.** The worker decided
whether to restart a workflow by comparing its name, vhost, dead-letter sink and
graph — and nothing else. Dry-Run Mode, Prioritize DLQ, the DLQ threshold and
all three retry settings were therefore inert on a running workflow: the editor
showed the Dry-Run badge while the engine carried on writing to production, until
something unrelated — a node edit, a failover, a stall recovery — happened to
restart it. Every field the registry reads when it builds an engine is now part
of that comparison, in one function that says so.

**The DLQ alert threshold was never evaluated.** The check lived inside the
engine's status-change callback, but dead-lettering a message only incremented a
counter; it changed no status, so the callback never ran. A pipeline parking
every message it received reported "running" and stayed silent. Crossing the
threshold is now an event in its own right, raised once by the message that
crosses it — once, because the count only climbs, and firing per message past
the line would have the registry write workflow, source and sink status rows to
storage for each one. The alert also no longer claims the queue holds N
messages: the count is the engine's own and resets on restart, so it says
"dead-lettered N since it started". The two tests covering this re-implemented
the registry's logic inside the test body and asserted on themselves, so they
could not fail when the real path broke; they now call it.

**Drain DLQ raced the read loop.** The button swaps the engine's source for a
priority wrapper under one lock while the pipeline reads and acknowledges
through the same field under none. The source is now read through a single
guarded accessor, on its own mutex rather than the engine-wide one, so the swap
cannot serialise the pipeline behind it.

**The editor guessed which sinks it could drain.** Whether "Prioritize DLQ on
startup" is available depends on the dead-letter sink being a type Hermod can
also read as a source, and the editor answered from a literal list of 25 sink
types. It had drifted both ways: four types it advertised are not sources, so
the checkbox was offered and the workflow then refused to start, and nine that
work were missing, so the checkbox was disabled and the feature unreachable.
The factory owns the list now and the editor asks it over
`GET /api/sinks/capabilities/dlq-recovery`, the same shape as the two-phase
capability endpoint. A test reads the case labels out of both factory switches
and fails if either moves without the list — which is how `ftp` and `s3` were
found missing and `scylladb`, a source that is not a sink, found in it.

Finally, the editor's "Dry-run (Full Execute)" menu item sent `dry_run: true` to
`/api/workflows/test`, a handler that decodes only `workflow` and `message` and
a simulation that never writes to a sink regardless. It did exactly what "Run
Simulation" does and promised an execution that never happened, so it is gone.
Running the real pipeline without writing is Dry-Run Mode, in Settings.


### Fixed — a Foreach (Fan-out) node fanned out into nothing

`ForeachNode` split a message into one per array item and returned all of them.
The traversal carried one message per node — a single slot per node id, and a
node fires once — so it delivered the first and silently dropped the rest. A
three-line order wrote one row and the workflow reported success.

Paired with a `collect` node it was worse than partial: collect waits for
`_fanout_total` items before it emits, and only one ever arrived, so the group
never completed and the sink was written to **zero** times, for every message,
with nothing in the logs.

Each extra fan-out message now walks everything downstream in a traversal of its
own, and that traversal's routed writes, inline-delivery and dead-letter
outcomes are folded back into the original — so acknowledgement still reflects
what happened to every item. The extras are walked one at a time rather than all
at once: an array field is upstream-controlled, and a goroutine plus a pooled
traversal per element turns a large array into a memory incident.

`ForeachNode.Execute` had full unit coverage and the traversal had its own
tests; nothing ran the two together, which is exactly where the messages were
being thrown away. The tests now start at the registry and assert on what the
sink received.

### Fixed — a fan-out cost memory in proportion to the square of the array

`Message.Clone` deep-copies every data value, and the foreach node cloned once
per item — so each of the N messages carried its own copy of the whole
N-element array it was iterating. Measured for a single message:

| array items | allocated | time |
| --- | --- | --- |
| 100 | 3.64 MB | 1.7 ms |
| 1,000 | 353 MB | 102 ms |
| 4,000 | 5.64 GB | 3.6 s |

Doubling the array quadrupled the memory. One 4,000-line order was enough to
exhaust a 7 GB runner, and the node had no bound at all — the width of a fan-out
was decided by an upstream row.

The node now clones one base, removes the iterated array from it, and clones
that per item. The N-1 items a given message never reads were the entire cost:
4,000 items went from 5.64 GB to 8.41 MB and from 3.6 s to 4.5 ms, 671x less
memory and 813x faster. A fanned-out message therefore no longer carries the
source array, only its own `_item` and `_index`; **Carry the source array on
every message** opts back in, and back into the old cost.

`maxItems` bounds the width, defaulting to 10,000. Exceeding it **fails** the
node with an error naming the actual length, the cap and the setting that raises
it — so the message dead-letters rather than fanning out part of itself.
Truncating would have been a partial write to every downstream sink with nothing
to distinguish it from a short array, which is the failure this whole change set
is about. Both settings are in the node's editor.

A nested `arrayPath` is removed at its own level: dropping `order.lines` by its
first segment would have taken `order.id` with it.

### Fixed — nine node types opened a settings panel with no editor in it

`WorkflowNodeSettingsModal` chose what to render from a literal list of node
types, and the list had drifted from the config registry. `foreach`, `collect`,
`wait`, `join`, `circuit_breaker`, `approval`, `log`, `deduplicate` and
`multicast` all have a registered editor and none of them was on it, so clicking
one of those nodes gave a title, a Remove button and nothing else.

For foreach that made the node impossible to use: `arrayPath` is required, there
was no field to type it into, and workflow validation then flagged the node as
unconfigured with no way to act on it. The modal now reads the registry, so
registering an editor is all it takes for a node type to be configurable.

### Fixed — schema propagation read a config shape the editor does not store

`useWorkflowInitialization` hydrates a saved node as
`data: { ...node.config, ref_id }` — the config is spread *flat* onto `data`,
which is why `TransformationForm` passes `config: selectedNode.data`. Schema
propagation in `useNodeContext` read `node.data.config`, so for every workflow
loaded from storage it read an empty object and contributed nothing: a
`targetField` set on any upstream transformation never appeared in Available
Fields. It now reads the flat shape, keeping the nested one as a fallback for a
node built in memory.

### Fixed — Available Fields stopped at a fan-out node

Schema propagation in the editor knew about `targetField` and pipeline steps and
nothing else, so everything downstream of a foreach node listed the source's
columns and no way to address the item. It now offers `_item` and `_index` past
a `foreach` node — including the element's own fields, read out of the upstream
sample — the collected batch and `_count` past a `collect` node, and the
materialised array past a foreach/fanout transformation. It also *removes* the
array a foreach consumed, since the node no longer carries it: offering `lines`
downstream of the split would offer a path that resolves to nothing at run time.

These paths are deliberately not `after.`-prefixed the way a transformation's
`targetField` is: the engine writes them with `SetData`, and
`GetMsgValByPath` reads the data map before any CDC envelope, so `_item` is what
resolves at run time on a CDC message too.

### Fixed — one editor, two different foreach nodes, one description

`ForeachConfig` serves both the `foreach` node type and the foreach/fanout
*transformation*, and described only the first. A user who took **Foreach /
Fanout** from Common Transformations was told downstream nodes would run once
per item and that `_item` and `_index` would be there; that path splits nothing
and adds neither. It now describes the node it is actually editing, points at
the other one, and exposes the transformation's own settings — result field,
item path, index field, limit and drop-when-empty — which were reachable only by
hand-editing a bundle.


### Fixed — a Character Map node did nothing

The editor's Operation select wrote the chosen operation under `op`. The node
read `operations` and `operation`, and never `op`, so its operation list came out
empty, the loop applied nothing, and the field was written back exactly as it
arrived. A green node, no error, nothing in the logs — and since the editor is
the only way to build a Character Map node, that was every one of them.

The node now also reads `op`, which repairs existing nodes without touching
them, and resolves the keys in a fixed order — `operations`, then `operation`,
then `op` — so a config holding more than one behaves the same way every time.
The editor writes `operation` from now on and clears the stale `op` as it goes.

The node had no tests at all, which is how a selector that was wired to nothing
survived. It has them now, including one that pins every operation the editor
offers to one the node implements.

### Added — one `data_conversion` node converts several fields

A `data_conversion` node held one field and one target type. Retyping five
columns meant five chained nodes, each with its own field name to keep in step
and its own error setting to remember — and the editor gives no hint that
chaining is what you are supposed to do.

The node now holds a list of conversions. Each row has its own field, target
type, date format, separator, element type and target field, so one node can
send `amount` to Float, `qty` to Integer, `created_at` to Date and `tags` to an
Array of UUIDs. **Add Conversion** adds a row; the bin icon removes one.

**On Error** is now a node-level default that any row may override, which is the
case chaining was really being used for: fail hard on a key column while letting
an optional one go null. A row left on *Use node default* follows the node.

Rows apply in the order shown, and each one reads the message as it arrived
rather than as the row above it left it — so a row's result never depends on
where it sits in the list. Nothing is written until every row has resolved: a row
that fails under *Fail* leaves the message exactly as it came in, instead of
handing a half-converted row to the sink on a workflow set to continue on error.

Existing nodes are unchanged and keep working; the editor shows a stored
single-field conversion as the first row and migrates it on the first edit.

### Fixed — typing a field name in a `set` node scattered it across other rows

A `set` (and `advanced`) node stores its mappings as flat `column.<path>` keys,
so the row order in the editor is nothing but object key order. Renaming rebuilt
that object as `{ ...others, ['column.' + newPath]: value }`, which dropped the
edited key and re-appended it last. The Target Path input fires per keystroke, so
each character sent its row to the bottom of the list — and because rows are
keyed by position, the caret was left in whichever row had moved up into it.
Typing `_id` into the first of three rows produced `alpha_`, `betai`, `gammad`.

- **Renaming now rewrites the key in place**, so nothing moves and the caret
  stays put.
- **Renaming onto a path another row already holds no longer merges the two.**
  A config object can only hold the key once, so the write silently discarded one
  row and its value. The typed text stays on screen, the collision is named, and
  it commits as soon as the path is unique again.
- **"Add Field" no longer overwrites an existing row.** The generated name was
  `new_field_<count>`; delete a middle row and the count points at a name that is
  still taken, so the button appeared to do nothing while replacing that row's
  value. It now picks the first free name.
- Both inputs in a row carry an accessible name, so they are reachable without
  relying on visual order.

### Fixed — a `set` node applied its fields in a different order each message

`Prepare` collected the `column.*` entries by ranging a Go map, which is
randomised per range, and a `set` node applies them one at a time. Two columns
touching the same path — or one whose expression reads what another just wrote —
therefore resolved differently from message to message inside a single run, with
nothing wrong in the config and nothing in the logs. They are now sorted by path,
which also puts a parent path before the child that writes into it. The preview
endpoint's unprepared-config fallback shares the same parser, so a node cannot
resolve one way in the editor and another in the engine.

An `advanced` node evaluated into a map and then ranged *that* map to write the
results out, so fixing the evaluation order alone was not enough. `SetData` nests
a dotted path, so overlapping paths gave two different messages from one node and
one input — measured over 300 runs of a node with `column.a` and `column.a.b`,
266 came out `{"a":{"b":"child"}}` and 34 came out `{"a":"parent"}`. The results
are now written in the order they were evaluated in.

The editor's row order cannot be used for this: the config is stored as JSON,
which has no key order, so it is already gone by the time the engine sees it. A
fixed order is what is available, and it is what makes a pipeline that works in
test work in production.

### Added — `data_conversion` converts to JSON for a `json`/`jsonb` column

An object built in a `set` node, or read from a document source, had no way into
a JSON column outside PostgreSQL: `database/sql` rejects a `map[string]any`
outright, and the PostgreSQL sink's own marshalling only runs where it already
knows the column type. The new **JSON / JSONB** target type renders any value as
JSON text.

Text that already holds a JSON object or array passes through unchanged, so it is
not double-encoded into a JSON string; anything else is encoded, so `"123"` stays
the text `"123"` rather than becoming the number `123` — converting to a number is
what the Integer type is for. Text that *opens* like JSON but does not parse is an
error subject to On Error, rather than being quoted and stored: a truncated
payload is a real failure mode and storing `"{\"a\":1"` as a success is not a
useful answer. It is available per element of an Array conversion too, for a
`jsonb[]` column.

### Fixed — `execute_sql` could do nothing and report success

It has no cache and cannot serve a stale answer — it re-resolves its template
against the current message every call and keeps no state between them — but
both ways it could quietly write nothing were open:

- **A node missing `sourceId` or `queryTemplate` returned the message unchanged
  with a nil error.** On the one transformer that exists to *write* rows, that
  leaves the pipeline green and the table empty. It is now an error naming the
  field at fault.
- **A `{{ }}` token that resolved to nothing was bound as NULL and the statement
  ran.** That is correct for an optional field and indistinguishable from a
  typo, and on a write it means a statement that changes nothing, silently,
  forever. The default is unchanged — NULL is the fail-safe direction — but a
  node can now set `onUnresolved: "fail"` to reject it instead, and the editor
  offers the choice.
- **`affectedRowsField` was unreachable from the editor.** The transformer has
  always written the statement's affected-row count into a message field when
  that option is set, and it is the only signal a write node can give about what
  it did — without it, a node that changed nothing looks exactly like one that
  changed a thousand rows. The panel now offers it.

`execute_sql` still does **not** refuse a CDC source, unlike `db_lookup` and
`batch_sql`. That stays deliberate: their guard is about read load, which applies
whatever table is read, while the risk here depends on whether the *target* table
is in the publication — something the node config cannot know. Writing an audit
row nobody streams is legitimate. The editor already warns and names the loop
risk, which is the right layer for a judgement call.


## [1.6.0] — 2026-09-17

This release is mostly about caches that kept serving an answer which was right
once. One bug shape in three places — a key built from the *template* rather
than from the values it resolves to — meant a `db_lookup` handed the first
message's row to every message after it, an `api_lookup` handed one caller's
response, and one caller's credential, to another, and a batched lookup filtered
every message by the first message's values. Neither lookup's Cache TTL was
honest either: a value with no unit was discarded, leaving a cache that never
expired.

Beside those, SQL templates learn list variables so `IN ({{.ids}})` finally has a
working form, `data_conversion` converts to and from lists and UUIDs, `batch_sql`
sources take query parameters, sharding keeps the rows it was meant to keep
together, and seven list endpoints stop paging without an `ORDER BY` — which
could repeat a row on one page and skip it on the next.


### Changed — a `db_lookup` with no Cache TTL now expires after an hour

It used to never expire. `SetLookupCache` reads a ttl of zero as "no expiry" and
the editor's Cache TTL field is empty by default, so once the cache-key defect
below was fixed a workflow served the *right* row — and then went on serving it
after the row had been edited, for as long as the process lived. An enrichment
lookup that can never observe a change to the table it enriches from is a
snapshot, not a cache.

An hour rather than `api_lookup`'s five minutes: a remote HTTP response is
volatile and the call is somebody else's cost, while a lookup table is usually
slow-moving reference data and the query lands on your own database. The point
is turning "never correct again" into "correct within the hour", not minimising
staleness.

The extra query load is bounded from both sides — the cache holds at most 10000
entries, so a workflow with more distinct keys than that is already re-querying
through eviction, and one with fewer costs at most 10000 re-queries an hour.
**If you were relying on the old behaviour, set a long duration** (`87600h`);
`0` still means "do not cache", and the editor now states all three.

### Fixed — every message got the first message's `db_lookup` row

A `db_lookup` in query mode enriched message one correctly and then handed
message one's row to every message after it, however different its key. The
lookup cache was keyed on the *unresolved* `queryTemplate` and `whereClause`
text, so for a node like

```json
{"mode":"query","targetField":"User",
 "queryTemplate":"SELECT * FROM iam.users WHERE id = {{.UserId}}"}
```

every message in the workflow produced a byte-identical cache key — the
per-message input lives entirely inside the `{{ }}` token, and such a node has
no `keyField`, so the key's one message-derived component was `nil` as well.
An unset `ttl` means the entry never expires, so the first row was served for
the lifetime of the engine.

The key now carries a digest of the values the template actually binds,
produced by the same walk that builds the statement (`sqlutil.TemplateArgs`),
so the two cannot drift. A lookup with no `{{ }}` token keys and costs exactly
what it did before.

This is also why the SQL query builder and the node preview disagreed on the
same variable: the builder runs its query directly and never consults the
lookup cache, so it returned the right row while the pipeline returned the
cached one.

### Fixed — `api_lookup` served one caller's response to another

`api_lookup` resolved its URL and body before keying on them, so it never had
the total key collapse above. Three inputs were applied *after* the key was
built and so never reached it:

- **templated headers** — two tenants sharing an endpoint, distinguished only
  by `{"X-Tenant-Id":"{{.tenant}}"}`, shared one cache entry;
- **the templated credential** — a per-message `{{.userToken}}` returned
  whatever the *first* token had fetched. A cache that ignores who asked is a
  disclosure, not just a staleness bug;
- **`responsePath`** — it decides what is extracted and therefore what is
  stored, so two nodes reading different fields of one endpoint collided.

Because the cache key is also the singleflight key, a concurrent pair of
messages also shared one HTTP request, so the second was answered with the
first's response even on a cold cache.

Headers and credentials are now resolved once, before the key, and folded into
it as a SHA-256 digest — never in the clear, since the lookup cache is an
in-memory map whose keys are walked during eviction. Requests that really are
identical still share an entry. Resolving once also stops the retry loop
re-parsing the header JSON on every attempt.

### Fixed — a batched `db_lookup` filtered every message by the first message's values

`getOrCreateBatcher` built its closure once per node id and then handed the same
one back forever, so whatever the first message supplied was frozen into it:

- **A templated `whereClause` was resolved once.** Every later batch was
  filtered by the first message's values — `tenant = {{ .tenant }}` meant every
  message in the workflow was looked up in the first message's tenant. Batching
  coalesces many messages into one query and a per-message WHERE cannot be
  coalesced, so such a node is no longer batched at all; it takes the
  per-message path, which resolves its templates for the message in hand.
- **The source was frozen too.** Repointing the node at another database, or
  rotating its credentials, left the batching path querying the old one until
  the process restarted — defeating the cache invalidation the registry
  performs on a source edit for exactly this reason. Batchers are now keyed by
  a fingerprint of everything the closure captures, so a reconfigured node gets
  a fresh one. The superseded batcher is dropped rather than closed: closing it
  would make a concurrent `Execute` fail, turning a reconfiguration into failed
  messages.
- **`batchSize` ignored a JSON number.** It was read with an `.(int)` assertion
  and a string fallback, neither of which matches the `float64` that JSON
  decodes to, so a configured size silently fell back to the default of 100.

`db_lookup`'s Cache TTL also gained the parsing `api_lookup` did: a value with
no unit is an error naming the field instead of silently meaning "cache
forever", and an explicit `0` disables the cache. Unlike `api_lookup`, an unset
TTL still means no expiry — a lookup table is routinely static reference data,
and changing that default would add a query per message to every existing
workflow.

### Fixed — `api_lookup`'s silent failures

Everything `db_lookup` was given and `api_lookup` was not:

- **A miss is now a decision.** A call that returned 2xx with nothing at the
  response path, and a node too incomplete to make a request, both returned the
  message unchanged with a nil error — so the sink could not tell an enriched
  message from an un-enriched one. Both now go through the `onMiss` policy
  (`passthrough` / `default` / `fail`), which the API Lookup editor now offers.
  A *failed* request keeps its existing behaviour by default — reported when
  there is no Default Value, substituted when there is — but `fail` now
  overrides a Default Value, so "fill in empty responses, yet still fail the
  message on a 500" is expressible for the first time.
- **Cache TTL is bounded and parsed.** The parse error was discarded, so `5` or
  `300` — what people type into a box labelled Cache TTL — left the zero value
  behind, which `SetLookupCache` reads as *never expires*: the field whose only
  purpose is bounding staleness silently unbounded it. An unparseable value is
  now an error naming the field, an unset TTL means 5 minutes rather than
  forever, and `0` disables the cache, which was previously inexpressible.
- **Malformed `headers` or `queryParams` JSON is reported.** A typo used to
  drop them and send the request anyway — unauthenticated, or unfiltered,
  against a real endpoint, reported nowhere.
- **Max Retries works.** The editor's control is a `NumberInput`, so it saves a
  JSON number, and every reader went through `GetConfigString`, which returns
  `""` for a non-string. A retry count set in the editor produced exactly one
  attempt. Numbers and strings are both accepted now.
- **Requests use `httpclient.DataClient`** rather than `http.DefaultClient`,
  which brings the project's connection pooling and dial timeouts. `DataClient`
  is the right one here: it performs no SSRF check, because an `api_lookup`
  pointed at an internal address is an ordinary thing to configure.

### Fixed — the `router` trace step showed the pipeline's output before the pipeline ran

For a node-graph workflow the engine's router *is* the traversal — the whole
DAG runs inside it — so the `router` trace step captured its payload after the
last node while carrying the timestamp routing began. Message traces order
steps by timestamp and rebuild each step's "before" from the previous step's
"after", so the finished payload appeared between the message arriving and the
source node emitting it, and the source node then looked as though it had
deleted every field the pipeline added. The step now records the message the
router was handed. Its duration still covers the traversal.

### Added — a list variable works in a SQL `IN (...)` clause

`id IN ({{.ids}})` had no working form. Every `{{ }}` token became exactly one
bound parameter, so a list was handed to the driver whole and rejected there:
`database/sql` answers `unsupported type []interface {}, a slice of interface`,
and pgx answers `cannot find encode plan for ... into binary format for uuid
(OID 2950)`. It failed the same way for uuid, integer and text columns, on the
first message, every time.

A token that sits directly in an `IN (...)` list now expands to one placeholder
per element, so `id IN ({{.ids}})` becomes `id IN ($1, $2, $3)` with the elements
bound individually. An empty list binds a single NULL — valid SQL in every
dialect and matching nothing, which is what an empty set means. `IN ()` is a
syntax error everywhere, so there was no other choice available.

The expansion is deliberately confined to `IN` lists. `= ANY({{.ids}})` is the
native PostgreSQL array form and already worked by binding the slice whole;
expanding it would produce `= ANY($1, $2)`, a syntax error. A token inside a
subquery — `IN (SELECT ... WHERE k = {{.k}})` — is left alone for the same
reason. Quoted string literals no longer affect the detection either, so a
stray `'in ('` inside a literal cannot open a list.

One token expands to at most 65535 placeholders — PostgreSQL's wire-protocol
limit, and below it SQL Server's own limit of 2100 — and a longer list fails the
node instead. The element count comes from message data, which nothing upstream
bounds, and each element costs a placeholder in the statement text as well as an
argument in the bind list; without the cap a pathological array is built in the
worker's memory before any server gets the chance to reject it.

One function does this for every SQL template path, so all of them gain it at
once: the `db_lookup` node in query mode, the `execute_sql` node, and the
editor's query preview. It also moved from `pkg/comm/transformer/core` down to
`pkg/infra/sqlutil`, next to the placeholder and identifier-quoting rules it
depends on, so sources and sinks can use it without importing a transformer
package. The old `core.ParameterizeTemplate` name still works.

### Added — `data_conversion` converts to a list, to a UUID, and back

The node accepted `int`, `float`, `bool`, `string` and `date` and rejected
everything else with `unsupported target type`. There was no way to build a list
anywhere in Hermod — not through this node, and not through the expression
evaluator, which has no `split`, no `join` and no array constructor — so the new
`IN` expansion above had nothing to feed it except a list that already arrived
as one.

**Array.** Splits text on a separator (default `,`, elements trimmed), reads a
JSON array as a list when the value looks like one, treats a JSON object as a
single element rather than a list of its fields, passes an existing list
through, and wraps a scalar as a one-element list — a lookup keyed on one id is
the degenerate case of a lookup keyed on several. An **Element Type**
(`string`, `int`, `float`, `bool`, `uuid`) coerces every element; it is needed
whenever the column is typed, because splitting text yields strings and a string
does not match an integer or uuid column.

**UUID.** Validates and normalises to lower-case hyphenated form. Accepts
hyphenated, bare hex, braced and `urn:uuid:` forms, and the raw 16 bytes a uuid
column decodes to. There was no UUID conversion at all before — the evaluator's
`uuid` function *generates* one, which is a different thing.

**Back again.** Converting a list to `string` now joins on the separator.
It used to render Go's `%v` form, so a list became the literal text `[a b c]` —
a value no database or downstream system accepts, and the reason a list could
not be converted back to a scalar at all. Two narrower consequences of the same
fix: a `[]byte` value converts to its text rather than to `[97 98]`, and a
nested map or list inside a joined list renders as JSON rather than as `%v`.

### Added — a `batch_sql` source can define query parameters

`{{.last_value}}` was the only variable a scheduled query understood. Any other
token passed through into the SQL as literal text and failed at the server,
because the query was executed with no arguments at all.

A source now carries a **Query Parameters** JSON object, and every token other
than `{{.last_value}}` is bound from it — including a list, which expands inside
an `IN (...)` list like anywhere else. A batch source has no inbound message, so
this is the only place its variables can come from.

Two failure modes are loud rather than silent. A token with no matching
parameter fails the query and names the token, instead of binding NULL and
returning an empty result set that reads as an empty table. A parameters blob
that will not decode is an error rather than an empty map, which would have
dropped every filter the operator configured while leaving the query running.
The editor flags both before the next cron tick.

`{{.last_value}}` keeps its existing textual substitution: operators write it
inside their own quoting (`id > '{{.last_value}}'`), and binding it would break
every query that does. The scheduled run and the editor's sample preview now
resolve queries through the same function, so a query that previews cleanly runs
the same way on the cron.

### Fixed — `db_lookup` bound a list with `=` on the WHERE-clause path

The `keyColumn` path expanded a list into `IN (...)`; the `whereClause` path did
not. A list value there produced `col = $1` — the wrong operator and an argument
no driver can encode. It now builds an `IN (...)` list, and an empty list
resolves to no match instead of an error.

The same path also recognised only `[]any`, `[]string`, `[]int`, `[]int64` and
`[]float64` as lists, so a list arriving as any other slice kind (`[]int32`, for
one) was bound whole and rejected by the driver. Recognition is now by kind, with
`[]byte` and `json.RawMessage` still treated as the scalar blob and JSON values
they are.

### Added — per-row ordering, end to end

Changes to one row now reach the sink in the order the source produced them,
while different rows still run fully in parallel.

Nothing enforced this before. `processMessage` ran on `max_inflight` (128)
workers pulling from one shared queue with no key affinity, so two changes to
the same row raced and the later one could be written first. Measured, ten
changes to one row arrived at the sink as `010 005 006 007 004 002 001 003 008
009` — for CDC that is an older UPDATE landing after a newer one, and the row
keeps the wrong value. The sink writer's per-key shards did preserve order, but
they sit downstream of the reordering and their only test enqueued straight into
the writer, so the gap was invisible.

A source that knows its row identity now stamps `_hermod_order_key`. For
PostgreSQL that is `schema.table` plus the replica-identity columns, on both the
initial load and the CDC stream, so the backfill row and the first change to it
are ordered against each other at the handover instead of racing. The engine
pins a key to one worker and one sink shard.

Messages with no ordering key — a queue message, a cron tick, a batch row — have
no row order to keep and continue to use the shared queue, so they lose no
throughput to affinity they do not need. A table with no primary key or
`REPLICA IDENTITY NOTHING` cannot identify a row, so its changes carry no key
and are not ordered against each other; set a replica identity if you need that.

Cost, measured with `benchstat` over ten paired runs: -2.1% geomean throughput,
with no individually significant regression at any payload size (64 B, 1 KiB,
16 KiB; p ≥ 0.05 each), and allocations unchanged.

### Fixed — `shard_count` scattered the rows it was meant to keep together

With `shard_count` set and no `shard_key_meta`, the sink writer sharded on the
message ID. A CDC message's ID is its LSN, which is unique per change, so every
change to one row hashed to a different shard: turning sharding on scattered
exactly the messages the shards exist to keep in order, and the per-key ordering
the feature promised was never delivered for the source that needs it most.

`shard_key_meta` now defaults to the row key when sharding is enabled. The
README's claim that sharding "guarantees per-key ordering" has been corrected to
describe what it does and what it costs — sharding splits a sink's queue, so
each shard batches independently.


### Fixed — list pages could repeat a row and skip another

Seven list endpoints paged with `LIMIT`/`OFFSET` over no `ORDER BY` at all, so
the engine was free to return the same row on two pages and another on neither.
There was also nothing to order by: `workflows`, `sources`, `sinks`, `users`,
`vhosts`, `workers` and `plugins` had no `created_at` column. Each gained one —
stamped in storage rather than at the call sites, never rewritten by an `UPDATE`,
and backfilled by `Init` for rows that predate it. Lists sort newest first, with
`id` to break ties.

Searching those lists matched with `LIKE` against the raw column. sqlite and
MySQL fold ASCII case on their own, so this looked correct in development;
**PostgreSQL does not**, so on the driver Hermod actually deploys with, typing
`Order` found nothing named `order ingest`. Both sides are now folded in SQL.

The SQL and mongo backends also searched *different fields*, so which one was
deployed changed what the search box found. Both now read one list from
`internal/storage/search.go`.

### Fixed — Available Fields was empty on a source that had never been sampled

A transformation wired to a fully configured source offered no fields, and
nothing was failing anywhere: the list is derived from the upstream source's
*stored* sample, and that sample was only ever written by Test Connection or the
refresh icon. A source created through the API, restored from a bundle, or saved
from the wizard without pressing Test Connection had no sample at all. The editor
now captures one when a transformation is opened.

Closing that gap surfaced two defects in how the editor already wrote to a
source:

- `storage.UpdateSource` wrote `State` on every update, so a `PUT` body with no
  `state` key overwrote the column with null — resetting a `batch_sql`
  `last_value` watermark or a Postgres CDC cursor as a side effect of an
  unrelated save, surfacing only on the next run as already-delivered rows
  arriving again. Absent or null now keeps the stored cursor; `{}` clears it.
- Storing a sample rewrote the rest of the source along with it.


## [1.5.0] — 2026-09-17

This release is mostly the editor telling the truth. A cluster of surfaces
looked configured and could not work: the Discord and Slack sink forms rendered
no fields at all, Telegram's token was written under one name and read under
another, the "sink in use" warning had nothing to trigger it, "Preview Template"
rendered nothing, the SMTP sink's S3 template tab pointed at keys the form never
wrote, and the Available Fields list stayed empty for every node downstream of a
`batch_sql` source. Beside those, the two email sinks become fully templated —
every panmail field, plus `.Format` and `.In` helpers so a date column can be
formatted where the email uses it — and importing a workflow becomes a wizard
over the bundle's contents rather than a raw-JSON textarea, with export finally
carrying every source the workflow references instead of dropping the ones it
names outside a node's `ref_id`.

**If a `db_lookup` or a `batch_sql` source in your pipelines points at a
database that also has CDC enabled, read the fourth entry before upgrading.**
Both run SQL against a source they only name, neither used to check whether that
source was also serving change data capture, and both now refuse. A `batch_sql`
source whose delegate has CDC on no longer builds, so its workflow will not
start; a `db_lookup` pointing at a CDC source fails every message through that
node. `use_cdc` is opt-out — a source carrying no key at all counts as a CDC
source — so this catches configurations that never set the flag either way. SQL
Server is the documented exception, because its CDC is read back through
ordinary queries. The fix is to turn CDC off on that source, or to register a
second, non-CDC source for the same database and point the node at that.

One smaller upgrade note: a panmail sink that templates `base_url` or `api_key`
now needs an `allowed_hosts` list and will not start without one, because a
templated gateway lets a row decide where a tenant-wide credential is sent. A
panmail sink whose gateway and key are static is unaffected.

### Fixed — nodes downstream of a batch_sql source had no available fields

Opening a `db_lookup` wired to a `batch_sql` source showed an empty Available
Fields list, so there was nothing to pick a key field from.

Available Fields is built in the browser from the upstream source's stored
sample (`useNodeContext`), and a source's sample is captured right after a
successful Test Connection (`useSourceForm.testMutation` → `fetchSample`).
`fetchSample` takes the table to sample from `config.tables`, a key a batch
source does not have — its config carries `source_id`, `cron`,
`incremental_column` and `queries` — so it posted the empty string.
`BatchSQLSource.Sample` built its query as `SELECT * FROM <table> LIMIT 1`,
which with no table is `SELECT * FROM  LIMIT 1`: a syntax error. The sample
request 400'd, no sample was ever stored, and every node downstream of the batch
source offered nothing.

`Sample` now previews the first configured query when no table name is given,
which is also the statement the scheduled run executes, so the columns offered
are the columns the pipeline actually produces. `{{.last_value}}` is substituted
the way `runBatch` substitutes it — the empty string before the first run, which
is what that run sees too — and both paths now decode the query list through one
helper so a preview cannot drift from the run. The query is not wrapped in a
`LIMIT`: the statement is the operator's own and the dialect is the delegate's,
so SQL Server and Oracle would reject the wrapper; one row is read and the rows
closed instead. A caller that does pass a table (the column browser, the
sink-side preview) still gets the table query.

Separately, `validateSourceForSampling` asked a batch_sql config for `query` or
`table` and so reported every configured batch source invalid. That rule is
latent — `SamplePanel` is its only consumer and nothing renders it today — but it
is fixed rather than left to be rediscovered if the panel is wired back up.

### Fixed — editing a source did not reach the rows already cached from it

`db_lookup` caches what it reads, and `SetLookupCache` treats `ttl <= 0` as
"never expires" — which is the default, because the editor's Cache TTL box is
empty until somebody fills it in. Nothing dropped those entries when the source
they came from was edited, so "until something evicts it" meant "for the life of
the process": a corrected connection string, a changed table, a rotated
credential all left the old rows being served.

The CDC rule makes it sharper. Switching `use_cdc` on is precisely the edit that
has to stop a lookup working, and it was the one edit a stale cache would serve
straight past.

`Registry.UpdateSource` now drops the lookup rows cached from that source and
only that source — the rest were read from sources nobody edited, and dropping
them would turn every unrelated edit into a throughput cliff. A failed write
keeps the cache, since the source is still what it was and a storage blip should
not become a cache stampede. The key prefix both sides match on lives in
`hermod.LookupCacheKeyPrefix`, trailing separator included: without it,
invalidating source `cust` would also drop everything cached from `customers`.

### Fixed — the worker registration end-to-end test could never run

`worker_e2e.spec.ts` resolved the binary through cwd-relative candidates
(`.dev/hermod`, `./hermod`). Playwright runs from `ui/`, where those are
`ui/.dev/hermod` and `ui/hermod` — paths that have never existed. The spec
failed with "no hermod binary found" while the binary sat built one directory
up, and `HERMOD_BIN` was set nowhere in the repo or in CI. The candidates are
now anchored to the repo root through the `repoRoot()` helper the port
resolution already used.

### Fixed — queries that borrow a source's database could still land on a CDC one

Two node types run SQL against a source they merely name rather than stream
from: `db_lookup`, once per message, and the `batch_sql` source, which holds no
connection of its own and runs whole queries on a cron against the source in its
`source_id`. Neither belongs on a database already paying for logical
replication — and where a `batch_sql` delegate is also a CDC source node, the
same rows arrive twice, once streamed and once batched.

`db_lookup` had carried that rule since it was written, with two holes. It only
fired when the source had an explicit `use_cdc` key, but the factory that builds
a source reads the flag as opt-out — `useCDC := cfg.Config["use_cdc"] !=
"false"` — so a source with no key runs as a CDC source and passed the check
anyway. And it sat inside one arm of the batching branch, so a node with Batch
Lookups switched on skipped it outright, cached the row it was not allowed to
fetch, and served every later message from that cache. `batch_sql` had no rule
at all.

There is now one definition — `hermod.SourceAllowsDirectQueries` — read by the
factory, the lookup transformer and the registry, so the three cannot drift.
`db_lookup` checks once, ahead of both branches. The registry refuses to build a
`batch_sql` source on a CDC delegate, on both of its constructors. SQL Server
stays the documented exception: its CDC is read back through ordinary queries
against change tables.

Two smaller decisions fell out of it. The lookup's refusal deliberately does not
go through `onMiss` — a misconfigured source is not a lookup that found no row,
and a passthrough policy must not turn it into silence. And a `batch_sql`
delegate that cannot be resolved now fails the source naming that delegate,
rather than being waved through: treating a failed lookup as "no objection"
would let a transient storage error during a restart build the very source the
check exists to refuse.

Both editors apply the same rule from one place (`ui/src/lib/sourceCdc.ts`). A
CDC source is listed but not selectable, labelled with the reason rather than
filtered out, so an absent entry never reads as a missing source. A node or
source already pointing at one keeps the value it was configured with and says
why it will not run.

**Behaviour change:** a query target with no `use_cdc` key is now refused. That
is the reading the rest of Hermod already uses, so such a source was being built
as a CDC source regardless — set `use_cdc` to `false` on it, which is what a
lookup or batch target is meant to be.

Validation knows the rule too. `GET /api/workflows/{id}/validate` and every
save now report a `db_lookup` or `batch_sql` node aimed at a CDC source, as a
warning — an error would turn saving the fix into a 400. That is the only
notice a workflow created through the API or restored from a bundle ever gets,
since it never passes through the editor's pickers.

And switching CDC *on* for a source a running workflow queries is now refused.
`checkActiveWorkflows` guarded only sources held in a source node's `ref_id`,
which is one of the ways a workflow names one: a `db_lookup` holds its source in
the node config, and a `batch_sql` source holds its database in `source_id`. A
source reached only those ways could be edited out from under a running
workflow, and after this release that edit breaks it on the next message.
`storage.WorkflowQueriesSource` answers the reference question in one place.

`execute_sql` is deliberately not blocked, because it is not the same hazard.
It writes — `ExecContext`, with nothing to hand back but a row count — so the
risk is not query load on a replicating database but a **feedback loop**: a
write into a published table produces a change event that comes back round the
pipeline. That is scoped to the table while `use_cdc` is scoped to the source,
so refusing the source would break the ordinary case of writing an audit or
status row nobody streams. Its editor names the real risk and leaves the choice.

### Fixed — a sink set to "Sequential Execution" never acknowledged anything it delivered

A sink node with Sequential Execution switched on writes the message itself, so
it deliberately hands the engine no routing targets. The engine read that empty
list as "this workflow has sinks and resolved none of them" — its data-loss
case — and took the branch that refuses to acknowledge, on every message, of a
workflow that was delivering all of them correctly.

Measured on a 202-row PostgreSQL CDC run: every row reached the destination,
all 202 were counted in `hermod_engine_messages_dropped_no_target_total` (a
metric documented as "any non-zero value is an incident"), an ERROR said the
data had gone nowhere, and the replication slot stopped advancing — 108 KB of
WAL retained and never released, growing for the life of the workflow, with the
whole backlog replayed on the next start. With the flag off, the same run
retained nothing.

A sink that writes inline now says so, and the engine acknowledges it. A sink
whose inline write *failed* still does not, so it is redelivered rather than
lost — and with several inline sinks, one failing keeps the message
unacknowledged even if another succeeded.


### Fixed — an enriched CDC message could reach the sink without its enrichment

What a message serialised to depended on whether anything had read it before the
first write. `SetData` hydrated a lazily-decoded payload differently from
`Data()`/`DataRef()`: on a CDC message it buried the row under a nested `after`
key and left the newly written field at the root, where `Payload()` — which
serialises only `data["after"]` — no longer included it. The same message then
came out four different ways, with `MarshalJSON` nesting it twice as
`after.after`.

`db_lookup` is how this reached a destination. In query mode there is no key
field, so nothing reads the message before the result is written, and the Cache
TTL box is empty by default — where `ttl <= 0` means never expire. The first
message took the query path, which happens to read; every message after it was
served from cache and silently lost its looked-up value. Measured live: 1 of 5
rows carried the enrichment. It is rate-dependent, which is what made it hard to
see — a burst is processed concurrently, races past the cold cache, and looks
fine.

Both paths now hydrate through the same helper, so the shape no longer depends
on access order, and `ToMap`/`MarshalJSON` share one after-image. The same
change stops `SetData` destroying a payload that is not a JSON object: the
unmarshal failed, nothing was stored, and the payload bytes were cleared at the
end of the call, so the first write threw away a plain-text body.


### Fixed — Data Conversion reported success when the field did not exist

A field name that resolved to nothing returned the message unchanged with no
error: a green node, untouched data, and nothing anywhere to say the conversion
had not run. A misspelling is the usual way to get there, and the editor offers
field names from the source's stored Sample, which is known to drift from the
names a live CDC stream carries.

An unresolvable field is now a conversion failure and follows the node's Error
Behaviour like any other — which the editor already defaults to "fail", so the
setting an operator is looking at is now the one that applies. Pipelines where
the field is genuinely optional set "keep" to pass the message through
untouched, or "null" to write an explicit null.


### Fixed — the "sink in use" warning could never appear

The sink form asks which workflows use a sink before letting anyone edit it, and
warns "stop these first" when any of them is running. It asked
`GET /api/sinks/{id}/workflows`, which was never registered: every sink edit
page raised a "Request Failed — Not Found" toast, the list came back empty, and
the warning stayed hidden however many live workflows were writing through the
sink. The source side has had the route all along.

It is registered now, and answers the same shape: the running workflows with a
sink node pointing at that id, filtered by the caller's vhost access. A source
node that happens to carry the same id is not one of them.


### Fixed — the Discord and Slack sink forms were blank, and Telegram's token went nowhere

Both types are offered in the sink picker and both are routed to the chat sink
form, which had a branch for Telegram and `default: return null`. Picking either
one showed an empty step. Nothing stopped the save either — neither type has a
required key in the wizard's gate — so the sink stored empty and failed on its
first message with `not configured: missing webhook_url or token/channel_id`.

They have forms now, asking for what the sink actually reads: a webhook URL on
its own, or a bot token with a channel id, with the either/or said out loud
because the sink accepts both shapes and demands one.

The same form's Telegram branch wrote `bot_token` while the factory read
`token`, so the token typed into it reached the sink as `""` and every message
went to `https://api.telegram.org/bot/sendMessage` — a 404 about a bot nobody
has. The factory reads either name now, preferring the form's.

Two things went with it. The chat form carried a second copy of the SMTP form
that nothing could reach (`SinkForm` maps `smtp` to `SMTPSinkConfig`) and that
had drifted from the one that is reached — deleted. And the silent `default` is
now a visible "no form for this sink type", because rendering nothing is what
hid this for as long as it was hidden: the coverage test derives its type list
from the routing map, so a fourth type pointed at that form fails until it has a
branch.


### Fixed — the template preview rendered against a row nobody has

`SinkForm` declared an `incomingPayload` prop and never destructured it, so the
row the editor had already fetched — the one its field picker is built from —
stopped at the form's own signature and never reached the sink forms inside it.

The SMTP preview now opens on that row when the editor has one, and falls back
to the example row only when it does not. The sample box is still editable, so
the fallback is a starting point rather than the only thing on offer.


### Fixed — "Preview Template" never rendered anything

The button was gated on a prop nothing passed, so it never appeared; the route
behind it, `POST /api/sinks/smtp/preview`, was a handler with a comment where
its body should be, answering 200 and an empty response. Writing an email
template meant activating the workflow and mailing someone to find out whether
it worked.

The endpoint renders through the sink's own `BuildEmail` — the same call the
worker makes, reading the same config through the same factory. A preview with a
renderer of its own agrees with the send right up until the day it matters; this
one cannot disagree, because it is the same code. `Write` is now that call plus
the idempotency claim and the send.

The modal shows the subject, the resolved recipients and the body, rendered in
an iframe when the template is HTML. It renders against an example row so a
fresh template shows an email rather than a page of `<no value>`, and the row
comes back in an editable box so it can be replaced with a real one. A template
that does not parse puts its error next to the template, not in a toast.

**Inline templates only.** A URL or S3 template is fetched by the server and a
preview hands back what came out of it, so previewing one would make anyone who
can edit a sink a reader of any address the worker can reach. The request is
refused with that stated, rather than quietly rendering nothing.


### Fixed — the SMTP sink's S3 template tab was inert

The form writes `template_s3_region`, `template_s3_bucket`, `template_s3_key`,
`template_s3_access_key` and `template_s3_secret_key`; the factory read
`s3_region`, `s3_bucket`, and so on. Every field typed into that tab reached the
sink as an empty `S3Config`, so the sink asked S3 for bucket `""` and key `""`,
and the only symptom was a send failing over a bucket nobody had configured.

The factory reads the form's names now, and still reads the bare ones, because a
bundle imported or a sink posted against them is a config that exists. The tab
also gained the **Endpoint** field the factory has always read and the form
never offered, which is what an S3-compatible store needs to be addressable.

The test that covers it fetches the template from a stand-in for S3 and checks
the path that was asked for, because a test that only checks the mapping cannot
tell whether the sink uses it.


### Added — a date column can be formatted where the email uses it

An SMTP template could print `created_at` and little else: the column reaches a
sink as text, and text has no `.Format`. Anyone who wanted `01 Dec 2026`, or a
row's timestamp in the reader's own zone, had to add a transformation upstream
of the sink to get it.

Values that read as a date or a timestamp are now recognised on the way into the
template and carry the time methods:

```
{{ .created_at.Format "2006-01-02" }}                   2026-12-01
{{ .start_at.In "Asia/Jakarta" }}                       2026-12-01T16:30:00+07:00
{{ .start_at.In (time.LoadLocation "Asia/Jakarta") }}   the same
{{ .created_at.Time.Year }}                             2026
```

alongside a function set — `time.LoadLocation`, `time.Now`, `time.Parse`,
`time.Unix`, `time.UnixMilli`, `time.UTC`, `time.Local`, and `now`, `date`,
`dateInZone`, `toDate`:

```
{{ date "02 Jan 2006" .created_at }}
{{ dateInZone "2006-01-02 15:04" "Asia/Jakarta" .created_at }}
{{ (time.Unix .epoch_seconds).Format "15:04" }}
```

A recognised value is still a string, holding the exact text the row carried, so
`{{.created_at}}`, `eq`, `len`, `slice` and `printf "%s"` render what they
rendered before this change. Recognition is by layout, and only for text that
starts with a `YYYY-MM-DD` date: RFC 3339, the PostgreSQL and MySQL timestamp
forms, Go's own, and a bare date. Anything else — an id that happens to be
digits, `2026-13-45` — is left as it was.

The same column arrives in two shapes, and one template has to read both: the
PostgreSQL CDC path sends every column as text, while a query path hands over
the driver's `time.Time`. A driver time now takes a zone by name as well
(`{{ .start_at.In "Asia/Jakarta" }}` used to be a type error on it and worked
on the text column beside it), it still prints exactly as Go prints a time, and
`Sub`, `Before`, `After` and `Equal` read across the two:
`{{ .ended_at.Sub .started_at }}` no longer cares which path each of them came
from.

The methods travel with the message, so they work in the subject, in each
recipient, in the idempotency key and in an inline body. The functions need
Hermod's own renderer: a body fetched from a URL or from S3 is rendered by
gsmail, which parses with no function map, and a `time.` call there is a parse
error that names the function.

The zone database is compiled into the binary (`time/tzdata`), so
`Asia/Jakarta` resolves on an image that ships no `/usr/share/zoneinfo`.


### Added — every panmail field is a template, and a gateway allowlist to bound the two that matter

The panmail sink rendered seven of its settings as Go templates over the message
— from, to, cc, bcc, subject and the two bodies — and passed the rest through
verbatim. The four it passed through are the ones that decide *where* a message
goes: the gateway url, the api key, the provider id and the stored template id.
All four are now templated, so one sink can serve many tenants and pick the
stored template per row (`{{.kind}}`) instead of needing a sink per case.

In scope for every one of them: `{{.id}}`, `{{.operation}}`, `{{.table}}`,
`{{.schema}}`, `{{.metadata.x}}`, and any field of the row — which wins over the
envelope when the names collide.

The provider id and the template id are refused when they render empty rather
than sent. An empty provider sends from whatever the gateway picks, and an empty
template id used to fall through to the body fallback, so a typo in a field name
mailed the wrong thing to a real person instead of failing.

The gateway url and the api key are a different kind of field, because between
them they decide where a tenant-wide credential is sent — and with them
templated, a row decides it. A row reading
`gateway_url = https://attacker.example/` would hand over the key. So templating
either one now requires `allowed_hosts`, and the sink refuses to start without
it:

```
allowed_hosts = *.mail.example.com, mail.example.com
```

Hosts are matched exactly, or by a single leading `*.` over a domain with at
least two labels — `*.com` is refused, it bounds nothing. A rendered host that
matches no rule is refused before the client is built, and the error names the
host without the key. The UI asks for the list as soon as either field contains
`{{`, and the wizard's Next stays disabled until it is filled in.

Two smaller consequences, both deliberate:

- The client cache is keyed on the rendered gateway and key, which a wildcard
  rule lets the upstream table grow without limit. It is bounded at 32 entries
  and evicted least-recently-used.
- A templated gateway joins the derived idempotency key: the same mail to two
  gateways is two sends, and hashing them alike would suppress the second. A
  *static* gateway is deliberately left out, so keys already in the store keep
  matching — an orphaned claim is a message mailed twice.


### Added — importing a workflow is a wizard, not a textarea

Import was a box to paste JSON into and a button. Whatever the file said was
written verbatim, which is fine for a bundle produced by the same instance and
wrong for every other case: a bundle from staging arrives carrying staging's
hostnames, staging's passwords, staging's encryption key, and ids that may
already name something in production.

**Import JSON** now opens a wizard that reads the file first and shows what is
in it, step by step:

- **Bundle** — paste or upload, with a summary of what was found and the
  export's `missing_refs` spelled out rather than discovered later.
- **Workflow** — its name and the vhost it lands in.
- **Sources** and **Sinks** — every one in the bundle, with its real
  configuration form (the same components the Add Source and Add Sink screens
  use) and a **Test connection** button that works before anything is written.
- **Nodes** — the nodes holding something environment-specific: a `db_lookup` or
  enrichment SQL node's source, an `api_lookup`'s address, an `encrypt` or
  `decrypt` node's key. Each is edited with the editor the canvas would use for
  it, so a lookup's source picker offers this instance's sources too and can be
  repointed at one. A node is listed when its config holds a source reference, a
  credential (decided by key shape, the way `configsecrets` decides on the
  server) or an endpoint — so a connector added next year is covered without
  anyone adding it to a list. Mapping and filter nodes describe a shape, travel
  unchanged, and stay out of the way.
- **Review** — a table of exactly what will be created and what will be
  replaced, then one request.

**Id collisions are now a choice.** When a bundle's source, sink or workflow id
already names something here, the wizard says so, names the record it would
replace, and offers to import it as a separate copy instead. Choosing a copy
generates a new id and repoints every reference to it — a source node's
`ref_id`, a `db_lookup`'s `sourceId`, a `batch_sql`'s `source_id`, the
dead-letter sink. That rewriting is pure and unit-tested against all four
reference sites; missing one would produce a workflow that imports cleanly and
then cannot start.

`sources.name` and `sinks.name` are `NOT NULL UNIQUE`, so a copy cannot keep the
original's name: the wizard suggests the nearest free one, and puts it back if
you change your mind. A name already held by a different record is reported on
the field and blocks the import, instead of surfacing as
`constraint failed: UNIQUE constraint failed: sources.name (2067)`.

### Fixed — an exported workflow did not carry every source it uses, and importing one could overwrite a live connection

Export collected a workflow's dependencies by walking its nodes and taking the
`ref_id` of the ones typed `source` or `sink`. That is not the only way a
workflow names a source. A `db_lookup` node holds one in its config under
`sourceId`, the enrichment SQL node holds one under `sourceId` or `sourceID`,
and a `batch_sql` source delegates its connection to a second source through
`config.source_id`. None of those travelled with the bundle, so an export of an
enriched pipeline described a workflow that started on the destination instance
and then failed every message with `failed to get source for lookup`. The
bundle now collects all of them, following the `batch_sql` hop transitively.

A dependency that could not be read was dropped from the bundle in silence,
which is how an export looked complete and was not. A reference that no longer
exists is now listed in the bundle's `missing_refs` and shown in the export
notification; a *storage failure* while reading one is no longer treated as the
same thing, and fails the export rather than quietly shipping a bundle with a
hole in it.

On the way back in, the source and sink upserts ran as
`_ = h.Storage.CreateSource(...)`. An import whose dependencies all failed to
save still answered `201 Created`, and left a workflow pointing at connections
that were never written — the same swallowed-error shape that was fixed for the
workflow row itself one release ago. Every save now reports its failure, and
dependencies are written before the workflow so a failure stops short of
creating one that cannot run.

Two things in an import were not checked at all:

- **Permissions.** The vhost check only looked at the workflow. The bundle's
  sources and sinks carry their own vhost and are upserted by ID, so an editor
  confined to one vhost could hand in a bundle that overwrote a connection —
  host, credentials and all — belonging to a vhost they cannot even read. Every
  bundled resource is now checked.
- **Runtime state.** A bundle is a description of a workflow, not of a running
  one, but the exported JSON carried the origin's runtime columns and the import
  wrote them straight through. Re-importing over an existing source therefore
  replaced its CDC cursor with a position from another database, silently losing
  or replaying everything in between, and reassigned the workflow to a worker
  that does not exist on this instance. Export no longer writes those columns,
  and import preserves this instance's own.

Also fixed: a workflow name went into the `Content-Disposition` filename raw, so
a name containing a quote produced a malformed header and one containing a slash
proposed a path; and in the import modal, pressing **Import Workflow** straight
after pasting a bundle did nothing. The JSON field reformats itself on blur, and
the export writes the bundle on a single line, so pressing the button blurred the
field, rewrote its value and re-rendered the modal between mousedown and mouseup
— no click event was ever produced and it took a second press.

### Fixed — a `jsonb` column arrived as a string on the CDC path, and vanished when it was TOASTed

A PostgreSQL `jsonb` column had two different shapes depending on how the row
reached the pipeline, and the workflow editor showed you the one that does not
run in production.

The snapshot, polling and `Sample` paths read rows through pgx, whose registered
codec unmarshals `jsonb` into a map — so those messages carried a real nested
object. The live CDC path decodes pgoutput tuples by hand, and pgoutput sends
every column as text, so the same column arrived as a string that happened to
contain JSON. The editor builds its *available fields* list from the source's
stored sample, which comes from the first path. It therefore offered
`meta.addr.city`, and on a running CDC pipeline that path resolved to nothing.
Both paths now produce the object.

The second half is worse and is the reason to read this entry. PostgreSQL stores
a `jsonb` value larger than about 2 KB out of line, and an `UPDATE` that does not
touch such a column does not send it — it marks it *unchanged* instead. That
marker was not a case in the tuple decoder at all, so the column was dropped from
the after-image and a sink writing that image lost the document. Every update to
a row with a document of any size, silently. Where the table is `REPLICA IDENTITY
FULL` the before-image does carry the value and the after-image is now completed
from it. Where it is not — `REPLICA IDENTITY DEFAULT` sends no before-image at
all — nothing in the WAL record holds those bytes, so the column stays out of the
row image rather than being invented, and the message now carries
`unchanged_toast_columns` naming it. **If you stream a table with large `jsonb`
or `text` columns, `ALTER TABLE ... REPLICA IDENTITY FULL` is what makes updates
complete.**

MySQL had the same shape problem on its own `JSON` type, on both the binlog and
the query paths, and is fixed the same way.

Only columns the database itself types as JSON are decoded, never columns that
merely contain it: a `VARCHAR` holding `{"a":1}` is still a string. MariaDB is
therefore unchanged — its `JSON` is an alias for `LONGTEXT` and the driver
reports it as `TEXT`, indistinguishable from any other long text column, so
there is nothing to key on and guessing from content would reshape far more than
it fixed.

**This changes the shape of messages from `json`/`jsonb`/`JSON` columns on the
CDC path.** A transformation or sink template that treated such a column as a
string — parsing it itself, or passing it through as text — now receives an
object. Templates that reached into it with gjson's `@fromstr` modifier keep
working, since that modifier is a no-op on a value that is already an object.


## [1.4.0] — 2026-09-14

The FCM sink is the headline. It could address a device, a topic or a condition
and set a title and body, all of them only from message metadata; everything
else Firebase offers had no representation at all. It is now a full client, with
every field a Go template over the message. Two connectors arrive beside it —
the `panmail` sink, and `metis` as both source and sink for a BPMN workflow
engine — and twelve sink types stop rendering the database form instead of their
own fields, a wizard fallthrough rather than a missing-field bug.

**If you run an `encrypt` or `decrypt` node, read the first entry before
deploying.** A field list that matches nothing on the message now fails that
message instead of forwarding it untouched. That is the intent — a decrypt node
that silently passes ciphertext to a sink is the failure the node exists to
prevent — but a stream where some messages legitimately carry none of the named
fields needs `onMissingField: "skip"` to keep working. A partial match is
unaffected: one field present and another absent is an optional column, not a
misconfiguration.

### Changed — `encrypt` and `decrypt` report a field list that matches nothing

A security node whose configured fields are all absent from the message used to
run, touch nothing, and report success: encrypt forwarded plaintext, decrypt
forwarded ciphertext, and nothing anywhere said so. Both now fail the message
instead, naming the fields asked for and the fields the message actually
carries.

This is the misconfiguration operators hit when a source starts delivering a
body that is not a JSON object — the field list still names the old column while
the body now arrives under `payload`. The node looked healthy and the sink
quietly received ciphertext.

It sits one level above the existing `onError` and `onPlaintext` policies, which
are value-level: both need the value in hand, and neither can see a field that
was never reached.

A **partial** miss is deliberately unaffected. One field present and another
absent is an optional column, not a misconfiguration, and failing it would break
every heterogeneous stream.

**If a stream legitimately carries messages with none of the named fields**, set
`onMissingField` to `skip` to restore the previous behaviour. The editor exposes
it as *When no field matches*, next to the other failure policies.

### Added — the FCM sink is a real Firebase client

The Firebase Cloud Messaging sink could address a device, a topic or a
condition and set a notification title and body, all of them only from message
metadata. Everything else FCM offers — Android channels and collapse keys,
APNs badges and background pushes, Web Push links, analytics labels, dry runs,
topic subscription — had no representation at all, and the editor's form
offered four fields: the credentials and three destination defaults.

It is now a full client. Every destination and notification field is a Go
template over the message, so a device token or a deep link comes from a column
rather than needing a transformer to copy it into metadata first. A token field
that renders a comma-separated list fans out as a multicast. Android, APNs and
Web Push each have their own block, because "high priority" and "expire this
after ten minutes" mean different things on each. `subscribe` and `unsubscribe`
actions turn a device-registration table into an FCM audience, so a topic is
addressable without the pipeline holding a token list of its own.

The data payload gained a choice: the envelope it has always sent (the
formatted row under `payload`), the row's own columns as top-level keys, or
nothing at all for a notification-only push.

### Fixed — six ways the FCM sink misbehaved

- **Two defaults produced a message FCM refuses.** Configuring both a default
  device token and a default topic set `Token`, `Topic` *and* `Condition` on
  every message. FCM accepts exactly one, so every send failed with "exactly
  one of token, topic or condition must be specified". The sink now refuses
  that configuration when it is saved.
- **A blank `fcm_token` was treated as a destination.** The check was for the
  key's presence, not its value, so a transformer copying a nullable column
  produced a message addressed to the empty string instead of falling back to
  the configured default.
- **`Ping` sent a real message.** The connection test called `Send` with a
  made-up token, which cost send quota and, with a token that happened to be
  live, would have notified a real device. It now uses `SendDryRun`, and it
  distinguishes a refusal about the message — which proves the round trip
  worked — from one about the credentials, which is the failure it exists to
  find.
- **An empty credentials field silently used the machine's Google
  credentials.** On any host with `gcloud` logged in, that meant pushing to
  whatever project that account defaulted to. Using ambient credentials is now
  an explicit opt-in that has to name its project.
- **A payload over FCM's 4096-byte limit burned the whole retry budget.** FCM
  refuses an oversized message and it will not be smaller next time. The sink
  now checks before sending and reports it as permanent, with `truncate` and
  `drop` available for workflows that would rather deliver something.
- **The column a push was addressed by travelled inside the push.** A
  registration token is a capability — whoever holds it can push to that
  device. Under the new `fields` data mode every column became a data key, so a
  message addressed by `{{.device_token}}` carried that token back to the
  device it was addressed to, and a multicast, whose field holds *every*
  recipient's token, handed each device the whole list. The columns the
  destination templates read are now withheld from the payload; naming one
  under `data_json` puts it back for anyone who wants it.

Refusals FCM calls permanent — a dead registration token, the wrong project,
an invalid argument — are now wrapped in `ErrPermanent`, and a dead token
arrives as an `UnregisteredTokenError` naming the token so the registration can
be pruned. Batching is available through `NewBatching` but off by default: FCM
has no idempotency key, so retrying a partly-delivered batch notifies the
devices that already received it a second time.

The editor's form covers every key, and a Go test reads the form and fails if
it writes a key the sink does not read, or if the sink reads one no field
writes.

### Fixed — twelve sink types were rendering the database form

Picking **API / Webhook** in the sink wizard showed host, port, database and
table fields. It was not a missing-field bug: `SinkWizard` resolved a type's
form as `configComponents[type] || configComponents['database']`, and twelve
types had no entry in that map, so they silently fell through to the database
form.

For `http` and `websocket` that made the sink unreachable rather than awkward.
The database form never writes a `url`, both types require one, and the wizard
disables Next *and* Save while a requirement is unmet — so there was no way to
create or edit one from any of the four entry points (Add Sink, Edit Sink, the
editor's node modal, the node drawer). Only the REST API could.

All twelve now have a form matched to the keys the factory actually reads:

- **API / Webhook** (`http`) — URL and headers, plus **compression** and
  **timeout**, which `createSinkBase` has always read and which had no input
  anywhere in the UI.
- **WebSocket** — URL, headers, subprotocols, the three timeouts, acknowledgement
  and the four TLS keys `buildWSTLSConfig` reads.
- **MQTT** — broker URL, topic, client id, credentials, QoS, retain, keepalive,
  clean session.
- **File**, **Stdout**, **Event Store**, and the five social sinks
  (Twitter/X, Facebook, Instagram, LinkedIn, TikTok).
- **MongoDB** and **Cassandra** keep the database form, now listed explicitly so
  it is a decision rather than a fall-through.

`MiscSinkConfig.tsx` held the correct `http` form all along but had been
imported by nothing since `ce5d533`; it is deleted. MQTT, File and Event Store
gained requirement gates, because their factory cases return an error rather
than degrading — saving one without them produced a sink that failed only when
it ran.

A test now fails if any type offered in the picker relies on that fall-through.

### Added — panmail sink

A new `panmail` sink sends each message as an email through a
[panmail](https://github.com/gsoultan/panmail) gateway's API, using
`github.com/gsoultan/panmail-sdk`. It sits beside the SMTP sink, which can reach
the same gateway through its SMTP door; what the API buys is the message id every
send returns — the handle delivery events and webhooks are keyed by — and
refusals that say which refusal they are.

Recipients, subject and both bodies are Go templates over the message, as in the
SMTP sink. A stored gateway template can be used instead.

**On retries and duplicate mail.** Sending is not idempotent and the gateway has
no de-duplication key, so a retry after a timeout may deliver a second copy. The
SDK refuses to make that call for you and never repeats a send whose outcome it
does not know; Hermod's `RetrySink` has no such discrimination and retries every
error alike. The sink resolves this with the idempotency claim:

- a refusal the gateway **stated** (rate limit, full backlog, bad key, bad
  argument) means the message was definitively not accepted, so the claim is
  released and a retry is free to take it;
- an **unknown** outcome keeps the claim, so the retry that follows finds the key
  taken and does nothing instead of mailing the recipient again.

With idempotency off there is nothing to hold the claim; the error says that,
rather than looking like any other failure. Turning it on is worth more for this
sink than for most.

The SMTP sink's idempotency-store wiring moved into
`internal/factory/idempotency.go` and is shared, with the sink name as the table
prefix so two sinks over one database cannot suppress each other's sends.

### Added — metis source and sink, for a BPMN workflow engine

Two new connectors reach a [Metis](https://github.com/gsoultan/metis) BPMN 2.0
workflow engine through `github.com/gsoultan/metis-sdk`, a client that depends on
nothing outside the standard library.

The **`metis` sink** turns each message into one act on the engine, chosen by its
`action` setting:

- `start_process` — start an instance of a deployed definition, so a committed
  database transaction is what begins the business process that answers it;
- `send_message` — correlate a message into whichever instance is already waiting
  on it, selected by a correlation key;
- `broadcast_signal` — reach every instance in the project listening for it.

The message's data map becomes the process variables, with the envelope (`id`,
`operation`, `table`, `schema`) written underneath it so a CDC row's own column
named `table` still wins. `variable_fields` narrows that to a named subset. The
definition key, message name, signal name and correlation key are Go templates
over the message.

**On retries and duplicate process instances.** Starting a process is not
idempotent and the engine has no de-duplication key, so this uses the same
idempotency claim as the panmail sink — but draws the line in a different place,
because the engine's failures are not the gateway's:

- a **stated** refusal (400, 401, 403, 404) means the request was rejected before
  anything was written, so the claim is released and a corrected retry may take
  it;
- an **unknown** outcome keeps the claim. That covers a transport failure *and a
  5xx*: a 500 is an answer that says the engine broke, not that it broke before
  committing the instance. Treating it as a refusal is what would start somebody's
  order-fulfilment process twice.

`TestWrite_ServerErrorKeepsTheClaim` fails when that classification is inverted,
so the distinction is verified rather than asserted.

The **`metis` source** polls a project and emits one message per row of a chosen
stream — `instances`, `tasks` or `incidents` — which is how process history
reaches a warehouse. Its listings are newest-first with no "since" filter, so the
source keeps the watermark itself: the `created_at` of the last row the pipeline
**acknowledged**, plus the ids of any rows sharing that exact instant, so a tie
is neither re-delivered nor dropped.

Only `Ack` moves it. Reading moves a second, in-process position that stops a
running poll re-reading what it just handed out, and that one is deliberately not
persisted — advancing the *persisted* cursor on read is the defect this
repository has fixed in ten other polling sources, and
`TestAck_AdvancesTheCursorAndReadDoesNot` fails when it is reintroduced.

The incidents stream carries a limitation the engine's API imposes: incidents are
listed per instance, not per project, so the source finds failed instances first
and asks each one. That is a request per failed instance per poll, and an
instance that fails after ageing out of `scan_pages` of the instance listing is
never asked.

Both connectors refuse plaintext `http` to a non-loopback host, because the
bearer token travels in a header; both take either a token or a username and
password, and prefer the password for a long-running pipeline, since only that
can log in again when the token expires. `Ping` lists projects rather than
writing, so a health check never starts somebody's process. Both are registered
in the `pkg/comm/conformance` contract suite.

`connectorRequirements` gained an optional `when` predicate on a required field,
because the metis sink's required name field follows its action: demanding a
definition key, a message name *and* a signal name at once would disable Next for
every configuration that is actually valid.

## [1.3.0] — 2026-09-11

The trace tables are the headline: listing traces was a sequential scan and
`message_trace_steps` stored every payload twice. Both are fixed, and
`currentSchemaVersion` moves to 2 as a result — **read the upgrade note below
before deploying**, because an older binary is refused rather than left failing
every trace write. The dashboard gains persisted history and panels for latency,
error rate, backpressure and circuit breakers. `db_lookup` gets a correctness
fix that made "Flatten Result" appear to do nothing, and the transformation
preview stops hiding what it did — including a CDC row that could lose its own
`table` or `id` column.

### Changed — message traces: half the disk, and a list that stops scanning the table

Two separate problems, one table. `message_trace_steps` is the largest thing
Hermod writes — a row per node per message — and it was both storing more than
it needed and being read in the worst possible way.

**Listing traces was a sequential scan.** The list query was
`SELECT DISTINCT message_id, MIN(timestamp) ... GROUP BY message_id`, which no
index can satisfy: every step a workflow had ever produced had to be aggregated
before the first 25 rows could come back. Measured on PostgreSQL 17 at 250k
steps — a Seq Scan, ~390 MB of buffer I/O and 58.5 ms to return 25 rows, growing
linearly with the table. A new `message_traces` table holds one row per traced
message, written alongside the steps, so the same page is an index scan:
**0.071 ms, and flat as the table grows.**

**Half the table was storing the payload twice.** `before_data` held what
entered a node, which is by definition the `after_data` of the node before it;
it is reconstructed on read instead. The `id` was a UUID written on every step
and selected by nothing — the same write-only column `dashboard_history` had.
Measured with realistic incompressible payloads, 250k rows: **262 MB → 123 MB.**

Also in this change:

- **Paging is by cursor.** `GET /api/workflows/{id}/traces` accepts `before`
  (RFC3339), so the next page is an index seek rather than a scan-and-discard.
  `limit`/`offset`/`page` still work; the trace viewer now uses the cursor.
- **One step cannot write an unbounded row.** Payloads over
  `HERMOD_TRACE_MAX_PAYLOAD_BYTES` (default 32 KiB) are replaced by a marker
  that is itself valid JSON, so the viewer still renders it.
- **Retention on PostgreSQL drops partitions instead of deleting rows.** New
  PostgreSQL tables are `PARTITION BY RANGE (timestamp)` with a day per
  partition, a DEFAULT partition so a lagging maintenance run can never fail an
  insert, and a week of lookahead so attaching one never waits on a DEFAULT
  scan. Expired days go by `DROP TABLE`: a catalogue change and an unlink, with
  no row-level WAL and the space returned immediately. Opt out with
  `HERMOD_TRACE_PARTITIONING=off`. Existing tables are left alone — a table
  cannot be altered into a partitioned one, and rebuilding one is an operator's
  decision to make in a window, not something to do unattended at start-up.
- **Tracing is off in `DefaultConfig`.** It defaulted to `TraceSampleRate: 1.0`,
  so any engine that did not apply the per-workflow rate traced every message.
  The registry does apply it, which meant the default only governed the paths
  that forgot — the ones nobody is watching. Off is fixable by configuration; a
  full disk is not.

**`currentSchemaVersion` is now 2.** This is the rollback the version exists to
block: the previous release inserts `id` and `before_data` by name and selects
`before_data`, and both columns are gone after this migration. An older binary
would fail every trace write and every trace read, so it is refused instead.

**Upgrading.** The columns are dropped in place where the engine allows it
(PostgreSQL, MySQL). SQLite cannot drop a primary key, so the `id` survives
there and the insert keeps supplying one — no saving on those databases, but no
breakage either. Two things worth doing deliberately:

- If the table is already huge, truncate or copy-and-swap **before** deploying.
  Nothing here bulk-deletes, but the first retention sweep after the parse fix
  above will.
- Traces recorded before this upgrade have no parent row and so will not appear
  in the list until backfilled. This is not run automatically because on a large
  table it is a long aggregate that would block start-up:

  ```sql
  INSERT INTO message_traces (workflow_id, message_id, started_at, last_step_at, duration_ms, step_count, error_count)
  SELECT workflow_id, message_id, MIN(timestamp), MAX(timestamp), COALESCE(SUM(duration_ms),0), COUNT(*),
         COUNT(*) FILTER (WHERE error IS NOT NULL AND error <> '')
  FROM message_trace_steps GROUP BY workflow_id, message_id
  ON CONFLICT DO NOTHING;
  ```

### Fixed — trace retention never ran, and `message_trace_steps` grew without bound

`purgeRetention` parsed each workflow's `trace_retention` and `audit_retention`
with `time.ParseDuration`, which has no `d` unit. The workflow editor defaults
the field to `7d`. So the parse failed **on the default value**, the call site
only acted when the error was nil, and the sweep was skipped — silently, for
every workflow, forever.

Nothing else bounds that table. `message_trace_steps` stores `before_data` and
`after_data`: the entire message payload, twice, per node, per message. The bug
was found on a deployment whose PostgreSQL grew **50 GB in a couple of hours**.

A day-aware `parseDuration` already existed in the same file, 1,400 lines below
the call site. Both call sites now use it, so `7d`, `30d` and `365d` work as the
field has always been documented. An unparseable value is now **logged as an
error** naming the workflow and the value, instead of being swallowed: there is
no safe fallback — defaulting to a short window would delete traces nobody asked
to lose, and defaulting to none restores exactly this bug — so the sweep keeps
the data and says why.

`message_trace_steps` also gained `idx_trace_ts` on `timestamp`. The purge
filters on that column alone and the only existing index was
`(workflow_id, message_id)`, so a sweep that now actually runs would otherwise
sequentially scan the largest table Hermod owns, hourly, once per workflow.
`audit_logs` has had `idx_audit_ts` for this reason all along.

**Upgrading with a table that is already huge:** truncate or copy-and-swap
*before* deploying this. The first successful sweep issues a single
`DELETE ... WHERE timestamp < ?` against everything past the window, which on a
50 GB table means tens of GB of WAL and a table still holding its dead tuples
until `VACUUM FULL`. `CREATE INDEX` on that table will also block startup until
it completes. Both are instant against an empty table.

**Still open — per-workflow trace retention is not per-workflow.**
`PurgeMessageTraces` executes `DELETE FROM message_trace_steps WHERE timestamp
< ?` with no `workflow_id` predicate, but the caller loops over workflows and
computes the cutoff from each one's own setting. A workflow with `7d` therefore
deletes the traces of a workflow configured for `365d`, and the sweep repeats
the same global delete once per workflow every hour. Fixing it needs a decision
about traces belonging to deleted workflows, so it is reported rather than
quietly changed.

### Fixed — the dashboard stopped updating when nothing was happening

The only thing that ever pushed dashboard statistics was `BroadcastStatus`, and
that is wired to an engine's status-change callback. With no workflow running
there is no engine, so nothing fired: the WebSocket delivered one snapshot when
the page loaded and then went silent. Measured against a running server, that
was one message in fifteen seconds — uptime, worker count and health frozen at
whatever they happened to be when the page opened.

This is the worst possible failure for a monitoring screen, because a dashboard
that has stopped updating looks exactly like a system with nothing wrong. The
registry now samples on a five-second tick regardless of engine activity, which
is the floor rather than the only source: `BroadcastStatus` still pushes on
engine activity so a busy pipeline stays responsive.

Two related gaps closed with it. The socket was opened once with no `onclose`
or `onerror`, so a dropped connection was never re-established and never
surfaced; it now reconnects with jittered backoff and the header says plainly
whether what you are reading is **Live** or **Reconnecting**. And every fetch
ended in `.catch(console.error)`, so an API returning 500 rendered as a tidy
dashboard full of zeros — which reads as "healthy and idle". Failures are now
shown.

### Removed — the dashboard's invented trend badge

The throughput card rendered a green "+5%" whenever throughput was above zero.
It was a literal `5` in the source, with no previous value behind it and no
period it referred to. A fabricated number on the one screen whose entire job
is to be believed costs more than the decoration was worth.

### Added — latency, error rate, backpressure and circuit breakers on the dashboard

The engines already computed all of this per workflow and it had nowhere to go:
`telemetry.StatusUpdate` carried average latency, sink buffer fill and per-sink
circuit-breaker state, and the dashboard read throughput and lag from it and
dropped the rest. It is now aggregated across running engines, each with the
combining rule the quantity actually calls for — throughput sums, latency
averages over engines that report one, and backpressure takes the *worst* sink
rather than the mean, because one jammed sink among nine idle ones is a stalled
pipeline and averaging it to 10% is the reading least likely to get anyone to
look.

Error rate is derived from the persisted counters as the dead-lettered share of
everything attempted. There is deliberately no separate dead-letter count:
`total_errors` already is that number, and showing one value twice under two
names is how a dashboard loses the reader's trust.

The page also now shows the six figures the API had been returning all along
and the UI discarded — lag, failed workflows, uptime, and the running-against-
configured counts for sources and sinks.

### Added — persisted dashboard history

The throughput chart lived entirely in React state, so every reload threw the
trend away and restarted from a flat line, making a page refresh
indistinguishable from an outage. Samples are now written to a new
`dashboard_history` table on the same five-second tick and the chart is seeded
from `GET /api/dashboard/history` on load.

The global series is always kept, because history exists to answer questions
asked after the fact and a series that only accrues while someone has the page
open is missing for exactly the outage nobody was watching. Per-tenant series
accrue while that tenant's dashboard is open. The table is swept on the
existing hourly retention pass with a seven-day window, and both the window and
the row limit on the endpoint are clamped, since both come off the query string.

`currentSchemaVersion` is deliberately unchanged. The previous release never
reads or writes `dashboard_history`, nothing existing changed shape, and no
foreign key points at it, so a rollback leaves the table unpopulated rather
than misread — and bumping the version would have refused start-up during
exactly the rollback it was meant to make safe.

**What it costs, and how to spend less.** This is the only append-only table in
the metadata database — a row every five seconds per watched vhost, whether or
not anyone is looking — so its footprint is a feature of the product, not an
implementation detail. Measured in SQLite over one week of one series it is
**14.08 MB**, down from 21 MB as first written:

- the surrogate UUID primary key is gone. Rows here are never addressed
  individually — every read is a range scan over `(vhost, timestamp)` and every
  delete a range sweep — so the id was written on every tick and selected by
  nothing. It was 9.6 MB of a 21 MB table, 46% of the disk, and on MongoDB a
  36-byte random `_id` has been replaced by the driver's 12-byte ascending
  ObjectId;
- timestamps are truncated to the second. A five-second sample never had
  microsecond precision, and the SQLite driver stores a `time.Time` as text, so
  those digits were bytes in the row and again in the covering index.

Two new environment variables make the rest adjustable, because the right
answer depends on the box: `HERMOD_DASHBOARD_SAMPLE_INTERVAL` (default `5s`)
and `HERMOD_DASHBOARD_HISTORY_RETENTION` (default `168h`). They multiply —
`30s` at `24h` is roughly 1/42 of the default footprint. Setting retention to
`0` turns history off entirely and sweeps what is already there: the dashboard
stays live, the chart just shows what the open page has collected. A malformed
value falls back to the default rather than to zero, so a typo cannot silently
delete the series.

Backends that cannot store history — Pebble, and any worker node, which reaches
the control plane over HTTP and owns no database — now say so with
`hermod.ErrNotSupported` instead of a bare error or a silent `nil`. The sampler
latches that and stops asking. Previously Pebble logged a failure every five
seconds forever, which made the backend that stores no history the one that
wrote the most to disk.
### Fixed — `db_lookup` stopped flattening after the first message with a given key

`flattenInto` ("Flatten Result") was applied only on the path that actually
queried the database. Every lookup is cached, and with no Cache TTL configured
it is cached forever, so the second message carrying a given key took the
cache-hit path — which wrote the target field and returned, skipping flattening
entirely. One configuration therefore produced two different message shapes:
message one had the columns as fields, every message after it did not.

The editor made this look like the feature simply did nothing. The live preview
re-runs on a debounce, so by the time an operator turned Flatten Result on and
pressed **Run Preview**, the lookup was already cached and the flattened fields
never appeared.

Both paths now go through one function, so what is written into the message no
longer depends on whether the row came from the database or the cache.

The cache key also interpolated the key value with `%v` alone, so the string
`"1"` and the number `1` hashed to the same entry and two lookups keyed on the
same id in different types served each other's rows. The key now includes the
type.

### Added — the preview panel states what happened to the target field

A lookup that matches no row passes the message through unchanged and without an
error (`onMiss` defaults to passthrough), so a miss and a working lookup rendered
as identical JSON. For a CDC sample the enriched field also lands nested inside
`after`, while every field picker in the editor shows the hoisted root path — so
an operator looking for `user_details` saw `after`.

The panel now reports the node's target field above the JSON: its value when the
field was produced, and **Not produced** with the reason when it was not. The
path is resolved the same way the rest of the editor resolves it, so the panel
and the field pickers agree.

### Added — `db_lookup` lets you choose what a miss does

`onMiss` has been in the transformer since miss policies were introduced, and
its whole purpose is that a lookup finding no row should be an explicit,
auditable choice rather than a silent passthrough. The editor never offered the
choice, so every workflow ran on whichever policy was inferred from whether
Default Value happened to be filled in.

The Advanced tab now has **When no row matches**: pass the message through, write
the default value, or fail the message. It mirrors the backend's inference, so
an unset policy displays the one actually in force rather than a blank field,
and it warns when "write the default value" is selected with no default value —
a combination that writes nothing and behaves exactly like passthrough.

### Added — `db_lookup` names the paths its output will have

Value Column(s), Target Field and Flatten Result combine into four different
output shapes, and nothing said which one the current settings produce. The
Output Mapping tab now spells out the paths the next node can address — a single
value, an object at the target field, every column of the row, or the flattened
per-column paths — instead of a general tip about objects.
### Fixed — a previewed CDC row lost its own `table`, `id` or `operation` column

`populateMessageFromMap` copied the message envelope's system fields into the
data map "for convenience in transformations". `ToMap` serialises a CDC message
by marshalling that same data map as the after-image, so two things followed.

Every system field appeared twice in a previewed message — once at the root,
once inside `after` — which is most of why the preview panel was hard to read.

Worse, the copy raced the after-image. Both write the same key and Go randomises
map iteration order, so a row with a column called `table`, `id`, `operation`,
`op` or `schema` kept the envelope's value instead of its own in roughly three
previews out of four. The operator then mapped downstream nodes against a value
the row does not have. Measured before the fix: 15 of 20 runs lost the column;
after, 20 of 20 keep it.

The copy was never needed. `evaluator.GetMsgValByPath` already exposes
`operation`, `op`, `table`, `schema` and `id` as virtual fields resolved from the
message itself, and deliberately lets a real data column of the same name
outrank them — leaving these out of the data map is what gives that rule
something to resolve against.

Only the preview is affected: a live CDC source sets the after-image as the
payload (`SetAfter`), so `ToMap` never falls back to the data map for it.
Non-CDC samples are untouched, because there is no after-image to duplicate into
and dropping the copy would change the previewed type of a field like `id`.

### Added — detect decryption settings from a sample value

The decrypt node's settings interact: the key format decides the key bytes, the
encoding decides the payload bytes, the nonce length decides where the
ciphertext starts, the tag position decides which end the tag is on, and the AAD
decides whether authentication can succeed at all. One wrong setting is
indistinguishable from all of them wrong, so matching an external system meant
searching that space by hand with nothing to search by. 1.2.0 made every
combination expressible; it did not make the right one findable.

**Detect settings** in the decrypt editor takes one encrypted value and the key
and reports the configurations that actually read it, each with a truncated
preview and an Apply button. `POST /api/transformations/detect-decryption` is
the same thing for scripting.

Confidence is load-bearing rather than decorative. `certain` means an
authenticated algorithm verified its tag, so the configuration is not a guess —
nothing else could have produced that value. `likely` means an unauthenticated
mode produced plausible-looking text, which on a short value can be coincidence,
and the UI says so at the point of applying it. Ranking puts proven candidates
first, and candidates differing only between the base64 and base64url alphabets
are collapsed, since offering a choice that is not a choice is noise.

Two things are deliberately not searched, and the failure reason says so instead
of leaving them to be discovered. PBKDF2 and scrypt take a salt and cost
parameters that are inputs rather than properties of the ciphertext — guessing a
salt is a dictionary attack, not a search. A fixed IV has nothing in the payload
to recover it from.

The endpoint is editor-only and grants no capability its caller lacked: it needs
the key, so anyone able to call it could already decrypt. It never echoes the key
back, sends `Cache-Control: no-store`, and truncates the preview so it cannot be
used to drain a column.

### Fixed — a source payload that was not a JSON object was silently dropped

Sources are free to deliver a bare string, a number, or bytes that are not JSON
at all: a RabbitMQ queue carrying plain text, a Kafka topic of CSV lines, a file
read in `raw` mode. Only JSON *objects* survived. Anything else reached the sink
as `{"id":"…","metadata":{…}}` — the body gone, no error logged, nothing in the
trace. `MarshalJSON` unmarshalled the payload into the output map and discarded
the error, so a payload with no fields to merge simply left nothing behind.

A payload that is not a JSON object is now preserved under a `payload` field,
holding the decoded JSON value where there is one (`42` stays a number, `[1,2]`
stays an array) and the literal text otherwise. It is addressable by
transformations and by sink templates as `{{.payload}}`.

Three consequences of the same root cause are fixed together:

- **Bodies reaching the sink.** Any sink configured with `format: json` or
  `format: cdc` now emits the body. Sinks with no `format` set were never
  affected — they publish `Payload()` bytes directly and always passed strings
  through untouched.
- **The workflow editor and message traces.** `ToMap` carried the same ignored
  error, so a string payload rendered as an empty body in the test/preview panel
  and in traces. It now agrees with `MarshalJSON`, and a test pins them together.
- **CDC messages whose payload was not JSON.** These failed to marshal at all
  (`invalid character 'h' looking for beginning of value`), failing the sink
  write rather than losing the body quietly. The `before`/`after` envelope now
  wraps such bytes instead of rejecting them.

The S3 Parquet sink needed a matching change. It refused a record it could not
build a row from by testing `Data()` for emptiness, which is exactly the
invariant that moved: a non-object payload now decodes to one synthetic field,
so the check passed the record through to the writer and the batch failed in
`WriteStop` with `interface conversion: interface {} is nil, not string` --
naming neither the record nor the reason. The guard now measures a record
against the schema's own columns, which is what it always meant.

The fix is in the message layer, so every source benefits without connector
changes. JSON object payloads serialise exactly as before. Array payloads were
already exposed under `payload`, which is why that name was widened to cover
strings and scalars rather than a new one introduced: existing `{{.payload}}`
templates keep resolving. Arrays also gain determinism — whether an array
survived used to depend on whether a transformation had read the message first.

## [1.2.0] — 2026-09-10

The `encrypt` and `decrypt` transformations added in 1.1.0 gain an algorithm
picker, and `decrypt` stops silently doing nothing on data Hermod did not write.
`enc:v1:` values written by 1.1.0 keep decrypting unchanged under the default
configuration; a regression test pins that literal wire format.

Nothing in the public Go API changed and no dependency moved, so this carries no
security fix of its own — but the bug it fixes is a security-relevant one: a
decrypt node pointed at data another system encrypted reported success while
forwarding ciphertext to the sink. If you run one, check that it is actually
decrypting rather than passing values through.

### Fixed — `decrypt` silently did nothing on data Hermod had not encrypted

In 1.1.0 the node only touched values carrying its own `enc:v1:` prefix and
passed everything else through. That is right midway through a rollout, when a
column holds a mix of sealed and plain values — and it meant that pointing the
node at a column encrypted by *another* application matched nothing, changed
nothing, and reported success, with no error and nothing in the logs. The
pipeline looked healthy while forwarding ciphertext to the destination, which is
the same class of failure as an unknown transformation type resolving to a no-op.

Setting `format` to `raw` now drops the envelope: every listed field is
decrypted, and a value that will not decrypt is an error rather than a
pass-through. Together with `ivPlacement`, `encoding` and the key formats below,
that is enough to describe what an external system actually wrote — verified in
both directions against `openssl enc -aes-256-cbc`. For operators staying on the
envelope format, the new `onPlaintext` policy (`passthrough`, the default,
`fail`, or `null`) turns the silence into a stopped pipeline once a rollout is
complete.

### Added — an algorithm, key-derivation and encoding picker

- **Fourteen algorithms.** AES-128/192/256 in GCM, CBC, CTR and CFB, plus
  ChaCha20-Poly1305 and XChaCha20-Poly1305. AES-256-GCM stays the default. GCM
  and Poly1305 are authenticated and detect a modified ciphertext; CBC, CTR and
  CFB cannot, and are offered so that data another system already wrote in them
  can be read at all. The editor says which is which at the point of choosing,
  and a Go test fails the build if the two lists ever drift apart.
- **Key derivation is selectable.** `passphrase` (SHA-256 of the text — 1.1.0's
  behaviour, and still the default), `raw`, `hex` and `base64` for key material
  of exactly the algorithm's length, and `pbkdf2` or `scrypt` for a human-chosen
  password. A raw key of the wrong length is an error rather than padded or
  truncated: padding leaves the remaining bytes known to an attacker, and
  truncating means two keys sharing a prefix encrypt identically, so an operator
  rotating between them would see success and get no rotation. PBKDF2 and scrypt
  require a salt and have no default for it.
- **Payload encoding is selectable** — `base64`, `base64url` or `hex` — and
  decoding accepts either base64 alphabet with or without padding, because
  external systems disagree about both and the difference otherwise looks
  exactly like a wrong key.
- **`enc:v2:` names its algorithm.** A value can then be read without the node
  being told how it was written, and a node explicitly configured for a
  different algorithm reports the conflict instead of failing with a generic
  authentication error. A node configured the way 1.1.0 behaved still emits
  `enc:v1:`, so a mixed-version fleet keeps working during a rollout.
- **Optional `aad`** binds additional authenticated data to the ciphertext for
  the GCM and Poly1305 modes. It is rejected for the unauthenticated modes
  rather than accepted and dropped, which would suggest a binding that does not
  exist.

Three limits are worth knowing before leaving the defaults. In `raw` format
nothing marks a value as encrypted, so encrypting into it is not idempotent —
running the same workflow twice encrypts the column twice. The optional fixed IV
exists only to match external systems that use one: it makes identical inputs
produce identical ciphertext, and under GCM or CTR reusing an IV with one key
exposes the XOR of the two plaintexts and, for GCM, the authentication key. And
CBC, CTR and CFB cannot detect tampering at all — a wrong key yields plausible
garbage rather than an error, except where CBC's padding check happens to catch
it.

### Added — decrypted JSON can become an object

A column often holds one JSON document rather than a scalar, and decrypting it
returned a *string* that happened to contain JSON. That is not the same as an
object: no downstream node could address into it with a dotted path, and the live
preview rendered it as a single escaped line instead of a tree.

`decrypt` now takes **`parseJson`** — `off` (the default), `objects`, or
`strict`. `objects` parses a value that starts with `{` or `[` and leaves
everything else as text; `strict` treats the whole value as a JSON document,
scalars included, and routes a parse failure through the existing `onError`
policy. Parsing is opt-in because it changes a field's type, and doing that
silently would reshape every message flowing through an existing node.

The split between the two modes is deliberate. A decrypted `"12345"` is valid
JSON, so a single "parse if you can" mode would quietly turn an account number
into a float — a schema change downstream that nobody asked for. `objects` never
does that; `strict` is how an operator asks for it, and is also what complains
when a column declared to be JSON is not.

`encrypt` gained the inverse, **`serializeJson`**. It previously refused a field
holding an object, because rendering a map with `%v` produces Go syntax that
decrypt would hand back as a literal string. With the opt-in the subtree is
marshalled and sealed as one document, so an object survives a full round trip
and a later node can mask or map `payload.contact.email` directly. Without it the
refusal stands, and the error now names the option that lifts it.


### Added — an explicit AAD mode, and a diagnosis for authentication failures

An authenticated algorithm reports a wrong key, a wrong AAD and a tampered
ciphertext identically. That is correct — GCM cannot distinguish them — and it
means one opaque error covers every setting on the node. Configuring a decrypt
node against a system you do not control turns into guesswork, and the guess
people reach for first is "the key must be wrong", which it usually is not.

`diagnose` on the decrypt node decrypts once **without** checking the tag and
reports which half is wrong: either the key and framing are right and the AAD is
the difference, or the trial produced nothing sensible and the AAD is not worth
looking at. The trial result is never returned or logged — only the
classification — because handing back unauthenticated plaintext is exactly what
the tag exists to prevent. It is off by default, both for that reason and
because reporting whether forged input decrypts to something plausible is a
small oracle to expose on a hot path. The check reads "plausible" as well-formed
text, so a binary plaintext reads as "key wrong" even when the key is right; the
message says so rather than overstating what it knows.

Alongside it, **`aadMode`** makes the AAD an explicit choice — `none` (the
default), `value`, or `key` — instead of inferring it from whether a text box is
empty. An empty box could equally mean "no AAD" or "not filled in yet", and the
two produce ciphertext that cannot be told apart until it fails to open;
selecting `none` now also genuinely drops a value left behind in the field
rather than leaving it quietly in effect. Nodes that set only `aad`, from before
the mode existed, keep working unchanged.

`aadMode: "key"` covers systems that pass the encryption key itself as the AAD.
It buys nothing — the key is already bound to the ciphertext by construction —
but it is what their data requires, and without the preset there is nothing to
discover it from, because the failure is identical to a wrong key.


### Added — tag placement and nonce length for AES-GCM

The last two ways an external AES-GCM value can be framed differently from Go's.
`gcm.Seal` appends the authentication tag, so a Go-written value is
nonce||ciphertext||tag with a 12-byte nonce. Node's crypto, Java's Cipher and
.NET's AesGcm all return the tag *separately*, which leaves whoever wrote the
storage code to choose where it goes — and putting it in front of the ciphertext
is a common choice. 16-byte nonces appear for the same reason: nothing stopped
them.

Neither is recoverable from the bytes. Both layouts are the same length and both
fail authentication in the same way, so `tagPlacement` (`suffix`, the default,
or `prefix`) and `nonceSize` have to be told rather than inferred. Both work for
writing as well as reading, so a pipeline can produce data a partner system
consumes instead of only consuming theirs.

`nonceSize` is GCM-only: the Poly1305 constructions fix their nonce as part of
the construction and the block modes take a full 16-byte IV, so setting it there
is rejected rather than silently ignored. It is also read by presence rather
than by value — an explicit `nonceSize: 0` is a configuration error, and a
sentinel of 0 would have quietly accepted it as "unset". The nonce length is
part of the derived-cipher cache key, because it changes the constructed AEAD
and two nodes sharing a passphrase must not share an entry across it.

Fixtures for both come from Node's crypto module rather than from this package,
so they test interoperability instead of self-consistency.


## [1.1.0] — 2026-09-09

One new capability and a set of connector-wizard fixes. Nothing in the public Go
API changed, and no dependency moved, so this carries no security fix of its own.

### Added — field-level encryption and decryption

Two transformations, `encrypt` and `decrypt`, seal and unseal named fields with
AES-256-GCM. The configured key is hashed to 256 bits, so any length works, and
encrypt and decrypt nodes must be given the same one.

Three decisions are worth stating, because each one closes a failure that the
obvious implementation leaves open:

- **Ciphertext is tagged `enc:v1:`.** Without a marker the two transformations
  cannot tell ciphertext from plaintext. Re-running a workflow would encrypt an
  already-encrypted column a second time, and decrypt could not distinguish
  "never encrypted" from "corrupt". With it, encrypt skips sealed values and
  decrypt passes untagged ones through — which is also what a column holds
  midway through a rollout. The version segment leaves room for a second scheme
  that does not strand data written under this one.
- **Every value gets a fresh random nonce.** Identical plaintexts therefore
  encrypt differently, which is the safe default and the reason an encrypted
  field cannot be used as a join or lookup key downstream.
- **Both fail closed.** A missing key or an empty field list is an error rather
  than a silent pass-through: a step asked to encrypt that quietly forwards
  plaintext is the whole failure. Decryption failures — a wrong key or an
  altered ciphertext, which GCM reports identically — fail the message by
  default; `onError` can relax that to `skip` or `null`.

There is deliberately no `*` wildcard. Mask has one, but masking every field
degrades a message where encrypting every field destroys it, keys and routing
columns included. Only scalar values can be encrypted: rendering a map with `%v`
gives Go syntax, and decrypt would hand that literal string back in place of the
object, so a field naming an object is refused rather than silently mangled.

Two operational limits worth knowing. The nonce is 96 random bits, and NIST
SP 800-38D caps a key used that way at 2^32 encryptions — reachable on a busy
pipeline, and rotation is what resets it. Values are read through the same JSON
path every other node uses, so a `[]byte` field is already its base64 form by
the time it is encrypted, and round-trips as base64.

The key is set on the node, so it is stored with the workflow definition and is
readable by anyone who can read or export that workflow. Rotating it does not
re-encrypt data already written under the old key.

### Fixed — connector wizards could not be completed

The connection step's **Next** button read config keys that nothing produced.
For a `rabbitmq_queue` source the gate required `url` and `queue`, while the
form writes `host`, `port`, `username`, `password`, `dbname` and `queue_name`
and hides the URL input once a host is set. Neither key was reachable, so the
button was dead for every RabbitMQ queue source and sink.

Test Connection succeeded at the same moment, which is what made it baffling:
`BuildConnectionString` assembles the AMQP URL from the host fields and the
factory reads `queue_name` separately, so the step genuinely worked while the
gate that guarded it asked for fields the user could not fill.

Two connectors had drifted the same way. `mqtt` required `broker` where the form
writes `broker_url`, giving the same dead button; it now also requires a topic,
because the source refuses to start without one. The `snowflake` sink required
`dsn` where both the form and the factory use `connection_string`, so its
"Required:" message named a field the form does not show.

Requirements gained an alias list, so "a server" is one requirement satisfied by
a host *or* a whole connection string, mirroring `BuildConnectionString`'s own
precedence instead of keeping a second copy of it that can drift.

Two further gaps in the same area closed with it:

- **A pasted connection string satisfied every requirement, not just the ones it
  replaces.** Any `uri` or `connection_string` cleared the whole step, so a
  MongoDB source with a URI but no database or collection advanced and failed
  later — at a screen that no longer showed the fields, which is the failure the
  gate exists to prevent. A pasted string now satisfies the host-shaped fields
  only; the factory reads database and collection from their own keys and cannot
  derive either.
- **A connection URL could override the host fields while invisible.**
  `BuildConnectionString` prefers `url` over host and port, but the RabbitMQ
  forms rendered that input only while the host was empty. A URL entered first
  kept winning from behind a field that had disappeared, so editing the host
  changed nothing and Test Connection reported on whichever server the hidden
  URL named. It now stays on screen whenever it holds a value, and its label
  says that it overrides.

### Fixed — palette categories could render under the wrong heading

Category titles are unique only within a group: "Databases", "Messaging &
Streams" and "Social Media" each name both a source and a sink group. The
palette's combined tab keyed one list by title, so three pairs collided and
React warned twelve times per render of the workflow panel. Duplicate keys let
React reuse or drop the wrong child, meaning a category's contents could appear
under another category's heading. Keys now pair the group with the title.

## [1.0.0] — 2026-09-07

The first generally available release. It is `1.0.0-rc.2` plus one concurrency
fix; no dependency changed, so it carries no security fix of its own and
`1.0.0-rc.1`'s advisory remains the current one.

### Fixed — a health pass could clear a stall it raced

`checkHealth` publishes `"running"` whenever the source is up and every sink
answers `Ping`, and a wedged sink does answer `Ping`: it accepts the connection
and never completes a write. So the stall watchdog and the health pass both
write the engine status, and the watchdog has to win.

An earlier fix guarded the write with `engStatus != "stalled"`, which closed the
case of a health tick arriving *after* a stall. It could not close the case
inside a single tick: `engStatus` came from an earlier `GetStatus`, and the
write was a separate lock acquisition, so a watchdog setting `"stalled"` between
the two was overwritten by a guard that had already decided the pipeline was
fine. A supervisor was told the workflow had stalled while the status the UI
reads said it was healthy.

The exclusion moved into the write. `StatusTracker.SetEngineStatusUnless`
decides and publishes under one lock and reports whether it wrote.

The window was only as wide as the gap between the two calls, so it never
reproduced on a developer machine and surfaced instead as an intermittent CI
failure. Both halves of the fix now have a test that fails without it: the
tracker races 2000 pairs of writers, and the engine-level test races the
watchdog against `checkHealth` 300 times.

### The version number, and what it does not buy you

`1.0.0` is the release you can run: the container image, the Helm chart, the
binaries and the git tag were all free at this number.

It is **not** installable with `go get`, and no future release can make it so.
`proxy.golang.org` is immutable and permanently maps `v1.0.0` to the February
commit that carried that tag, under `module github.com/user/hermod` — a path
matching no repository. That is unchanged from
[`go get` does not work at this version, by choice](#go-get-does-not-work-at-this-version-by-choice),
and the `retract` block in `go.mod` deliberately still covers `v1.0.0`:
narrowing it would un-retract the February commit without making this one
reachable, and would break the plain `go get` that currently resolves cleanly to
the newest candidate.

Consume this release as an image, a chart or a binary.

### Known gaps

Everything listed under Known gaps in `1.0.0-rc.1` still applies; none of it was
addressed here. The five social connectors that advance their cursor on read —
Twitter/X, LinkedIn, Facebook, Instagram and TikTok — remain the most
significant: treat a restart as potentially lossy for those.

## [1.0.0-rc.2] — 2026-09-07

The second release candidate. Almost all of it is the editor UI: a measured pass
over rendering, navigation and forms, plus the first-run defect that made a
fresh install unable to start a workflow without a restart. No dependency
changed, so nothing here carries a security fix; `1.0.0-rc.1`'s advisory
remains the current one.

### Fixed — a fresh install could not run anything until you restarted it

A first run has no database, so `shouldStartWorker`
(`cmd/hermod/worker_util.go`) was false at process start and `main` built no
worker. Setup then opened the database the admin chose but left the registry
holding `nil`, so every toggle failed with `registry storage is not
initialized`, with nothing on screen to say a restart was needed. Every
`dev.sh --reset` stack and CI's E2E job ran in that state.

Setup now announces the database it opened and `main` answers by starting a
worker — last, after every setup step has succeeded, and behind a `sync.Once`
so a second call cannot put two workers on the same workflows.

Two data races surfaced alongside it, both on state that is now swapped while
the process runs. `Registry.SetStorage` took `r.mu`, but 57 of the 70 reads of
`r.storage`/`r.logStorage` took nothing, on request paths and on the stats and
retention tickers. `r.mu` could not be the fix — `StartWorkflow` holds it and
calls `ValidateWorkflow`, which reads storage, and `sync.RWMutex` is not
reentrant — so the two fields moved to their own `storeMu` behind `store()` and
`logStore()`. `Handler.Worker` gained the same treatment.

### Fixed — the editor did work on every keystroke and every render

- **Preview ran once a second while idle.** `usePreviewTransformation` spread a
  mutation result into a fresh object each render, so the effect debouncing it
  re-armed its timer every render and the 1s debounce behaved as a 1s poll.
  Measured at 4 requests in 5.6s with no user input.
- **Column discovery queried the sink's own database on every keystroke of a
  table name.** The `mappings.length === 0` guard only closes once a discovery
  succeeds, and a partial table name is not a table, so typing `orders` issued
  six live queries.
- **Raw-JSON panes discarded what you were typing.** They were controlled off
  the node config, so half-typed JSON failed to parse, committed nothing, said
  nothing, and the next unrelated re-render replaced the text with the last
  serialised config. A keystroke that did parse came back reformatted, moving
  the caret to the end.
- **Lists blanked between keystrokes.** No query set `placeholderData` and only
  `WorkflowDetailPage` debounced, so each character minted a query key, data
  dropped to `undefined`, and the table emptied and refilled. Logs did this
  while polling every 5s. Now debounced at 300ms with the previous result held,
  across Workflows, Sources, Sinks, Users, Logs and Audit Logs.
- **`FlowCanvas` merged edges with `JSON.parse(JSON.stringify(...))`.** It read
  as a deep-equality guard and did the opposite: a new object per edge per
  change, so React Flow's memoisation never held and every edge re-rendered
  whenever any one did — several times a second under live telemetry. It also
  silently mangled anything JSON cannot carry.
- **`SinkForm` fetched workers with a bare `useEffect`** — no cache, no dedupe,
  no abort, a request per mount, and a `setState` after unmount if the form
  closed mid-flight. `SourceForm` already read the same list through React
  Query, so one resource had two mechanisms.
- **Seven transformation types rendered their configuration twice.** Config
  moved to a registry but the inline blocks it replaced were never deleted, so
  set, advanced, pipeline, lua, wasm, foreach and aggregate showed their
  settings under both Configuration and Advanced, with stale labels the second
  time. 288 inline lines removed.
- **Forms hydrated over your edits.** `useSourceForm` re-parsed `initialData`
  on every keystroke, restoring a sample `updateConfig` had just cleared;
  `UserForm` and `VHostForm` re-hydrated on every `initialData` identity, so a
  refetch after their own save overwrote in-progress edits.
- Routing nodes previewed the `{branch, result}` envelope instead of the
  message, and `WorkflowsPage` crashed on a non-array `/api/workspaces`
  response.

### Changed — navigation, first paint and layout

- **Cold loads no longer flash white.** The app renders dark by default but the
  theme was applied only after hydration, and `index.html` set no background.
  It now inlines Mantine's own `ColorSchemeScript` algorithm, a `color-scheme`
  meta and matching `theme-color`, and `#root` holds a shell placeholder. First
  paint measured at `rgb(26,27,30)`.
- **Navigation stopped paying a fixed cost.** Router `defaultPreload: 'intent'`
  warms a route's lazy chunk on hover instead of fetching chunk then data in
  series; `defaultPendingMinMs` drops 500 → 0 and `defaultPendingMs` rises
  0 → 300, so a warm route that paints in 20ms no longer costs a pinned 500ms
  full-viewport spinner.
- **The manual chunk buckets are gone**, measured rather than assumed. The
  `reactflow` rule matched a package renamed to `@xyflow/react` long ago, so it
  only ever caught dagre and d3 — and fixing the name made it worse, promoting
  the graph library and drag-and-drop kit onto the login screen's critical path
  at 1.45MB. Rolldown's own splitting tracks the eager/lazy boundary properly.
- **One width grammar for every form.** A measured audit found five different
  grammars across six forms. `<Group grow>` was the broken one — a flex row
  that never wraps, squeezing host/port/user/password to ~80px on a narrow
  viewport instead of stacking. 72 of these became `FormRow`, a `SimpleGrid`
  that does collapse and bottom-aligns, replacing a global `min-height: 1.2em`
  that only ever reserved one line.
- The editor toolbar at 390px was a non-wrapping flex row; it wraps now.
- Two theme blocks were silently dead: Mantine `styles` become inline styles,
  so nested selectors in them were not CSS rules — React rejected them and
  logged an error every render. Active nav links rendered at weight 400 instead
  of 600 and table headers had no background in either scheme.

### Added

- **Search in the workflow palette.** It lists well over a hundred sources,
  sinks and transformations across three tabs and a dozen categories, and the
  only way to find one was to scroll. Labels, sub-types and descriptions are
  all searched — so "drop records" finds Filter — ignoring spaces, underscores
  and slashes, because the catalogue is not consistent about them. A tabbed
  palette hides matches by design, so an empty tab reports where they are, and
  says so rather than offering a link when the target tab is locked.
- **The PWA is finished**: an explicit manifest `id` (it was derived from
  `start_url`, so changing that later would have orphaned every install),
  192/512 PNG icons, a maskable icon inside the inner 80% safe zone, and an
  `apple-touch-icon`, which iOS needs because it ignores the manifest for Add
  to Home Screen.
- **Accessible names that describe the right action.** A bulk find-and-replace
  had given icon buttons the wrong ones: copy-to-clipboard announced "Confirm",
  generate-password announced "Refresh", clear-stream announced "Delete", and
  ten delete buttons in a table all announced "Delete" with nothing to say
  which row. 26 corrected.
- **A bundle budget in CI.** `check-bundle-budget.mjs` sums every script and
  stylesheet `index.html` references and fails past 781kB; measured 736kB. One
  eager import of a page-only library puts it back to the 1,092kB it was before
  the buckets came out, with nothing else in CI noticing.
- Editor measurement scripts (`measure-editor.mjs`, `profile-editor-cpu.mjs`,
  `visual-sweep.mjs`) used to settle the memory and worker-offload questions
  rather than argue them. The apparent +17MB peak on the new code was GC sample
  timing: 4.55 vs 4.35MB sampled over 30s, identical composition.

### Developer experience

- `scripts/dev.sh` chooses its ports rather than assuming they are free. It
  prefers 4005 (API), 50051 (gRPC) and 5175 (UI) and steps up when one is
  taken, so a second stack no longer fails with `address already in use`. The
  gRPC port was previously unmanaged, and a failed bind on it is fatal for the
  whole process.

### Known gaps

- Everything listed under Known gaps in `1.0.0-rc.1` still applies; none of it
  was addressed here.
- The 239kB stylesheet costs 36.8kB over the wire with FCP at 44ms on the
  production build. Splitting it was measured and judged not worth the work,
  and is recorded rather than done.

## [1.0.0-rc.1] — 2026-09-03

A release candidate, not a final release. Everything below is tested and the
gates are green, but most of it landed within the last few weeks and none of it
has yet run in a production deployment. `1.0.0` follows once it has.

### Security

- **`golang.org/x/crypto` 0.54.0 → 0.56.0**, for [GO-2026-6354] and
  [GO-2026-6355] — two denial-of-service defects in `golang.org/x/crypto/ssh`
  where a channel can deadlock on an established connection. Both are reachable
  from the SFTP file source, at `pkg/comm/source/file/generic.go:736`, where
  `sshDialContext` calls `ssh.NewClientConn`.

  The connection is outbound, so reaching it means Hermod has been configured to
  poll an SFTP server that is hostile or has been compromised — not an exposure
  anyone can reach unprompted. It is still reachable, which is why this is an
  upgrade rather than an exemption.

  The handshake was already bounded by a deadline, cleared immediately
  afterwards so transfers are not cut short. That is exactly the window these
  advisories describe, so the timeout does not mitigate them.

  `golang.org/x/text` (0.40.0 → 0.41.0) and three indirect `golang.org/x`
  modules moved with it.

[GO-2026-6354]: https://pkg.go.dev/vuln/GO-2026-6354
[GO-2026-6355]: https://pkg.go.dev/vuln/GO-2026-6355

### The releases before this one are gone

Every earlier release has been deleted: 52 GitHub releases, all 55 tags, and
both GHCR packages (`hermod` and `charts/hermod`). Nothing published under a
`1.x` number before 2026-09-03 is available any more, and none of it should be
treated as a supported upgrade path to this release.

**Container images and charts are gone.** `ghcr.io/gsoultan/hermod:1.7.3`,
`:latest`, and the matching `charts/hermod` versions no longer resolve. If you
are running one, keep your local copy until you have moved to `1.0.0-rc.1`; it
cannot be pulled again.

### `go get` does not work at this version, by choice

The version numbering restarts here, and that is possible everywhere except one
place: `proxy.golang.org` is immutable, and it still maps `v1.0.0` to the commit
that carried that tag in February, under the old `module github.com/user/hermod`
which matched no repository.

```
$ curl proxy.golang.org/github.com/gsoultan/hermod/@v/v1.0.0.info
{"Version":"v1.0.0","Time":"2026-02-09T07:38:40Z","Hash":"915f5346..."}
$ curl proxy.golang.org/github.com/gsoultan/hermod/@v/v1.0.0.mod
module github.com/user/hermod
```

Re-tagging does not change what the proxy serves. Every number from `v1.0.0` to
`v1.7.4` is spent for this module path, and `v1.0.0` has to stay retracted — if
it did not, `go get` would resolve to that February commit and fail on the
module path anyway.

The consequence, stated plainly: **Hermod is not currently consumable as a Go
module.** `go get github.com/gsoultan/hermod` and
`go install github.com/gsoultan/hermod/cmd/hermod@…` do not work at `1.0.0`, and
will not until the line passes `v1.7.4`. The container image, Helm chart,
GitHub release and packaged binaries are unaffected and are the supported ways
to run it. Building from a checkout also works.

This was a deliberate trade: version numbers that read correctly everywhere a
user actually installs Hermod, against a `go get` path that had never worked in
any published version anyway — the module path was a placeholder until
2026-09-02, so no release before this one could be imported either.

### Why the withdrawn versions are retracted

Two tombstones exist: `v1.7.4` and `v1.8.0`. A tombstone is a tag holding a
`retract` directive and nothing else — no code, no image, no chart. Both are
listed in `.github/tombstones`, which is how the release workflow knows to
publish no artifacts for them.

They exist because Go reads retractions from the go.mod of the **highest release
version**, which makes retracting anything require publishing something above
it. `v1.7.4` was cut to retract the withdrawn `1.x` line. `v1.8.0` was cut
because that turned out not to be enough:

- `v1.8.0-rc.1`, an intermediate candidate withdrawn for the `x/crypto` SSH
  defects, sorts *above* `v1.7.4`, so no directive in `v1.7.4` could reach it.
  `go get github.com/gsoultan/hermod` selected it and installed the vulnerable
  build without complaint.
- `v1.0.0-rc.1` sorts *below* `v1.0.0`, since a pre-release precedes its
  release, so the `[v1.0.0, v1.7.4]` range never covered it either. It needs a
  directive naming it, and that directive has to live in the highest release —
  not in `v1.0.0-rc.1` itself, which is a pre-release and therefore not where Go
  looks.

The retraction is now `[v1.0.0, v1.8.0]` plus `v1.0.0-rc.1` by name, carried by
the `v1.8.0` tombstone. Between them they cover every version the proxy can
serve for this module path.

`v1.0.0-rc.1` — this release — is itself retracted, which is unusual and
deliberate. The proxy holds that version string against an older commit from an
earlier attempt at this reset, and re-tagging cannot displace it, so a Go user
asking for it would receive code without the SSH denial-of-service fix above.
Failing loudly is the better outcome. The image, chart and binaries are freshly
built from this commit, carry no such history, and are published normally.

### Breaking

- **The module path is now `github.com/gsoultan/hermod`.** It was
  `github.com/user/hermod`, a placeholder that matched no repository, so
  `go get`, `go install` and importing Hermod as a library all failed with a
  path mismatch regardless of which version you asked for. Nothing could import
  Hermod at any version, so there was no importer for the change to break —
  which is why it happens here rather than being carried forward.
- **Go 1.27 is required to build.** `go.mod` declares `go 1.27.0`.
- **`hermod.TwoPhaseCommit.Prepare` takes a transaction ID:**
  `Prepare(ctx context.Context, txID string) (string, error)`. The coordinator
  now names a transaction and records the name *before* a participant is asked
  to hold it, so a crash between those two steps leaves a name that recovery can
  look for. Previously the participant chose the name and returned it, and a
  crash in that window left a prepared transaction pinned in the database with
  nothing on record pointing at it. In-tree this affects only the PostgreSQL
  sink; any out-of-tree implementation needs the new parameter.
- **Pebble is refused as a metadata store.** `--db-type=pebble` now exits with an
  explanation instead of starting. It never satisfied the metadata store's
  requirements, and previously reported itself as configured while failing to be
  one.

### Changed behaviour you will notice in production

These are corrections, but each one changes something an operator can see. None
of them need action; all of them are worth reading before you deploy.

- **A failed dead-letter park no longer counts as a delivery.** When a message
  could not be delivered *and* could not be written to the DLQ, the engine
  acknowledged it anyway and the record was gone. It is now retained. On a
  deployment with a misconfigured or unreachable DLQ this appears as replication
  slot or queue growth that was not there before — that growth is the data the
  previous behaviour was discarding.
- **Resume cursors advance on acknowledgement, not on read**, across thirteen
  database and OData sources (including SQLite, Oracle, MongoDB, Dynamics 365 and
  SAP). A crash between reading a batch and the sinks writing it no longer skips
  those rows on restart. The trade is at-least-once behaviour where the previous
  code was accidentally at-most-once: a restart may now redeliver a batch that
  was already written. Sinks with upsert semantics absorb this; append-only sinks
  may see duplicates.
- **Outbound HTTP requests time out.** Requests that previously used a client
  with no timeout are now bounded, and WebAssembly modules are fetched through
  the client that carries the SSRF guard. A remote host that accepts a connection
  and never answers now fails the request instead of holding a worker forever.
- **Per-message delivery logging moved from `Info` to `Debug`.** Steady-state log
  volume drops sharply. Raise the level if you were counting those lines.

### Hardening

- The HTTP API server has read-header, idle and header-size limits; it had none,
  and was answerable to a slowloris client.
- The gRPC server has a concurrent-stream ceiling, a receive-size limit, an idle
  timeout and a keepalive enforcement policy; it had none.
- Oracle and Snowflake identifiers are quoted the way those dialects fold case
  (upper), rather than the way PostgreSQL does (lower).
- `scripts/security-check.sh` fails when a `-run` pattern matches no tests, so a
  renamed test can no longer turn a security claim into a green tick that checks
  nothing.
- CI runs the browser security specs, the race detector within the runner's
  memory budget, and `govulncheck` in a job with the swap it needs.

### Added

- MQTT source, tested against a real broker and promoted to GA.
- Live-server integration tests for the Oracle sink and source, and the MSSQL
  sink against Azure SQL Edge.
- The UI moved to Tailwind v4; a pasted URL can configure a database connector,
  and transformation nodes explain themselves in the form.

### Known gaps

Stated here rather than discovered later. All three are also in `README.md` or
`SECURITY.md`.

- **Snowflake is the one identifier fix never watched failing against a real
  server.** No warehouse is reachable from CI, so the Snowflake half of the
  case-folding fix is inference from documented behaviour rather than
  observation. See `SECURITY.md`.
- **The MSSQL source lacks coverage, not capability.** It reads `CHANGETABLE`
  and emits updates and deletes, but no live SQL Server runs in CI.
- **Five social connectors still advance their cursor on read** — Twitter/X,
  LinkedIn, Facebook, Instagram and TikTok. Each drives a vendor pagination token
  whose semantics differ per API, and no test here can exercise them, so they
  were left alone rather than changed mechanically. Treat a restart as
  potentially lossy for these. They are Experimental in `README.md`.

[1.1.0]: https://github.com/gsoultan/hermod/releases/tag/v1.1.0
[1.0.0]: https://github.com/gsoultan/hermod/releases/tag/v1.0.0
[1.0.0-rc.2]: https://github.com/gsoultan/hermod/releases/tag/v1.0.0-rc.2
[1.0.0-rc.1]: https://github.com/gsoultan/hermod/releases/tag/v1.0.0-rc.1
