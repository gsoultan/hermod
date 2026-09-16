/**
 * Whether a source is serving change data capture, and whether it may therefore
 * be the target of SQL that is not its change stream — a db_lookup's
 * per-message query, a batch_sql's scheduled one.
 *
 * This mirrors `hermod.SourceUsesCDC` / `hermod.SourceAllowsDirectQueries`
 * (stringmap.go), and reading the flag the same way the backend does is the
 * entire point. `use_cdc` is opt-out: `internal/factory/factory.go` builds every
 * database source with `useCDC := cfg.Config["use_cdc"] != "false"`, so a source
 * carrying no key at all is a CDC source. A picker that treats a missing key as
 * "not CDC" offers exactly the sources the pipeline will refuse.
 *
 * The value may arrive as a string or a boolean depending on how the client
 * encoded it (see the StringMap doc comment), so it is normalised before
 * comparison.
 */
export function sourceUsesCDC(source: { config?: Record<string, any> } | null | undefined): boolean {
  return String(source?.config?.use_cdc ?? '') !== 'false';
}

/**
 * SQL Server is the documented exception: its CDC is read back through ordinary
 * queries against change tables, so it carries none of the replication cost the
 * rule exists to avoid.
 */
export function sourceAllowsDirectQueries(
  source: { type?: string; config?: Record<string, any> } | null | undefined
): boolean {
  return source?.type === 'mssql' || !sourceUsesCDC(source);
}
