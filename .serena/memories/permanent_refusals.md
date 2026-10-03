# Permanent refusals: hermod.ErrPermanent

A sink marks a failure that another attempt cannot fix — the destination
refused *this message*, not "unreachable" — by wrapping `hermod.ErrPermanent`
(or an error type whose `Is` matches it). Three places act on it:

- `sink.RetrySink` (`pkg/comm/sink/decorators.go`, wraps every factory-built
  sink, 3 attempts) returns on the first one: `sink refused the message: %w`.
- The engine's retry loop (`writeToSink`, `pkg/engine/writer.go`) stops calling
  the sink. **With a dead-letter sink** the message is parked at once. **Without
  one** it still waits the sum of the remaining backoffs (`retryDelay`) before
  returning: the message goes back unacknowledged and the source redelivers it,
  and the backoff is what paced that. Returning at once turns one unsendable row
  into a tight loop. `TestAPermanentRefusalWithNoDeadLetterSinkKeepsThePace`
  holds it.
- The circuit breaker (`sinkWriter.run`) does not count it — neither failure nor
  success. A run of stale FCM tokens used to open the breaker and stop delivery
  to every live device. `healthFailure` is the distinction.

The batch path breaks out of its retry loop and falls into the existing
per-message isolation pass, which parks each message.

Before this, `fcm.ErrPermanent` was a value of its own and nothing above the
package could see it: the operator's error read "sink write failed after 3
retries: fcm: permanent failure". `fcm.ErrPermanent` is now a `permanentMark`
whose `Is` matches `hermod.ErrPermanent`, keeping its text.

Only FCM marks errors so far. A sink with a "will never succeed" class (HTTP
4xx other than 408/429, schema violations) can opt in by wrapping the marker.
See [fcm_sink](fcm_sink.md).
