import { test, expect, type Page } from '@playwright/test';
import { login, apiRequest } from './support/auth';

/**
 * Clicking Test used to leave the canvas exactly as it was: the toast said
 * "Active paths are highlighted" and nothing on the canvas changed. This drives
 * the whole path the operator takes -- a stored workflow and a stored sample,
 * the editor's Test button, the engine's simulation -- and asserts the canvas
 * shows where the sample went.
 *
 * The workflow branches, so there is a path to see: a condition sends gold
 * customers one way and everyone else the other, and both lanes end at the same
 * sink. The sample is a gold customer.
 */

const createdSources: string[] = [];
const createdSinks: string[] = [];
const createdWorkflows: string[] = [];

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

async function send(page: Page, method: string, path: string, body: unknown): Promise<any> {
  const res = await apiRequest(page, path, { method, body });
  if (res.status >= 400) {
    throw new Error(`fixture setup failed: ${method} ${path} -> ${res.status} ${res.body}`);
  }
  return res.body ? JSON.parse(res.body) : null;
}

async function branchingWorkflow(page: Page, stamp: number): Promise<string> {
  const source = await send(page, 'POST', '/api/sources', {
    name: `sim-path-src-${stamp}`, type: 'webhook', vhost: 'default', active: true,
    config: { path: `/sim-path-${stamp}` },
  });
  createdSources.push(source.id);
  // The Test button runs each source node on the sample stored on its source.
  await send(page, 'PUT', `/api/sources/${source.id}/sample`, { sample: JSON.stringify({ tier: 'gold' }) });

  const sink = await send(page, 'POST', '/api/sinks', {
    name: `sim-path-snk-${stamp}`, type: 'stdout', vhost: 'default', config: {},
  });
  createdSinks.push(sink.id);

  const lane = (id: string, label: string, value: string, y: number) => ({
    id, type: 'transformation', x: 700, y,
    config: { label, transType: 'set', 'column.lane': `'${value}'` },
  });
  const workflow = await send(page, 'POST', '/api/workflows', {
    name: `sim-path-wf-${stamp}`, vhost: 'default', active: false,
    nodes: [
      { id: 'n-src', type: 'source', ref_id: source.id, x: 40, y: 200, config: { label: 'Orders' } },
      { id: 'n-cond', type: 'condition', x: 360, y: 200, config: { label: 'Is gold?', field: 'tier', operator: '=', value: 'gold' } },
      lane('n-gold', 'Gold lane', 'gold', 40),
      lane('n-other', 'Other lane', 'other', 360),
      { id: 'n-sink', type: 'sink', ref_id: sink.id, x: 1040, y: 200, config: { label: 'Console' } },
    ],
    edges: [
      { id: 'e-in', source_id: 'n-src', target_id: 'n-cond', config: { label: '' } },
      { id: 'e-true', source_id: 'n-cond', target_id: 'n-gold', source_handle: 'true', config: { label: 'true' } },
      { id: 'e-false', source_id: 'n-cond', target_id: 'n-other', source_handle: 'false', config: { label: 'false' } },
      { id: 'e-gold-out', source_id: 'n-gold', target_id: 'n-sink', config: { label: '' } },
      { id: 'e-other-out', source_id: 'n-other', target_id: 'n-sink', config: { label: '' } },
    ],
  });
  createdWorkflows.push(workflow.id);
  return workflow.id;
}

const edgePath = (page: Page, id: string) =>
  page.getByTestId(`rf__edge-${id}`).locator('path.react-flow__edge-path');

async function runTest(page: Page) {
  const simulated = page.waitForResponse(
    (r) => r.url().endsWith('/api/workflows/test') && r.request().method() === 'POST'
  );
  await page.getByRole('button', { name: 'Test', exact: true }).click();
  const run = await simulated;
  expect(run.status(), `the simulation was refused: ${await run.text()}`).toBe(200);
}

test.describe('Test run simulation', () => {
  for (const scheme of ['dark', 'light'] as const) {
    test(`shows the path the sample took, and clearing it restores the canvas (${scheme})`, async ({ page }, testInfo) => {
      await page.addInitScript((value) => localStorage.setItem('mantine-color-scheme-value', value), scheme);
      await login(page);
      const id = await branchingWorkflow(page, Date.now());

      await page.goto(`/workflows/${id}/edit`);
      await expect(page.getByTestId('rf__node-n-sink')).toBeVisible({ timeout: 20000 });
      // Nothing is marked before a run.
      await expect(page.locator('[data-simulation]')).toHaveCount(0);

      await runTest(page);

      for (const edge of ['e-in', 'e-true', 'e-gold-out']) {
        await expect(edgePath(page, edge), edge).toHaveAttribute('data-simulation', 'taken');
      }
      for (const edge of ['e-false', 'e-other-out']) {
        await expect(edgePath(page, edge), edge).toHaveAttribute('data-simulation', 'untaken');
      }

      const status = (node: string) => page.getByTestId(`rf__node-${node}`).getByText(/^Simulation:/);
      await expect(status('n-src')).toHaveText(/^Simulation: passed/);
      await expect(status('n-cond')).toHaveText(/^Simulation: passed, took branch "true"/);
      await expect(status('n-gold')).toHaveText(/^Simulation: passed/);
      await expect(status('n-other')).toHaveText(/^Simulation: not reached/);
      await expect(status('n-sink')).toHaveText(/^Simulation: passed/);

      const summary = page.getByRole('region', { name: 'Simulation result' });
      await expect(summary).toBeVisible();
      await expect(summary).toContainText('4 passed');
      await expect(summary).toContainText('1 not reached');

      // Settled: the ring and the path fade in over a short transition, and a
      // capture taken the moment the result lands shows the canvas before it.
      await page.screenshot({ path: testInfo.outputPath(`simulation-path-${scheme}.png`), animations: 'disabled' });

      await summary.getByRole('button', { name: 'Clear simulation' }).click();
      await expect(summary).toBeHidden();
      await expect(page.locator('[data-simulation]')).toHaveCount(0);
      await expect(page.getByTestId('rf__node-n-other').getByText(/^Simulation:/)).toHaveCount(0);
    });
  }
});
