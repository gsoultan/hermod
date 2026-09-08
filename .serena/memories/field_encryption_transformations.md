# Field-level encryption transformations

`encrypt` and `decrypt` (`pkg/comm/transformer/security/encrypt.go`) seal named
fields with AES-256-GCM. Registered from `init()`; the engine reaches them via
`transformer.Get(transType)` because `cmd/hermod/main.go` blank-imports the
package.

## Decisions that are load-bearing

**Ciphertext is tagged `enc:v1:`.** Without a marker neither direction can tell
ciphertext from plaintext. Re-running a workflow would double-encrypt a column,
and decrypt could not separate "never encrypted" from "corrupt". With the tag,
encrypt skips sealed values (idempotent re-runs) and decrypt passes untagged
values through — which is also what a column holds midway through a rollout.
The version segment leaves room for a second scheme without stranding old data.

**The key is hashed, never truncated or padded.** `aeadFor` runs SHA-256 over
the configured key. Truncating at 32 bytes means two keys sharing a prefix
encrypt identically, so a rotation silently does nothing; padding a short key
leaves the remaining bytes known. Same reasoning as `pkg/security/crypto`'s
`derive`, and the reason that package keeps `deriveLegacy` around.

**Both fail closed in `Transform`, not `Prepare`.** The engine ignores the error
`Prepare` returns — `registry_workflow.go:1464` calls it as
`if prepared, err := pt.Prepare(step); err == nil`. Validation placed there is
invisible. A missing key or empty field list must error from `Transform`, and a
step asked to encrypt that quietly forwards plaintext is the whole failure.

**No `*` wildcard.** `mask` has one. Masking every field degrades a message;
encrypting every field destroys it — primary keys, operation type and routing
columns included.

**Fresh random nonce per value.** Identical plaintexts encrypt differently, so an
encrypted field cannot be used as a join or lookup key downstream. Deterministic
mode was considered and deliberately left out: it leaks equality.

## Key storage — accepted risk

The key is configured inline on the node, so it is stored with the workflow
definition and readable by anyone who can read or export that workflow. This was
chosen explicitly over env-var or secret-manager sourcing. Note that transformer
configs are *not* run through the secret manager today: `resolveSecrets` at
`internal/engine/registry/registry.go:764` only covers source and sink configs
(`map[string]string`). Wiring `secret:` refs into transformer configs is the
upgrade path if this posture changes. Rotating the key does not re-encrypt data.

## Tests

`pkg/comm/transformer/security/encrypt_test.go` covers round trip, nonce
randomness, idempotence, wrong-key `onError` policies, fail-closed configs,
nested and multi-field paths, and the `Prepare`→`Transform` path the engine
actually uses. `internal/engine/registry/encrypt_integration_test.go` covers the
registration seam — the transformers register from an `init()` in a
side-effect-only import, so unit tests can pass while a real workflow node
resolves to nothing. See [reachability tests](reachability_tests.md).

UI: `ui/src/components/workflow/Transformation/configs/security/EncryptConfig.tsx`
serves both types, switching on the `transType` prop the dispatcher passes.
