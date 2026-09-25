import { test, expect, type Page, type Locator } from '@playwright/test';
import { login, apiRequest } from './support/auth';

/**
 * One click on the refresh icon beside AVAILABLE FIELDS has to reach every node
 * after it: the node's own fields and Live Preview, then the next node's fields
 * and Live Preview, and so on down the branch.
 *
 * The refresh stores a fresh sample and runs the whole workflow on it; each
 * node reads what the node before it emitted in that run, and a node's Live
 * Preview re-runs whenever that input changes. Three things stopped it:
 *
 *   - the run was refused for a workflow with no sink yet — every workflow
 *     while it is being built — so nothing downstream moved;
 *   - the walk each node falls back to read the source node's `lastSample`, a
 *     copy Test Connection writes and saving the workflow keeps, before the
 *     sample the refresh had just stored;
 *   - the run fed one sample to every source, so on a workflow with two
 *     sources a refresh on one branch put its columns on the other.
 *
 * Seeded the way the stored workflows that showed it look: a saved source node
 * carrying a `lastSample`, and a source record with no sample of its own.
 */

const createdSources: string[] = [];
const createdSinks: string[] = [];
const createdWorkflows: string[] = [];

// The dev stack's own SQLite metadata database, so the batch query reads a
// table that is guaranteed to exist and hold rows.
const DEV_DB = new URL('../../.dev/hermod.db', import.meta.url).pathname;

// Aliased, so an assertion cannot pass on a name that some other part of the
// payload happens to contain.
const probeQuery = (...columns: string[]) =>
  `SELECT ${columns.join(', ')} FROM sources WHERE name > '{{.last_value}}' ORDER BY name`;

// What the source returned the day someone pressed Test Connection.
const STALE_SAMPLE = { operation: 'snapshot', after: { retired_col: 'gone' } };

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

async function batchSource(page: Page, stamp: number, name: string, query: string): Promise<string> {
  const delegate = await post(page, '/api/sources', {
    name: `refresh-db-${name}-${stamp}`, type: 'sqlite', vhost: 'default', active: true,
    config: { path: DEV_DB, use_cdc: 'false' },
  });
  createdSources.push(delegate.id);
  const batch = await post(page, '/api/sources', {
    name: `refresh-${name}-${stamp}`, type: 'batch_sql', vhost: 'default', active: true,
    config: {
      source_id: delegate.id, cron: '0 2 * * *', incremental_column: 'name',
      queries: JSON.stringify([query]),
    },
  });
  createdSources.push(batch.id);
  return batch.id;
}

const setNode = (id: string, x: number, column: string, value: string) => ({
  id, type: 'transformation', config: { transType: 'set', [`column.${column}`]: `'${value}'` }, x, y: 100,
});

/** Opens a node's drawer and returns its field list and Live Preview. */
async function open(page: Page, index: number): Promise<{ fields: Locator; preview: Locator }> {
  await page.locator('.react-flow__node').nth(index).dblclick();
  const fields = page.getByTestId('available-fields-panel');
  await expect(fields).toBeVisible({ timeout: 20000 });
  return { fields, preview: page.getByTestId('live-preview') };
}

async function close(page: Page) {
  await page.getByRole('button', { name: 'Close' }).click();
  await expect(page.getByTestId('available-fields-panel')).toBeHidden();
}

async function expectFields(fields: Locator, offered: string[], gone: string[]) {
  const search = fields.getByPlaceholder('Search fields...');
  for (const path of offered) {
    await search.fill(path);
    await expect(fields.getByText(path, { exact: true }), `${path} should be offered`).toBeVisible({ timeout: 15000 });
  }
  for (const path of gone) {
    await search.fill(path);
    await expect(fields.getByText(path, { exact: true }), `${path} should be gone`).toHaveCount(0);
  }
}

async function expectPreview(preview: Locator, shows: string[], lacks: string[]) {
  for (const text of shows) {
    await expect(preview, `Live Preview should show ${text}`).toContainText(text, { timeout: 15000 });
  }
  for (const text of lacks) {
    await expect(preview, `Live Preview should no longer show ${text}`).not.toContainText(text, { timeout: 15000 });
  }
}

async function refresh(page: Page, fields: Locator) {
  const sampled = page.waitForResponse(
    (r) => r.url().endsWith('/api/sources/sample') && r.request().method() === 'POST'
  );
  const simulated = page.waitForResponse(
    (r) => r.url().endsWith('/api/workflows/test') && r.request().method() === 'POST'
  );
  await fields.getByRole('button', { name: 'Refresh sample data and fields' }).click();
  expect((await sampled).status(), 'the refresh must sample successfully').toBe(200);
  const run = await simulated;
  expect(run.status(), `the workflow run was refused: ${await run.text()}`).toBe(200);
}

