### Connector conformance suite — the contract every source and sink must pass

**Where:** `pkg/comm/conformance`. Adding a connector is one line in
`connectors_test.go`. Currently 67 of 86 connectors, 309 assertions, ~45s.

**Why it exists.** The connector layer is what users touch and was the least
tested — 102 of 193 packages had no test files, almost all connectors. Per-package
test files were never going to fix that: the interesting properties are identical
for all 86 and nobody writes the same six tests eighty-six times.

**What it asserts** (no live infrastructure): `Close` is idempotent, nil messages
do not panic, operations after `Close` do not panic, and — the one that matters —
`Read`, `Ping`, `Write` and `Ack` return within their **context deadline** rather
than falling back to a driver default. Passing means "not obviously broken before
it reaches the network", not "verified"; data-path correctness stays in the
integration tests.

**Defects it found**, all fixed rather than parked:

| Connector | Defect |
|---|---|
| cassandra, scylladb | `Read` ignored cancellation — `gocql.CreateSession` has no context-aware variant |
| kafka sink | leaked goroutines via the shared `DefaultTransport`, outliving `Close` |
| kafka source | `Ack(ctx, nil)` stopped returning — wedges the consumer on the hot path |
| servicenow, elasticsearch, dynamics365, websocket, sap sinks | nil message neither returned nor panicked |
| mongodb source | connect discarded the caller's context for a hardcoded 10s |
| pulsar sink | no connect or operation timeout at all |

**KnownGaps is a ratchet**, modelled on `.golangci.yml`: a listed gap that still
fails is skipped with its reason logged; an **unlisted** failure is hard; and a
**listed gap that now passes is also hard**, so a fixed entry cannot sit there
pretending to be broken. The list only shrinks. It is currently empty.

**Two traps worth remembering.**

1. **Never register a connector that hardcodes a vendor hostname.** pinecone,
   and the social connectors, build URLs like `controller.<env>.pinecone.io` with
   no injectable base URL. Registering them dials the real internet — slow, flaky,
   and one connector doing live DNS and TLS degrades *every other connector's*
   dial in the same run (5:38 vs 45s). Give them a base-URL override and they can
   join. They are excluded with the reason in place.
2. **Endpoints are loopback on a closed port**, not an unroutable address.
   TEST-NET-1 models a black-holed network better, but each dial becomes a full
   TCP SYN retry chain and the kernel serialises them into a five-minute run. A
   suite that slow does not get run.

**Also excluded:** `batchsql`, `form`, `grpc` — each needs a collaborator (a
`DBProvider`, a submission `Storage`, a `.proto` on disk) that the suite cannot
supply without testing the fake instead of the connector.
