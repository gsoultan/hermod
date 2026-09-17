import { describe, it, expect } from 'vitest';
import { validateSourceForSampling } from '@/components/workflow/Source/sourceSampling';
import type { Source } from '@/types';

// BatchSQLSourceConfig writes exactly four keys: source_id, cron,
// incremental_column and `queries` — a JSON array of whole SQL statements.
// It has never written `query` or `table`, and neither has the backend, which
// reads cfg.Config["queries"] in registry.go's batch_sql branch.
//
// validateSourceForSampling asked for `query` or `table`, so a fully configured
// batch_sql source was always reported invalid. SamplePanel renders the issue
// list instead of the preview when validation fails, so "Fetch Sample Now"
// would never be drawn and no sample could be captured through it.
//
// SamplePanel is currently unrendered — the editor captures a source's sample
// as a side effect of Test Connection instead, which is the path
// batch_sql_available_fields_e2e.spec.ts drives — so this rule is latent. It is
// pinned here because the module is exported and the panel is one import away
// from being live again.
describe('batch_sql sampling pre-flight', () => {
  function batchSource(config: Record<string, any>): Source {
    return { name: 'nightly orders', type: 'batch_sql', config } as Source;
  }

  it('accepts a source configured the way the editor writes it', () => {
    const res = validateSourceForSampling(
      batchSource({
        source_id: 'src-reporting',
        cron: '0 2 * * *',
        incremental_column: 'id',
        queries: JSON.stringify(['SELECT id, name FROM orders']),
      })
    );

    expect(res.issues).toEqual([]);
    expect(res.valid).toBe(true);
  });

  it('still asks for a query when only the connection is chosen', () => {
    const res = validateSourceForSampling(
      batchSource({ source_id: 'src-reporting', cron: '0 2 * * *' })
    );

    expect(res.valid).toBe(false);
    expect(res.issues.join(' ')).toMatch(/quer/i);
  });

  // An empty array serialises to "[]", which is a non-empty string and would
  // sail past a bare truthiness check while naming no query at all.
  it('treats an empty query list as no query', () => {
    const res = validateSourceForSampling(
      batchSource({ source_id: 'src-reporting', cron: '0 2 * * *', queries: '[]' })
    );

    expect(res.valid).toBe(false);
    expect(res.issues.join(' ')).toMatch(/quer/i);
  });

  it('still asks for the database connection when none is chosen', () => {
    const res = validateSourceForSampling(
      batchSource({ queries: JSON.stringify(['SELECT 1']) })
    );

    expect(res.valid).toBe(false);
    expect(res.issues.join(' ')).toMatch(/connection/i);
  });
});
