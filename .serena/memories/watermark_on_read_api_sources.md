# Watermark-on-read in the polling sources: fixed, with two exceptions

[`hermod-watermark-on-read-class`] records that ten SQL/CDC sources advanced
their *persisted* cursor on read rather than on Ack, and were fixed. The
social and HTTP polling sources were not part of that sweep.

Found 2026-09-20 by auditing `Ack` implementations. All nine of

  discord  facebook  firebase  http  instagram  linkedin  slack  tiktok  twitter

have an `Ack` that is `return nil` and a `GetState()` that persists a cursor
the **`Read` path** advanced. Worked example, `pkg/comm/source/slack/slack.go`:

- `Read` line ~119: `s.lastTimestamp = s.items[len(s.items)-1]["ts"].(string)`
  — the cursor jumps to the newest item of the *fetched page*
- `Ack` line ~151: `return nil`
- `GetState()` returns `{"last_timestamp": s.lastTimestamp}`, which the
  engine's checkpoint persists

So a page of 100 items moves the cursor past all 100 the moment it is fetched.
Crash after delivering the first and the other 99 are gone: the cursor says
they were consumed, and nothing will ever fetch that window again. At-least-once
is not delivered for these connectors — it is at-most-once, silently.

## Fixed 2026-09-20

Ten of them: **slack, discord, twitter, instagram, linkedin, tiktok, facebook,
firebase, googlesheets, mainframe**. `pkg/infra/ackwatermark` is the shared
mechanism.

Two design points that are easy to get wrong:

- It advances across the acknowledged **prefix**, not to the highest
  acknowledged value. Acks arrive out of order when sinks run in parallel, and
  the highest would step over an item still in flight.
- An item may carry **no cursor**. TikTok's cursor and Facebook's `since`
  address a *page*, so every item but the last carries nothing and the token is
  stored only once the whole page is acked. Passing the token on every item
  would let it be stored after the page's first ack — the same bug one level up.

**The count was wrong, twice.** A grep for `s.last*` assignments in `Read`
found nine, of which `http` turned out to have no state at all. The AST-based
contract test (`ack_watermark_contract_test.go`, "GetState plus a no-op Ack")
found **four more** the grep's field-name guessing missed: `file`,
`googleanalytics`, `googlesheets`, `mainframe`. Ask the AST, not a field name.

### The one exception

- `googleanalytics` — **false positive**. `lastFetch` records when the source
  last polled, not what it consumed: it gates the poll interval and nothing
  filters the query by it.

It is on an exemption list next to the check, and `TestNoExemptionIsStale`
fails if it stops applying.

### `file`, the one that needed a different shape

Fixed after the others. The claim that it "needs end-of-file signalling across
all four backends" was **wrong**: end-of-file is already signalled in two
backend-independent places, where the reader returns `nil`. Backends only list
files and fetch bytes; row iteration sits above them.

The real problem was *what unit gets acknowledged*. A file yields many rows and
a streaming reader cannot know a row is the last until it asks for one more, so
there is no row to hang the file's watermark on. The unit is the **file**:
complete once read to the end *and* every row it produced is acknowledged.

Two further defects fell out of it:

- **A message ID cannot identify a row across files.** With `key_field` set —
  the normal parquet config — a row's ID is the key column's value, so two
  exports of the same table hand out the same IDs; a `map[id]file` reassigns
  the older file's rows to the newer one and the watermark freezes for good.
  Rows carry their file in the `_hermod_file_ack` metadata key instead.
- **A timestamp is not a position.** `ModTime.After(lastMTime)` skipped every
  file sharing the acknowledged file's second. The watermark is now a
  `(modification time, name)` pair and the queue is sorted by the same total
  order. A bare Unix second from older state orders before everything in that
  second, so it redelivers rather than skips.

The contract gate itself was per-package, which read `CSVSource`'s correctly
trivial `Ack` as `GenericFileSource`'s bug. It is now per receiver type.

## The fix, and why it was not applied everywhere

Advance the persisted cursor only to the newest **acked** message, which is what
the ten SQL sources now do. Each connector needs to track what is outstanding,
so it is nine separate changes, each with its own test.

They are testable without credentials: the sources take a base URL
(`SlackSource.SetBaseURL`, and the equivalent on the others), so a fake server
is the seam. That is the same shape as
[`firebase_sdk_has_a_testable_send_seam`](fcm_sink.md).

Not attempted in the session that found it: nine connectors changing delivery
semantics is its own piece of work, and the finding is worth more than a
half-finished sweep.
