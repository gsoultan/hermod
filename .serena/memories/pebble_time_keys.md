# Pebble time keys (logs and audit rows)

`internal/storage/pebble/time_key.go`.

- Key: `l:<%020d of MaxInt64 - UnixNano>:<id>` for logs, `a:` for audit rows. The number is
  never negative, so a fixed-width print sorts newest first. `ListLogs` and `ListAuditLogs`
  rely on that order and do not sort.
- `PurgeLogs` / `PurgeAuditLogs` call `purgeTimeKeysBefore`: one `DeleteRange` from the key
  just past the cutoff to `<prefix>;`. Nothing is read. A row exactly at the cutoff is kept.
- The first scheme subtracted from `999999999999999999`, which is below every timestamp since
  2001. The key was negative, sorted oldest first, and the purge range covered the rows
  *newer* than the cutoff. `rekeyLegacyTimeKeys` runs in `NewPebbleStorage` and moves those
  keys (the only ones whose number starts with `-`) in batches of 1000; it is resumable and
  costs one empty seek once done.
- Tests: `time_key_test.go` (purge both families, list order, legacy rows rekeyed on open).
- Do not derive a time ordering from a signed number printed with `%0Nd`.
