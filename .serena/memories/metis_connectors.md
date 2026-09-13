# The metis connectors, and where they differ from panmail

`pkg/comm/sink/metis` and `pkg/comm/source/metis` reach a [Metis](https://github.com/gsoultan/metis)
BPMN 2.0 workflow engine (the `gobpm` repo, module `github.com/gsoultan/metis`)
through `github.com/gsoultan/metis-sdk` — a stdlib-only client, same shape as
`panmail-sdk`.

## The dependency was a local `replace`, and it reached CI

`go.mod` carried:

```
github.com/gsoultan/metis-sdk v0.0.0-00010101000000-000000000000
replace github.com/gsoultan/metis-sdk => /Users/gsoultan/projects/metis-sdk
```

A filesystem path on one machine. It is invisible to every local gate — `go
build`, `go test -race`, `golangci-lint` and `govulncheck` all pass, because the
path resolves here — and it fails on any other checkout. It took down five of
six CI jobs on PRs #100 and #101 at once, each with the same line:

```
metis-sdk@v0.0.0-00010101000000-000000000000 (replaced by /Users/.../metis-sdk):
  open /Users/.../metis-sdk/go.mod: no such file or directory
```

Resolved: the SDK is public and tagged `v0.1.0` (at `32a9ece`, the same commit
the local path was serving), so the replace is gone and `go.mod` requires
`v0.1.0` with a real `go.sum` entry — a filesystem replace needs none, which is
why there was no metis line in `go.sum` before.

**A local-path `replace` is the one defect class this repo's local gates cannot
see.** If one is added again, `grep -n '=> /' go.mod` is the check; nothing else
will tell you before CI does.

## The sink: a 5xx is an unknown outcome, not a refusal

Starting a process is not idempotent and the engine has no de-duplication key, so
the sink uses the same idempotency claim as panmail. It draws the line in a
different place, and that difference is the whole design:

| Outcome | panmail | metis |
|---|---|---|
| stated refusal | all four SDK error types | 400, 401, 403, 404 only |
| unknown | transport failure | transport failure **and 5xx** |

A 500 is an answer, but not a statement that nothing was written — the engine may
have committed the instance and then faltered on the way to saying so. Treating
it as a refusal releases the claim and lets the retry start somebody's
order-fulfilment process a second time.

`stated()` is the discriminator and `TestWrite_ServerErrorKeepsTheClaim` fails
when it is inverted — verified by mutation, not assumed. A first attempt at that
mutation was a **no-op**: an early `if sdk.IsServerError(err) { return false }`
guard changed nothing, because `IsInvalid`/`IsUnauthorized`/`IsNotFound` never
match a 5xx anyway. The guard was dead code and was removed; the real mutation is
appending `|| sdk.IsServerError(err)`.

**An expired token is the one error retried in-line.** A 401 is a stated refusal,
so logging in again and repeating the call cannot act twice. It is only retried
when the sink holds a username — a static token has no second thing to try, and
`TestWrite_DoesNotRetryA401WithAStaticToken` pins that.

## The source: the cursor is time-plus-ties, and only `Ack` moves it

The engine's listings are newest-first with **no "since" filter**, so the source
keeps the watermark itself. It is not just a timestamp: two rows can share a
`created_at`, and a timestamp-only cursor must either re-deliver the first or
drop the second. `cursor` is `{at time.Time, ids map[string]struct{}}` — the
instant, plus the ids already passed *at* that instant.

`seen` (reading) and `acked` (persisted) are separate, the
[connector-conformance](connector_conformance_suite.md) house pattern. `GetState`
reports only `acked`. This is the
[watermark-on-read class](https://github.com/gsoultan/hermod) fixed in ten other
sources; `TestAck_AdvancesTheCursorAndReadDoesNot` fails if `poll` touches
`acked`.

The watermark rides in **metadata** (`metis_created_at`), not in the data map, so
a stream whose own fields include `created_at` cannot move the cursor by
accident.

**Incidents are an N+1 by API design.** `ListIncidents` takes an *instance* ID,
not a project, so the stream lists instances, keeps the failed ones, and asks
each. That is a request per failed instance per poll, and an instance that fails
after ageing out of `scan_pages` of the listing is never asked. Documented in the
package doc, the README and the UI form rather than hidden.

## The UI gate needed a predicate

The sink's required name field follows its `action` — `definition_key` to start,
`message_name` to correlate, `signal_name` to broadcast. Demanding all three
would disable Next for every configuration that is actually valid, so
`RequiredField` in `ui/src/lib/connectorRequirements.ts` gained an optional
`when?: (config) => boolean`. It is the first connector whose requirements are
not static, and any future mode-switching connector wants the same thing.

Both connectors are registered in `pkg/comm/conformance` (405 assertions, up from
393) and are **Beta** in README.md: unit-tested against an in-process engine
speaking the real wire contract, but no engine is reachable from CI.

Related: [sink_form_fallthrough_and_panmail](sink_form_fallthrough_and_panmail.md),
[connector_conformance_suite](connector_conformance_suite.md).
