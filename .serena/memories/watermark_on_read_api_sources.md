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

### The two exceptions

- `googleanalytics` — **false positive**. `lastFetch` records when the source
  last polled, not what it consumed: it gates the poll interval and nothing
  filters the query by it.
- `file` — **the same bug, still open**. `pop()` advances `lastMTime` when a
  file is dequeued, before any of its rows are delivered, and one file can
  yield thousands of rows in CSV per-row mode. Needs the reader to signal
  end-of-file across all four backends, so it is not mechanical like the rest.

Both are on an exemption list next to the check, and `TestNoExemptionIsStale`
fails if one stops applying.

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
