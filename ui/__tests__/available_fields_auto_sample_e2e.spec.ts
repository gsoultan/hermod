import { test, expect, type Page } from '@playwright/test';
import { login, apiRequest } from './support/auth';

/**
 * A transformation wired to a fully configured batch_sql source offered no
 * Available Fields.
 *
 * The list is built from the upstream source's stored sample (useNodeContext),
 * and that sample was only ever written by two explicit actions: Test
 * Connection in the source wizard, and the refresh icon beside the list.
 * Neither is on the path an operator takes to open a transformation, so a
 * source created through the API, restored from a bundle, or saved from the
 * wizard without pressing Test Connection had no sample at all — and every node
 * downstream of it opened empty, on a workflow that was correctly configured
 * and sampled fine on the first try.
 *
 * Driven through the real editor because that is the only place the failure is
 * visible: the capture is a side effect of opening the panel, and the field
 * list is assembled in the browser from what that capture stored.
 */

const createdSources: string[] = [];
const createdSinks: string[] = [];
const createdWorkflows: string[] = [];

// The dev stack's own SQLite metadata database, so the batch query hits a table
// guaranteed to exist and hold rows without this spec seeding a fixture file.
const DEV_DB = new URL('../../.dev/hermod.db', import.meta.url).pathname;

// Aliased columns, so an assertion cannot pass on a name that some other part
// of the payload happens to contain.
const BATCH_QUERY =
  "SELECT name AS auto_row_name, type AS auto_row_type FROM sources " +
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

async function stdoutSink(page: Page, stamp: number): Promise<string> {
  const sink = await post(page, '/api/sinks', {
    name: `autofields-snk-${stamp}`, type: 'stdout', vhost: 'default', config: {},
  });
  createdSinks.push(sink.id);
  return sink.id;
}

test.describe('Available Fields on a never-sampled source', () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test('fills itself when a transformation downstream of batch_sql is opened', async ({ page }) => {
    test.setTimeout(120000);
    const stamp = Date.now();

    // The delegate holds the connection; the batch source borrows it. CDC has
    // to be off or the delegate picker refuses the pairing.
    const delegate = await post(page, '/api/sources', {
      name: `autofields-db-${stamp}`, type: 'sqlite', vhost: 'default', active: true,
      config: { path: DEV_DB, use_cdc: 'false' },
    });
    createdSources.push(delegate.id);

    const batch = await post(page, '/api/sources', {
      name: `autofields-batch-${stamp}`, type: 'batch_sql', vhost: 'default', active: true,
      config: {
        source_id: delegate.id, cron: '0 2 * * *', incremental_column: 'name',
        queries: JSON.stringify([BATCH_QUERY]),
      },
    });
    createdSources.push(batch.id);

    const sinkID = await stdoutSink(page, stamp);

    const workflow = await post(page, '/api/workflows', {
      name: `autofields-wf-${stamp}`, vhost: 'default', active: false,
      nodes: [
        { id: 'n-src', type: 'source', ref_id: batch.id, x: 100, y: 100 },
        { id: 'n-tr', type: 'transformation', config: { transType: 'mapping' }, x: 340, y: 100 },
        { id: 'n-snk', type: 'sink', ref_id: sinkID, x: 580, y: 100 },
      ],
      edges: [
        { id: 'e1', source_id: 'n-src', target_id: 'n-tr' },
        { id: 'e2', source_id: 'n-tr', target_id: 'n-snk' },
      ],
    });
    createdWorkflows.push(workflow.id);

    // Nothing has sampled this source: the symptom is the starting state.
    const before = await apiRequest(page, `/api/sources/${batch.id}`, { method: 'GET' });
    expect(JSON.parse(before.body).sample || '', 'fixture should start with no sample').toBe('');

    await page.goto(`/workflows/${workflow.id}/edit`);
    await page.waitForLoadState('networkidle');
    const nodes = page.locator('.react-flow__node');
    await expect(nodes).toHaveCount(3, { timeout: 30000 });

    // Straight to the transformation. No Test Connection, no refresh icon —
    // this is the path the operator actually took.
    await nodes.nth(1).dblclick();
    const panel = page.getByTestId('available-fields-panel');
    await expect(panel).toBeVisible({ timeout: 20000 });

    const search = panel.getByPlaceholder('Search fields...');
    for (const column of ['auto_row_name', 'auto_row_type']) {
      await search.fill(column);
      await expect(
        panel.getByText(`after.${column}`, { exact: true }),
        `the transformation does not offer ${column}, a column its batch_sql source selects`
      ).toBeVisible({ timeout: 30000 });
      // Hoisted to the root too, which is where field pickers look.
      await expect(panel.getByText(column, { exact: true })).toBeVisible({ timeout: 15000 });
    }

    // The capture is stored, not just held in the tab, so the next person to
    // open the workflow does not pay for it again.
    const after = await apiRequest(page, `/api/sources/${batch.id}`, { method: 'GET' });
    expect(JSON.parse(after.body).sample || '').toContain('auto_row_name');
  });

  // Sampling a queue consumes a message. Filling a field list is not a good
  // enough reason to eat one, so auto-capture is limited to the source types
  // isNonDestructiveSample certifies as read-only, and a queue must not be
  // touched unless the operator asks.
  test('never samples a queue source on its own', async ({ page }) => {
    test.setTimeout(120000);
    const stamp = Date.now();

    const queue = await post(page, '/api/sources', {
      name: `autofields-kafka-${stamp}`, type: 'kafka', vhost: 'default', active: false,
      config: { brokers: 'localhost:9092', topic: `autofields-${stamp}`, group_id: 'autofields' },
    });
    createdSources.push(queue.id);

    const sinkID = await stdoutSink(page, stamp);

    const workflow = await post(page, '/api/workflows', {
      name: `autofields-q-wf-${stamp}`, vhost: 'default', active: false,
      nodes: [
        { id: 'n-src', type: 'source', ref_id: queue.id, x: 100, y: 100 },
        { id: 'n-tr', type: 'transformation', config: { transType: 'mapping' }, x: 340, y: 100 },
        { id: 'n-snk', type: 'sink', ref_id: sinkID, x: 580, y: 100 },
      ],
      edges: [
        { id: 'e1', source_id: 'n-src', target_id: 'n-tr' },
        { id: 'e2', source_id: 'n-tr', target_id: 'n-snk' },
      ],
    });
    createdWorkflows.push(workflow.id);

    const sampleCalls: string[] = [];
    page.on('request', (r) => {
      if (r.url().includes('/api/sources/sample')) sampleCalls.push(r.url());
    });

    await page.goto(`/workflows/${workflow.id}/edit`);
    await page.waitForLoadState('networkidle');
    const nodes = page.locator('.react-flow__node');
    await expect(nodes).toHaveCount(3, { timeout: 30000 });

    await nodes.nth(1).dblclick();
    await expect(page.getByTestId('available-fields-panel')).toBeVisible({ timeout: 20000 });
    await page.waitForTimeout(5000);

    expect(sampleCalls, 'opening a node must not consume a message from a queue source').toEqual([]);
  });
});
