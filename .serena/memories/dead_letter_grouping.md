# Dead letters are grouped per record, with a failure count

A source that re-sends what it never got a result for — a mailbox polled every
few seconds, a task queue, a table read without a watermark — fails the same
record every cycle while the cause stands. The export that prompted this held
500 rows for **one** applicant: two payloads, ×263 and ×237, 17:41→19:22 UTC,
all `panmail: invalid credentials`. Five hundred rows, two records, and no way
to tell on the page which two to replay. So a dead letter is a *record*, not an
attempt.

## The group key

`storage.DeadLetterGroupKey{WorkflowID, NodeID, SinkID, RecordKey}`
(`internal/storage/dead_letter_groups.go`). `RecordKey` is
`"payload:"+sha256(op\0schema\0table\0canonicalJSON)` — canonical JSON, so key
order does not matter — or `"fields:"+sha256(path=value…)` when the workflow
names `DeadLetterKeyFields` (dot paths into the payload; the editor's "Record
key fields" under Reliability Policy). Name fields when the payload carries
something that changes on every read (timestamp, signed URL), otherwise every
re-send is a new record and nothing folds. Changing the fields is a runtime
config change (`workflowRuntimeConfigDiffers`, `internal/engine/worker/sync.go`)
and restarts the engine, same rule as every other setting there.

## How a repeat folds

`storage.KeepDeadLetter` is the only write path (`deadLetterSink.Write` calls
it). It looks for a **queued** row with the same key (`FindDeadLetterGroup`;
only `queued` — a replayed, discarded or delivered record that fails again
starts a new row, which is the right answer: it is a new incident). Found, it
bumps `FailureCount`, moves `FailedAt`/`Error`/payload to the latest attempt,
keeps `FirstFailedAt`, and folds the history: identical consecutive failures
are one `failed_again` event with `Count` and `Until`. `UpdateDeadLetter(d,
prevCount)` is optimistic — `WHERE … failure_count = prevCount`; a lost race is
`ErrDeadLetterConflict` and `KeepDeadLetter` re-reads up to 5 times, then
inserts its own row rather than drop the message. A dropped message is the one
outcome the dead-letter store exists to prevent, so a fold that cannot be made
safely degrades to a duplicate row, never to a loss.

Legacy rows (no `record_key`) are folded by `GroupUngroupedDeadLetters`, which
`purgeDeadLetters` runs before the hourly purge: oldest row of a key becomes
the group, the copies are marked `merged`. `merged` rows are hidden from an
unfiltered list but kept, so nothing an operator saw disappears. The SQL
columns are `record_key`, `failure_count` (DEFAULT 1), `first_failed_at`, index
`idx_dead_letters_group(workflow_id, record_key, status)`;
`currentSchemaVersion` stayed at 2 because the columns are additive with
defaults and `knownSchemaFingerprint` carries the reason. Mongo stores the same
three fields; `prevCount <= 1` matches a missing `failure_count`. Stats gain
`queued_attempts` (sum of counts) next to `queued` (records).

## "Delivered later"

`pkg/engine` has a `DeliveredObserver` (`SetDeliveredObserver`,
`writer.go`), called from the runner with the sink targets after a successful
write, skipping messages that carry `_hermod_failed_at` (those *are* the park
route, see [ack_is_not_always_a_success](ack_is_not_always_a_success.md)). The
registry keeps an in-memory `openDeadLetters` set per workflow (loaded from the
store on start, 500×20 pages, added to on every park) and, when a delivered
message's key is in it, settles the dead letter `delivered`. The set is the
reason the observer costs nothing on the hot path when nothing is waiting:
one map lookup, no store call. Removing the `_hermod_failed_at` guard or the
`SetDeliveredObserver` wiring both fail tests (mutation-checked).

## Replay

The page replays once per record, never per attempt. `ReplayConfirmModal`
says "sends N messages, covers M failed attempts" and POSTs
`/api/dead-letters/{id}/check` once per distinct `sink_id`, which runs
`Registry.TestSink` on the sink the record failed at with a 15s timeout, so an
operator sees "Panmail does not answer: connection refused" before sending 50
records into the same refusal. The server caps are unchanged: 50 per replay,
500 per export (the page warns when the filter matches more; the export carries
`X-Dead-Letters-Total`).
