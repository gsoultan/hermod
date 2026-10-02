# Secrets per vhost — implementation plan

Spec: `docs/superpowers/specs/2026-10-02-vhost-secrets-design.md` (approved 2026-10-02).
Every step is test-first: the test is written and seen failing before the code.

## Open points, resolved

| Point | Finding | Consequence |
| :--- | :--- | :--- |
| Where the engine learns the vhost | The engine has none. It reads its source in one loop (`pkg/engine/runner.go`) and the registry runs every transformer at one call site (`internal/engine/registry/registry.go`, `t.Transform`) | `Engine.SetVHost`, set by the registry when it builds the engine; the runner marks each message it reads; the registry copies the mark onto a node's output messages |
| Master-key re-encryption | `ReEncryptSecrets` walks a fixed list, `secretTables` (`internal/storage/sql/reencrypt.go`) | The new table is re-encrypted explicitly in the same transaction |
| Vhost rename | `UpdateVHost` rewrites only the vhost row; sources, sinks and workflows keep the old name | Secrets are keyed by vhost name and follow the same rule. Not widened here |
| New collections in MongoDB / Pebble | No migrations: collections and key prefixes appear on first write | Nothing to migrate; SQL adds a `CREATE TABLE IF NOT EXISTS` and a schema-version bump |
| Remote workers (found while planning) | A worker process reaches storage over HTTP as a `worker:` principal and already receives decrypted connector configs | One worker-only route returns a secret's value; no user role can call it |

## Steps

1. **Message scope.** `DefaultMessage` gains an internal `vhost` field with
   `SetVHost` / `VHost`, copied by `Clone`, cleared when the message returns to
   the pool, absent from `Data`, `Metadata`, `ToMap` and JSON.
   Tests: set/get, clone, pool reuse, not serialised.
2. **Scoped lookup in `pkg/security/secrets`.** `VHostStore` (read one value)
   and `VHostManager`: the vhost's store, then the global manager.
   `CachedManager.GetScoped` keys its cache by vhost and name and gains
   `Invalidate`. Tests: order, fallback, isolation between vhosts, cache key,
   invalidation.
3. **Evaluator.** `secret()` / `env()` reached through `ParseAndEvaluate` pass
   the message's vhost to the source when the source is scoped. A message with
   no mark, and `CallFunction` used directly, resolve globally.
   Tests: expression, template and condition spellings, per vhost.
4. **Storage.** `storage.VHostSecret` and a separate `storage.VHostSecretStore`
   interface (list, get, put, delete, delete-all-for-vhost), implemented by the
   SQL, MongoDB and Pebble stores and the test mock. Values are encrypted with
   `pkg/security/crypto` in the store. `DeleteVHost` removes the vhost's
   secrets. SQL: table, queries, schema version, re-encryption.
   Tests per backend that can run locally; stored bytes are not the plaintext.
5. **Registry and engine.** The registry builds the `VHostManager` over its
   storage and global manager, publishes it to the evaluator, resolves
   connector `secret:` values with the connector's vhost, sets the engine's
   vhost, propagates the mark across a node, and marks preview and simulation
   messages. Reachability tests: isolation, fallback, rotation without restart,
   a payload claiming another vhost.
6. **API.** `GET/PUT/DELETE /api/vhosts/{vhost}/secrets[/{name}]` for
   Administrators and Editors with the vhost; a worker-only value route; audit
   log; `routes.golden`. Abuse-case tests, and no value in any response.
7. **Worker adapter.** `apiStorage` reads a value through the worker route.
8. **UI.** Secrets page and navigation entry, API hooks, add / rotate / delete,
   states; a secret-name picker in `TemplateField`. Vitest, one Playwright spec
   added to CI's list.
9. **Docs.** `SECURITY.md`, `CHANGELOG.md`, Security tab copy, Serena memory.
10. **Gates and PR.** The full gate list from `CLAUDE.md`, a live check on a
    spare instance, then one PR against `main`.
