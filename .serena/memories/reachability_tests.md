### Reachability tests — cover the assembly, not just the parts

**Rule.** A feature configured through storage needs one test that starts from storage:
reads the configuration the way the running system reads it, builds what the running
system builds, asserts the observable outcome. Recorded in AGENTS.md under Test-Driven
Development, and in its Definition of Done.

**Why it exists.** Three bugs in one month had the same shape — full coverage of both
sides of a seam, none of the join:

| Feature | Unit | Integration | Reality |
| --- | --- | --- | --- |
| Transactional sink group (`txgroup`) | passed | passed | could not start at all |
| Session revocation | passed | n/a | issued tokens had no `jti`; logout revoked nothing |
| Worker lease failover | passed | passed | a cancelled context stranded the registry entry, wedging the workflow permanently |

The txgroup case is the sharpest. `factory.CreateSink` wraps every sink in tracing and
retry decorators, and those forward `Write`/`WriteBatch` but **not** `Begin`, `Commit`,
`Prepare` or `CommitPrepared`. A decorated sink therefore fails the `hermod.TwoPhaseCommit`
assertion and the group rejects every member. The unit tests covered the refusals; the
integration tests built sinks by calling `NewPostgresSink` directly. Nothing built one the
way a workflow does. Fixed by `factory.CreateSinkForTransactionGroup` (undecorated) —
forwarding through the decorators would be wrong, because retrying a `Write` inside a
prepared transaction can apply it twice.

**Worked example.** `internal/engine/registry/txgroup_reachability_integration_test.go`:
stored sink config → registry → factory → group → two real PostgreSQL databases. Needs
`HERMOD_INTEGRATION=1`, `POSTGRES_DSN`, and `max_prepared_transactions > 0` (CI sets all
three; the local `postgres-dev` container has 32).

**Validate the test itself.** Break the wiring on purpose and watch it fail. A regression
test that does not catch its own regression is decoration — this one was confirmed by
reverting `resolveAndCreateTxGroupMember` to the decorated path.

**Two traps met while writing it, both of which produced false signals:**

- The in-memory `fakeStateStore` returned an *error* for a missing key. Every real store
  (Redis, etcd, SQLite) returns `(nil, nil)`. A fake stricter than the real thing made a
  first-ever group start look like a corrupt transaction log.
- Pointing a sink at a non-existent table is not a failure — the Postgres sink creates it.
  To force a member failure, pre-create the table with a `CHECK` constraint the write must
  violate.

See also [Connector conformance suite](connector_conformance_suite.md) for the contract
each connector must pass in isolation; reachability is the complement — the wiring
between them.
