# A cancelled health check is a stop, not an outage

**Symptom that led here.** `panmail sink: cannot reach the gateway at <host>: dial tcp:
lookup <host>: operation was canceled`. Go's resolver says "operation was canceled" only
for `context.Canceled`; a deadline reads "i/o timeout". So the gateway and DNS were fine:
the caller's context had been cancelled mid-dial.

**Where the cancel came from.** `Runner.checkHealth` (`pkg/engine/runner.go`) pings the
source and every sink on `r.ctx` each `StatusInterval` (default 1s). Stopping or restarting
a workflow cancels `r.ctx`, so a pass in flight saw every ping fail, logged
"Background sink health check failed" at error level and set the workflow
`reconnecting:sink:<id>` on every stop.

**Fix.**
- `checkHealth` returns without reporting anything when a ping fails and `r.ctx` is done.
- panmail `dial` reports a `context.Canceled` as "the check of the gateway ... was stopped
  before it finished" (wrapping `context.Canceled`). A deadline still says
  "cannot reach the gateway" — no answer in time *is* unreachable.

Tests: `pkg/engine/health_cancel_test.go`,
`TestPing_ACancelledCheckIsNotReportedAsAnUnreachableGateway` in the panmail tests.

**Rule for new connectors.** A `Ping` error message must not blame the remote when
`ctx.Err()` is `context.Canceled`; check the context before wording the error.
