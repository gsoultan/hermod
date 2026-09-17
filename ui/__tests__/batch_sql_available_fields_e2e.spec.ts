import { test, expect, type Page } from '@playwright/test';
import { login, apiRequest } from './support/auth';

/**
 * A db_lookup wired to a batch_sql source showed an empty Available Fields list.
 *
 * Available Fields comes from the upstream source's stored sample
 * (useNodeContext), and a source's sample is captured by the editor right after
 * a successful Test Connection (useSourceForm.testMutation -> fetchSample).
 * fetchSample has no table name to send for a batch source — batch_sql config
 * carries `queries`, never `table` or `tables` — so it posted the empty string,
 * and BatchSQLSource.Sample built "SELECT * FROM  LIMIT 1" out of it. The
 * request 400'd, no sample was ever stored, and every node downstream of the
 * batch source offered no fields.
 *
 * This drives the real editor because the failure is only visible through it:
 * the sample request is fired as a side effect of Test Connection, and the
 * field list is assembled in the browser from what that request stored.
 */

const createdSources: string[] = [];
const createdSinks: string[] = [];
const createdWorkflows: string[] = [];

// The dev stack's own SQLite metadata database. Reading it means the batch
// query hits a table guaranteed to exist and hold rows, without this spec
// needing a SQLite driver of its own to seed a fixture file.
const DEV_DB = new URL('../../.dev/hermod.db', import.meta.url).pathname;

// Aliased columns, so the assertions cannot pass on a column name that some
// other part of the payload (or the lookup's own config) happens to contain.
const BATCH_QUERY =
  "SELECT name AS batch_row_name, type AS batch_row_type FROM sources " +
  "WHERE name > '{{.last_value}}' ORDER BY name";

test.afterEach(async ({ page }) => {
  for (const id of createdWorkflows.splice(0)) {
    await apiRequest(page, `/api/workflows/${id}`, { method: 'DELETE' }).catch(() => {});
  }
  for (const id of createdSinks.splice(0)) {
    await apiRequest(page, `/api/sinks/${id}`, { method: 'DELETE' }).catch(() => {});
  }
  for (const id of createdSources.splice(0)) {
    await apiRequest(page, `/api/sources/${id}`, { method: 'DELETE' }).catch(() => {});
  }
});

async function post(page: Page, path: string, body: unknown): Promise<any> {
  const res = await apiRequest(page, path, { method: 'POST', body });
  if (res.status >= 400) {
    throw new Error(`fixture setup failed: POST ${path} -> ${res.status} ${res.body}`);
  }
  return JSON.parse(res.body);
}

test.describe('batch_sql source feeds a db_lookup node', () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test('the field list fills from the configured batch query', async ({ page }) => {
    test.setTimeout(120000);

    const stamp = Date.now();

    // The delegate holds the connection; the batch source borrows it. CDC has
    // to be off or the engine — and the delegate picker — refuse the pairing.
    const delegate = await post(page, '/api/sources', {
      name: `batchfields-db-${stamp}`,
      type: 'sqlite',
      vhost: 'default',
      active: true,
      config: { path: DEV_DB, use_cdc: 'false' },
    });
    createdSources.push(delegate.id);

    const batch = await post(page, '/api/sources', {
      name: `batchfields-batch-${stamp}`,
      type: 'batch_sql',
      vhost: 'default',
      active: true,
      config: {
        source_id: delegate.id,
        cron: '0 2 * * *',
        incremental_column: 'name',
        queries: JSON.stringify([BATCH_QUERY]),
      },
    });
    createdSources.push(batch.id);

    const sink = await post(page, '/api/sinks', {
      name: `batchfields-snk-${stamp}`,
      type: 'stdout',
      vhost: 'default',
      config: {},
    });
    createdSinks.push(sink.id);

    const workflow = await post(page, '/api/workflows', {
      name: `batchfields-wf-${stamp}`,
      vhost: 'default',
      active: false,
      nodes: [
        { id: 'n-src', type: 'source', ref_id: batch.id, x: 100, y: 100 },
        {
          id: 'n-lookup',
          type: 'transformation',
          config: { transType: 'db_lookup', sourceId: delegate.id, table: 'sources', keyColumn: 'name' },
          x: 340,
          y: 100,
        },
        { id: 'n-snk', type: 'sink', ref_id: sink.id, x: 580, y: 100 },
      ],
      edges: [
        { id: 'e1', source_id: 'n-src', target_id: 'n-lookup' },
        { id: 'e2', source_id: 'n-lookup', target_id: 'n-snk' },
      ],
    });
    createdWorkflows.push(workflow.id);

    await page.goto(`/workflows/${workflow.id}/edit`);
    await page.waitForLoadState('networkidle');

    const nodes = page.locator('.react-flow__node');
    await expect(nodes).toHaveCount(3, { timeout: 30000 });

    // Open the batch source and run Test Connection, which is what fires the
    // sample request. Its config lives on step 2 of the source wizard.
    await nodes.nth(0).dblclick();
    await expect(page.getByText(/step 1: source identity/i)).toBeVisible({ timeout: 20000 });
    await page.getByRole('button', { name: /next step/i }).click();
    await expect(page.getByText(/active queries \(1\)/i)).toBeVisible({ timeout: 20000 });

    const samplePromise = page.waitForResponse(
      (r) => r.url().includes('/api/sources/sample') && r.request().method() === 'POST',
      { timeout: 45000 }
    );
    await page.getByRole('button', { name: /test connection/i }).click();

    const sampled = await samplePromise;
    expect(
      sampled.status(),
      `sampling a batch_sql source failed: ${await sampled.text()}`
    ).toBe(200);
    // The columns are the batch query's own, not some table's — a batch source
    // has no table to fall back on.
    expect(await sampled.text()).toContain('batch_row_name');

    await page.getByRole('button', { name: /^cancel$/i }).click();
    await expect(page.getByText(/step 2: connection settings/i)).toBeHidden({ timeout: 20000 });

    // Now the node the operator was actually looking at. Scoped to the panel:
    // the same paths also appear in the Key Field autocomplete, and an
    // unscoped match would pass on that alone.
    await nodes.nth(1).dblclick();
    const panel = page.getByTestId('available-fields-panel');
    await expect(panel).toBeVisible({ timeout: 20000 });

    const search = panel.getByPlaceholder('Search fields...');
    for (const column of ['batch_row_name', 'batch_row_type']) {
      await search.fill(column);
      await expect(
        panel.getByText(`after.${column}`, { exact: true }),
        `the db_lookup node does not offer ${column}, a column its batch_sql source selects`
      ).toBeVisible({ timeout: 15000 });
      // Hoisted to the root as well, which is where a lookup's Key Field looks.
      await expect(panel.getByText(column, { exact: true })).toBeVisible({ timeout: 15000 });
    }
  });
});
