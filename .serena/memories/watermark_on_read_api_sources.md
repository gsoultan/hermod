# The watermark-on-read bug is still live in nine API sources

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

## The fix, and why it was not applied here

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