test.describe('Refreshing Available Fields', () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test('carries one refresh through every node after it, fields and preview alike', async ({ page }) => {
    test.setTimeout(180000);
    const stamp = Date.now();
    const batch = await batchSource(page, stamp, 'chain', probeQuery('name AS refresh_probe_name', 'type AS refresh_probe_type'));

    // No sink yet: the state a workflow is in while its nodes are configured.
    const workflow = await post(page, '/api/workflows', {
      name: `refresh-chain-${stamp}`, vhost: 'default', active: false,
      nodes: [
        { id: 'n-src', type: 'source', ref_id: batch, config: { lastSample: STALE_SAMPLE }, x: 100, y: 100 },
        setNode('n-a', 340, 'a_tag', 'from-A'),
        setNode('n-b', 580, 'b_tag', 'from-B'),
        { id: 'n-c', type: 'transformation', config: { transType: 'mapping' }, x: 820, y: 100 },
      ],
      edges: [
        { id: 'e1', source_id: 'n-src', target_id: 'n-a' },
        { id: 'e2', source_id: 'n-a', target_id: 'n-b' },
        { id: 'e3', source_id: 'n-b', target_id: 'n-c' },
      ],
    });
    createdWorkflows.push(workflow.id);

    await page.goto(`/workflows/${workflow.id}/edit`);
    await page.waitForLoadState('networkidle');
    await expect(page.locator('.react-flow__node')).toHaveCount(4, { timeout: 30000 });

    // Node A starts on the stale copy: that is the state being reported.
    const a = await open(page, 1);
    await expectFields(a.fields, ['after.retired_col'], []);

    await refresh(page, a.fields);

    // A: its fields, and the preview run on them.
    await expectFields(a.fields, ['after.refresh_probe_name'], ['after.retired_col']);
    await expectPreview(a.preview, ['"a_tag": "from-A"', 'refresh_probe_name'], ['retired_col']);
    await close(page);

    // B: what A emitted from the new sample, and B's own preview on it.
    const b = await open(page, 2);
    await expectFields(b.fields, ['after.a_tag', 'after.refresh_probe_name'], ['after.retired_col']);
    await expectPreview(b.preview, ['"b_tag": "from-B"', '"a_tag": "from-A"', 'refresh_probe_name'], ['retired_col']);
    await close(page);

    // C: and so on.
    const c = await open(page, 3);
    await expectFields(c.fields, ['after.a_tag', 'after.b_tag', 'after.refresh_probe_name'], ['after.retired_col']);
    await expectPreview(c.preview, ['"a_tag": "from-A"', '"b_tag": "from-B"', 'refresh_probe_name'], ['retired_col']);

    const stored = await apiRequest(page, `/api/sources/${batch}`, { method: 'GET' });
    expect(JSON.parse(stored.body).sample || '').toContain('refresh_probe_name');
  });

  test('previews each branch from its own source', async ({ page }) => {
    test.setTimeout(180000);
    const stamp = Date.now();
    const left = await batchSource(page, stamp, 'left', probeQuery('name AS left_name'));
    const right = await batchSource(page, stamp, 'right', probeQuery('type AS right_type'));

    // The left branch already has a stored sample; the refresh happens on the
    // right one.
    const seeded = await apiRequest(page, `/api/sources/${left}/sample`, {
      method: 'PUT',
      body: { sample: JSON.stringify({ operation: 'snapshot', after: { left_name: 'L' } }) },
    });
    expect(seeded.status, `seeding the left sample: ${seeded.body}`).toBeLessThan(300);

    const sink = await post(page, '/api/sinks', {
      name: `refresh-snk-${stamp}`, type: 'stdout', vhost: 'default', config: {},
    });
    createdSinks.push(sink.id);

    const workflow = await post(page, '/api/workflows', {
      name: `refresh-branches-${stamp}`, vhost: 'default', active: false,
      nodes: [
        { id: 'n-left', type: 'source', ref_id: left, x: 100, y: 100 },
        { id: 'n-right', type: 'source', ref_id: right, x: 100, y: 300 },
        setNode('n-a', 340, 'a_tag', 'left-branch'),
        { ...setNode('n-b', 340, 'b_tag', 'right-branch'), y: 300 },
        { id: 'n-snk', type: 'sink', ref_id: sink.id, x: 580, y: 200 },
      ],
      edges: [
        { id: 'e1', source_id: 'n-left', target_id: 'n-a' },
        { id: 'e2', source_id: 'n-right', target_id: 'n-b' },
        { id: 'e3', source_id: 'n-a', target_id: 'n-snk' },
        { id: 'e4', source_id: 'n-b', target_id: 'n-snk' },
      ],
    });
    createdWorkflows.push(workflow.id);

    await page.goto(`/workflows/${workflow.id}/edit`);
    await page.waitForLoadState('networkidle');
    await expect(page.locator('.react-flow__node')).toHaveCount(5, { timeout: 30000 });

    const b = await open(page, 3);
    await refresh(page, b.fields);
    await expectFields(b.fields, ['after.right_type'], ['after.left_name']);
    await close(page);

    // The left branch still shows its own columns, not the refreshed branch's.
    const a = await open(page, 2);
    await expectFields(a.fields, ['after.left_name'], ['after.right_type']);
    await expectPreview(a.preview, ['"a_tag": "left-branch"', 'left_name'], ['right_type']);
  });
});
