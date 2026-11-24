# DB Lookup query-plan & index check (`internal/advisor/queryplan`)

Tuning tab → "Lookup queries and indexes" → `POST /api/workflows/{id}/insights/lookup-plans`
(EditorOnly, audit `LOOKUP_PLAN_CHECK`). Works on the stored workflow, running or not.

## Pieces
- `pkg/comm/transformer/lookup/statement.go`: `LookupStatement(config, driver)` builds the SQL a node
  sends through the node's own builders (query mode: `ParameterizeTemplateWith`; table mode:
  `tableLookupQuery`, split out of `lookupSQL`). `LookupTarget` resolves source + dialect and refuses
  CDC sources exactly like the node.
- `plan.go`: `ParsePlan` reads EXPLAIN (VERBOSE, FORMAT JSON). VERBOSE is needed: schema names and
  alias-qualified columns. Join conds (Hash/Merge/Join Filter) are attached to the scans whose alias
  they reference; a merge join's Seq Scan has no Filter of its own.
- `assess.go`: pure rules on Scan + catalog. Only Seq Scans on tables >= 10k rows (reltuples; -1 =
  never analyzed = unknown, not small) produce proposals. Rules: wrapped_column (expression index,
  alias stripped, `TRIM(BOTH FROM lower(col))` as PG deparses btrim), leading_wildcard (pg_trgm GIN),
  jsonb_index (GIN, jsonb_path_ops for @>), wrong_index_type (range on hash-only → btree),
  missing_index (composite btree, eq first, range last), index_not_chosen, index_used, small_table.
- `pg.go` `Run`: BEGIN READ ONLY; SET LOCAL statement_timeout 5s + plan_cache_mode=force_generic_plan;
  SQL `PREPARE <random> AS q` via **ExecParams** (extended protocol ⇒ server refuses 2 statements);
  `EXPLAIN (VERBOSE, FORMAT JSON) EXECUTE name(NULL,...)`; DEALLOCATE (outlives rollback); ROLLBACK.
- `lookups.go` `CheckLookups`: all db_lookup nodes, 4 at a time, 15s each; Postgres (`pgx`) only.

## Things measured, do not relearn
- EXPLAIN (GENERIC_PLAN) with bound NULL params gives a bogus "One-Time Filter: false" plan.
- pgx simple-protocol mode interpolates `$1` client-side ("insufficient arguments"); a no-arg
  `db.ExecContext` runs multi-statement text, so it must not be used for the PREPARE.
- Protocol-level Prepare + SQL EXECUTE fails behind PgBouncer >=1.21 with max_prepared_statements>0
  (it renames statements: 26000). SQL-level PREPARE passes through. Tested: direct PG16, PgBouncer
  1.22 transaction mode with tracking 0 and 100.
- Every proposed index in the tests was created on PG16 and confirmed used by the planner.

## Tests
Unit fixtures are real PG16 plans in `internal/advisor/queryplan/testdata/`. Integration:
`POSTGRES_DSN=... go test -tags integration ./internal/advisor/queryplan/` (through PgBouncer add
`default_query_exec_mode=exec`). UI: `ui/src/__tests__/tuningLookupPlans.test.tsx`.
