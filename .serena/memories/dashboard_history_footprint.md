# dashboard_history — the only append-only table, and what it costs

`dashboard_history` is the one table in Hermod's metadata database that grows
with nobody doing anything: the registry's sampler writes a row every
`defaultDashboardSampleInterval` (5s) for the global series plus one per watched
vhost, forever. Every other table grows only when a user creates something.

That makes ordinary schema slop expensive in a way it is not elsewhere, and the
numbers are measured, not estimated — a throwaway test wrote a real week of
samples through the real driver into a file-backed SQLite database and stat'd
the file.

## What was removed, and what it was worth

| | MB / week / series |
|---|---|
| as first written | 21.00 |
| without the UUID primary key | 15.71 |
| plus second-precision timestamps | **14.08** |

- **No surrogate primary key.** Rows are never addressed individually: every
  read is a range scan over `(vhost, timestamp)`, every delete a range sweep.
  The UUID was 46% of the table. On MongoDB the same fix is *not* setting `_id`
  — the driver's 12-byte ascending ObjectId beats a 36-byte random hex string
  in both document size and `_id` index locality.
- **Timestamps truncated to the second**, in the storage layer so every writer
  gets it. A 5-second sample never had microsecond precision, and the SQLite
  driver encodes `time.Time` as *text* — 36 bytes, not 8 — so those digits cost
  bytes in the row and again in the covering index. A hand-written schema
  estimate missed this by 20%; only the real driver shows it.
- **No natural `(vhost, timestamp)` key either.** Truncation plus no leader
  election means duplicates are possible, and a natural key would turn one into
  a failed insert. The cost of having no key: PostgreSQL logical replication
  cannot publish DELETEs without a replica identity. Documented at the schema.

## The knobs

`HERMOD_DASHBOARD_SAMPLE_INTERVAL` (default `5s`) and
`HERMOD_DASHBOARD_HISTORY_RETENTION` (default `168h`). They multiply. Retention
`0` disables recording and lets the hourly sweep clear what is there. Malformed
values fall back to the default, never to zero.

## ErrNotSupported is a storage decision

Pebble and the worker's `api_storage_adapter` cannot store history. Returning a
bare error meant 17,280 logged failures a day; returning `nil` meant the sampler
handed samples to a sink that dropped them forever. Both now wrap
`hermod.ErrNotSupported`, and `Registry.historyUnsupported` latches on it so the
sampler asks exactly once. **The backend that stores no history was the one
writing the most to disk.**

## Still open

- No leader election exists anywhere in the repo. Two control-plane replicas
  against one metadata database each write their own global row every 5s, so
  history multiplies by replica count and the series interleaves per-node
  point-in-time readings of `Throughput`.
- Retention keeps 5-second resolution for the full window, though no chart can
  plot 120k points. Tiered roll-up would cut it another 10-20x.

Related: [reachability_tests](reachability_tests.md),
[sqlutil_owns_dialect_differences](sqlutil_owns_dialect_differences.md).
