# End-to-end scenarios

The Playwright coverage suite in `ui/__tests__/coverage/` drives the real UI against a real Hermod
(the SQLite dev stack) and checks what the user sees and what the engine does. This page lists every
scenario it runs, what each one proves, and what it does not cover.

## Running it

```bash
./scripts/dev.sh --sqlite --reset --detach      # API on :4005, UI on :5175, admin/admin
bunx playwright test ui/__tests__/coverage/     # the whole suite, about an hour on one worker
bunx playwright test ui/__tests__/coverage/conditions_e2e.spec.ts   # one scenario group
./scripts/dev.sh --stop
```

CI runs it as the `e2e-coverage` job, split into three shards (`--shard=N/3`), each with its own
Hermod. The job starts Hermod with `HERMOD_ENV_E2E_REGION` set and tells the suite the value in
`HERMOD_E2E_ENV_VALUE`, so the `env()` fallback (S4) runs there. Locally S4 is skipped unless both
are set.

## Where the lists come from

Nothing is listed by hand. `catalog.ts` reads them from the app's own source on every run, and fails
if a list comes back shorter than a floor, so a parser that silently lost a category cannot leave
the suite green:

| List | Read from | Size today |
| :--- | :--- | ---: |
| Palette steps | `ui/src/pages/workflows/WorkflowEditor/nodeCategories.ts` | 172 (45 sources, 52 sinks, 75 transformations and logic nodes) |
| Source types | `SOURCE_TYPES` in `ui/src/components/forms/SourceForm.tsx` | 44 |
| Sink types | `SINK_TYPES` in `ui/src/components/forms/SinkForm.tsx` | 53 |
| Expression functions and their answers | `ui/src/lib/expressionFunctions.json` (held to the engine by `pkg/infra/evaluator/function_catalog_test.go`) | 75 (70 with a fixed answer) |
| Pages | `ui/src/router.tsx` | 46 routes |
| Required connection fields | `ui/src/lib/connectorRequirements.ts`, the module the wizard gates on | per type |

A new connector, function, palette step or page is tested by the next run without touching the
suite.

## Every test runs under an error watch

`watchErrors` (in `helpers.ts`) fails a test on an uncaught exception, a console error, a 5xx from
`/api/`, or the router's error page, even when the test's own assertions pass. The few console lines
that are not defects (a deliberate 4xx resource load, a stream torn down by navigation) are listed in
`BENIGN_CONSOLE` with the reason for each.

## Scenarios

### P: pages (`pages_e2e.spec.ts`)

| ID | Scenario |
| :--- | :--- |
| P1 | Every route in the router opens, logged in as an administrator, with real fixtures for `$id` routes (a source, sink, workflow, vhost, user and worker), and every visible tab on it can be opened. |
| P2 | A guard fails if a route has no entry in the page map, or an entry names a route that no longer exists. |
| P3 | `/login` and `/forgot-password` render logged out; a logged-out visitor to any page is sent to `/login`. |

### N: palette (`palette_e2e.spec.ts`)

| ID | Scenario |
| :--- | :--- |
| N1 | Each of the 172 palette steps can be added to a new flow (after a Webhook source, for non-sources) and the node appears on the canvas. |
| N2 | Its settings drawer opens with a field to fill in (a Note is edited in place on the canvas instead). |
| N3 | For transformations with a Live Preview, Available Fields is shown and Run Preview against the sample does not fail on the server. |

### C: sources and sinks (`connectors_e2e.spec.ts`)

Every type in `SOURCE_TYPES` and `SINK_TYPES`, through `/sources/new` and `/sinks/new`:

| ID | Scenario |
| :--- | :--- |
| C1 | Name, virtual host and type are chosen on Basics; Next opens the connection step with that type's own fields, or a "Nothing to configure" notice for a type with none. |
| C2 | While a required field is blank, Next is disabled. Every field the wizard requires has an input under the same name the "Required:" tooltip uses, and filling them enables Next. |
| C3 | Test Connection answers with a visible result. Pointed at a closed port on 127.0.0.1, that is a refusal (4xx) with a message, never a server error or a crash. |
| C4 | The rest of the wizard is walked to the end and the connector saved; it is listed, its edit page opens with the saved name, and it is deleted. |

### F: expression functions (`functions_e2e.spec.ts`)

