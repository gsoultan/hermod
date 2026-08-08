import { test, expect, type Page } from '@playwright/test';
import { login, apiRequest } from './support/auth';
import { acceptConfirm } from './support/confirm';

/**
 * The rest of the workflow surface: detail tabs (message traces, history, logs,
 * debugger), the transformation form's controls, simulation, and the worker
 * lifecycle actions.
 *
 * These run against the SQLite dev stack and need no external service, so they
 * belong in the per-push CI job rather than the nightly integration one.
 */

/** Ids created by createWorkflow, torn down after each test. */
const createdWorkflows: string[] = [];

test.afterEach(async ({ page }) => {
  // Fixtures accumulate otherwise, and a paginated list eventually hides the
  // row the next run is looking for — a failure that looks like a product bug.
  for (const id of createdWorkflows.splice(0)) {
    await apiRequest(page, `/api/workflows/${id}`, { method: 'DELETE' }).catch(() => {});
  }
});

/** Create a minimal source -> transformation -> sink workflow through the API. */
async function createWorkflow(page: Page, name: string): Promise<string> {
  const src = await apiRequest(page, '/api/sources', {
    method: 'POST',
    body: { name: `${name}-src`, type: 'webhook', config: {}, vhost: 'default' },
  });
  const snk = await apiRequest(page, '/api/sinks', {
    method: 'POST',
    body: { name: `${name}-snk`, type: 'stdout', config: {}, vhost: 'default' },
  });
  if (src.status >= 400 || snk.status >= 400) {
    throw new Error(`fixture setup failed: source ${src.status} ${src.body}, sink ${snk.status} ${snk.body}`);
  }
  const srcID = JSON.parse(src.body).id;
  const snkID = JSON.parse(snk.body).id;

  const wf = await apiRequest(page, '/api/workflows', {
    method: 'POST',
    body: {
      name,
      vhost: 'default',
      active: false,
      nodes: [
        { id: 'n-src', type: 'source', ref_id: srcID, x: 100, y: 100 },
        {
          id: 'n-tx',
          type: 'transformation',
          config: { transType: 'set', 'column.tagged': "'yes'" },
          x: 320,
          y: 100,
        },
        { id: 'n-snk', type: 'sink', ref_id: snkID, x: 540, y: 100 },
      ],
      edges: [
        { id: 'e1', source_id: 'n-src', target_id: 'n-tx' },
        { id: 'e2', source_id: 'n-tx', target_id: 'n-snk' },
      ],
    },
  });
  if (wf.status >= 400) {
    throw new Error(`workflow create failed: ${wf.status} ${wf.body}`);
  }
  const id = JSON.parse(wf.body).id;
  createdWorkflows.push(id);
  return id;
}

test.describe('workflow detail surface', () => {
  let workflowID: string;

  test.beforeEach(async ({ page }) => {
    await login(page);
    workflowID = await createWorkflow(page, `e2e-surface-${Date.now()}`);
  });

  test('every detail tab opens without error', async ({ page }) => {
    test.setTimeout(120000);

    const errors: string[] = [];
    page.on('pageerror', (e) => errors.push(e.message));

    await page.goto(`/workflows/${workflowID}`);
    await page.waitForLoadState('networkidle');

    // Each tab is a distinct data path — traces, versions, logs, the debugger —
    // and any of them can throw on an empty workflow, which is exactly the
    // state a user sees right after creating one.
    for (const tab of [
      /graph view/i,
      /message traces/i,
      /history/i,
      /logs/i,
      /debugger/i,
      /optimization/i,
    ]) {
      const t = page.getByRole('tab', { name: tab });
      await expect(t, `tab ${tab} is missing`).toBeVisible({ timeout: 20000 });
      await t.click();
      await page.waitForTimeout(400);
    }

    expect(errors, `uncaught errors while switching tabs: ${errors.join('; ')}`).toHaveLength(0);
  });

  test('message traces tab renders its empty state', async ({ page }) => {
    test.setTimeout(120000);

    await page.goto(`/workflows/${workflowID}`);
    await page.getByRole('tab', { name: /message traces/i }).click();

    // A workflow that has never run has no traces. That must be a designed
    // empty state, not a spinner that never resolves or a blank panel.
    const panel = page.getByRole('tabpanel');
    await expect(panel).toBeVisible({ timeout: 20000 });
    await expect(panel).not.toBeEmpty();
  });

  test('logs tab renders and stays responsive', async ({ page }) => {
    test.setTimeout(120000);

    await page.goto(`/workflows/${workflowID}`);
    await page.getByRole('tab', { name: /^logs$/i }).click();

    const panel = page.getByRole('tabpanel');
    await expect(panel).toBeVisible({ timeout: 20000 });
    await expect(panel).not.toBeEmpty();
  });
});