| ID | Scenario |
| :--- | :--- |
| F1 | Each of the 70 functions with a fixed answer gives that answer in Set Fields (bare and as a `{{ }}` token), makes an If condition true when compared with its answer and false otherwise, picks the matching Switch case, and keeps the record in a Filter. |
| F2 | The 5 functions without a fixed answer (`now`, `timestamp`, `uuid`, `secret`, `hmac_sha256`) give a value; the two that read a secret get one first. |
| F3 | An If on a function's result, across every operator: `array_len(source.tags)` with `>`, `>=`, `<`, `<=`, `=`, `!=`, plus `len`, `round`, `abs`, `contains`, `regex`, `trim`, a function in the value, and a missing list. |
| F4 | The editor's function picker offers every function in the catalog. |

### K: conditions on a function result (`conditions_e2e.spec.ts`)

The case reported from the If node: `array_len(source.users) > 0` "always false" with one user.

| ID | Scenario |
| :--- | :--- |
| K1 | In the If drawer the condition is built as a user builds it (type `users`, Insert function, `array_len`, operator `>`, value `0`); Live Preview shows "Branch taken: true", then false for `> 5`. |
| K2 | The two ways it really is always false are named beside the field: a bare field inside a call (`array_len(users)`) and a function that does not exist (`array_length`, with "did you mean array_len"). The correct call shows no warning. |
| K3 | A Switch on the same expression routes to the case that holds, and its field opens as a structured call with a field argument. |
| K4 | The Test simulation takes the true path on the stored sample. |
| K5 | A running workflow sends a record with users to the true sink and one without to the false sink. |

### S: secrets and environment values (`secrets_env_e2e.spec.ts`)

| ID | Scenario |
| :--- | :--- |
| S1 | A secret is added, rotated and deleted on the Secrets page; its value is never shown again, on the page or by the API. |
| S2 | `secret("N")`, `{{secret.N}}`, a token inside text, `env("N")`, a fallback for a missing name, `hmac_sha256` keyed by a secret, and a secret in an If value all give the stored value in Live Preview. |
| S3 | A running workflow reads a secret and delivers it to a live endpoint. |
| S4 | `env()` falls back to the server's `HERMOD_ENV_<NAME>` variable (CI only, see above). |

### W: workflow features (`workflow_features_e2e.spec.ts`)

| ID | Scenario |
| :--- | :--- |
| W1 | Two steps are connected by dragging between their handles, the flow is renamed and saved; the stored flow has the edge and the name. |
| W2 | Each save is a version; Version history lists them and Restore brings an earlier one back. |
| W3 | Turn on from the editor, a record flows to a live endpoint with the transformation applied, Turn off. |
| W4 | Clone makes an independent, inactive copy; Delete flow removes it and leaves the original. |

Detail tabs, the simulation overlay, export and import, and list-level toggle and delete are
covered by `workflow_surface_e2e`, `simulation_path_e2e` and `workflow_export_import_e2e`, which the
`e2e` job runs.

### L: live connectors (`live_e2e.spec.ts`)

| ID | Scenario |
| :--- | :--- |
| L1 | Form source to HTTP sink. |
| L2 | Cron source (`@every 2s`) to HTTP sink: the payload arrives. |
| L3 | HTTP polling source to HTTP sink: what the polled URL serves arrives. |
| L4 | Webhook to File sink: the record is in the file. |
| L5 | Webhook to SQLite sink: the row is in the table. |
| L6 | Webhook to Stdout sink: the record is in the worker's output (`.dev/logs/backend.log`). |
| L7 | Webhook to a Filter whose conditions are stored as a list (the API's and an import's shape) to HTTP sink: only the matching record arrives. |

The HTTP "sink" is a server the test starts on 127.0.0.1 (`Receiver` in `helpers.ts`), which records
what arrives and can serve a body for a polling source to read.

## What is not covered live, and why

Every source and sink form is covered by C1 to C4, including the ones below. What is not tested is
whether the connector reaches its external system and moves records through it, because CI has none
of these systems:

- Databases and CDC: PostgreSQL, MySQL, MariaDB, SQL Server, Oracle, MongoDB, Cassandra, ScyllaDB,
  ClickHouse, YugabyteDB, DB2, Snowflake and the rest. The Go integration job (`integration-go`)
  covers the ones it can start.
- Message brokers and streams: Kafka, RabbitMQ, NATS, Redis, MQTT, Pulsar, Kinesis, Pub/Sub and
  similar.
- Object storage and files on remote hosts: S3, GCS, Azure Blob, FTP and SFTP.
- SaaS and messaging APIs: Salesforce, HubSpot, Stripe, Slack, Telegram, social networks, email
  providers, FCM and similar, which need accounts and credentials.
- Metis, SAP and other enterprise systems.

To add one, start the system in CI (as `integration-go` does for Postgres) and write an L scenario
that delivers through it and reads the result back.