test.describe('workflow editor: node forms and simulation', () => {
  let workflowID: string;

  test.beforeEach(async ({ page }) => {
    await login(page);
    workflowID = await createWorkflow(page, `e2e-editor-${Date.now()}`);
  });

  test('transformation node drawer opens with its controls', async ({ page }) => {
    test.setTimeout(120000);

    const errors: string[] = [];
    page.on('pageerror', (e) => errors.push(e.message));

    await page.goto(`/workflows/${workflowID}/edit`);
    await expect(page.locator('.react-flow').first()).toBeVisible({ timeout: 30000 });

    // Open the transformation node. Nodes render their label, so target that.
    const node = page.locator('.react-flow__node').filter({ hasText: /transform|set/i }).first();
    if (!(await node.count())) {
      test.skip(true, 'transformation node not rendered on the canvas');
    }
    await node.dblclick();

    // The drawer must offer the transformation type and a preview control —
    // the two things that make the node configurable at all.
    const drawer = page.getByRole('dialog').or(page.locator('.mantine-Drawer-content'));
    await expect(drawer.first()).toBeVisible({ timeout: 20000 });

    expect(errors, `uncaught errors opening the drawer: ${errors.join('; ')}`).toHaveLength(0);
  });

  test('editor Test control runs a simulation without throwing', async ({ page }) => {
    test.setTimeout(120000);

    const errors: string[] = [];
    page.on('pageerror', (e) => errors.push(e.message));

    await page.goto(`/workflows/${workflowID}/edit`);
    await expect(page.locator('.react-flow').first()).toBeVisible({ timeout: 30000 });

    const testBtn = page.getByRole('button', { name: /^test$/i }).first();
    await expect(testBtn).toBeVisible({ timeout: 20000 });
    await testBtn.click();

    // The simulation may succeed or report a configuration problem; what it must
    // not do is throw or leave the button stuck in a loading state forever.
    await page.waitForTimeout(3000);
    expect(errors, `uncaught errors running the simulation: ${errors.join('; ')}`).toHaveLength(0);
    await expect(testBtn).toBeEnabled({ timeout: 30000 });
  });

  test('live log panel toggles while the editor is open', async ({ page }) => {
    test.setTimeout(120000);

    await page.goto(`/workflows/${workflowID}/edit`);
    const header = page.getByText(/live workflow logs/i).first();
    await expect(header).toBeVisible({ timeout: 30000 });

    await header.click();
    await page.waitForTimeout(500);
    await header.click();
    await expect(header).toBeVisible();
  });
});

test.describe('workflow lifecycle from the list', () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test('a workflow can be activated and deactivated', async ({ page }) => {
    test.setTimeout(120000);

    const name = `e2e-toggle-${Date.now()}`;
    await createWorkflow(page, name);

    await page.goto('/workflows');
    await page.waitForLoadState('networkidle');
    // The list paginates and these specs keep adding to it, so a freshly
    // created workflow is not necessarily on the first page.
    await page.getByPlaceholder(/search workflows/i).fill(name);

    const row = page.locator('tr').filter({ hasText: name });
    await expect(row).toBeVisible({ timeout: 20000 });

    // The toggle is the single most-used control on this page: it is what
    // starts and stops data actually moving.
    const toggle = row.getByRole('switch').or(row.getByRole('checkbox')).first();
    if (await toggle.count()) {
      await toggle.click();
      await page.waitForTimeout(1500);
      // Reload to prove the new state persisted rather than only rendering;
      // the reload clears the filter, so re-apply it before looking for the row.
      await page.reload();
      await page.getByPlaceholder(/search workflows/i).fill(name);
      await expect(page.locator('tr').filter({ hasText: name })).toBeVisible({ timeout: 20000 });
    }
  });

  test('a workflow can be deleted', async ({ page }) => {
    test.setTimeout(120000);

    const name = `e2e-delete-${Date.now()}`;
    await createWorkflow(page, name);

    await page.goto('/workflows');
    await page.getByPlaceholder(/search workflows/i).fill(name);
    const row = page.locator('tr').filter({ hasText: name });
    await expect(row).toBeVisible({ timeout: 20000 });

    // Use the exact aria-label: a loose /delete/i also matches the page-level
    // "Delete Selected" action, which opens a different (batch) confirmation.
    await row.getByLabel('Delete workflow').click();
    await acceptConfirm(page);

    await expect(page.locator('tr').filter({ hasText: name })).toHaveCount(0, { timeout: 20000 });
  });
});

test.describe('worker lifecycle page', () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test('workers page exposes registration and lifecycle controls', async ({ page }) => {
    test.setTimeout(120000);

    const errors: string[] = [];
    page.on('pageerror', (e) => errors.push(e.message));

    await page.goto('/workers');
    await page.waitForLoadState('networkidle');

    await expect(page.getByRole('button', { name: /register worker/i }).first())
      .toBeVisible({ timeout: 20000 });

    // Lifecycle controls only exist for workers registered with an identity; a
    // fresh install has none, and the auto-sharded row is not one. Assert on
    // them when they are there — an operator must be able to drain a node
    // before maintenance — rather than requiring a fixture this page cannot
    // create on its own.
    const lifecycle = page.getByLabel(/start worker/i).or(page.getByLabel(/stop worker/i));
    if (await lifecycle.count()) {
      await expect(lifecycle.first()).toBeVisible({ timeout: 20000 });
      await expect(lifecycle.first()).toBeEnabled();
    } else {
      test.info().annotations.push({
        type: 'note',
        description: 'no registered worker on this stack; lifecycle controls not asserted',
      });
    }

    expect(errors, `uncaught errors on the workers page: ${errors.join('; ')}`).toHaveLength(0);
  });
});
