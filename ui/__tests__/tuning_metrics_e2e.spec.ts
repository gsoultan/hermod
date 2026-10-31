import { test, expect, type Page } from '@playwright/test';
import { login, apiRequest } from './support/auth';

/**
 * The Tuning tab shows what the running engine reports.
 *
 * It read error_rate and backpressure from the status frame, which the engine
 * never filled in, so both said 0% on any flow, failing or not; and it showed
 * nothing else the frame carried. Driven against a real engine: messages go
 * in through a webhook, and the tab has to show them.
 */

const created: { workflows: string[]; sinks: string[]; sources: string[] } = { workflows: [], sinks: [], sources: [] };

test.afterEach(async ({ page }) => {
  await page.evaluate(() => clearInterval((window as any).__tuningTimer)).catch(() => {});
  for (const id of created.workflows.splice(0)) {
    await apiRequest(page, `/api/workflows/${id}/toggle`, { method: 'POST' }).catch(() => {});
    await apiRequest(page, `/api/workflows/${id}`, { method: 'DELETE' }).catch(() => {});
  }
  for (const id of created.sinks.splice(0)) await apiRequest(page, `/api/sinks/${id}`, { method: 'DELETE' }).catch(() => {});
  for (const id of created.sources.splice(0)) await apiRequest(page, `/api/sources/${id}`, { method: 'DELETE' }).catch(() => {});
});

async function post(page: Page, path: string, body: unknown): Promise<any> {
  const res = await apiRequest(page, path, { method: 'POST', body });
  if (res.status >= 400) throw new Error(`fixture setup failed: POST ${path} -> ${res.status} ${res.body}`);
  return JSON.parse(res.body);
}

/** A webhook into one step into a console sink, switched on. */
async function runningFlow(page: Page, name: string, step: Record<string, unknown>) {
  const stamp = `${name}-${Date.now()}`;
  const hook = `/api/webhooks/tuning-${stamp}`;
  const source = await post(page, '/api/sources', { name: `tuning-src-${stamp}`, type: 'webhook', vhost: 'default', active: true, config: { path: hook } });
  created.sources.push(source.id);
  const sink = await post(page, '/api/sinks', { name: `Console ${stamp}`, type: 'stdout', vhost: 'default', config: {} });
  created.sinks.push(sink.id);
  const workflow = await post(page, '/api/workflows', {
    name: `Orders ${name}`, vhost: 'default', active: false,
    nodes: [
      { id: 'n-src', type: 'source', ref_id: source.id, x: 40, y: 150, config: { label: 'Orders webhook' } },
      { id: 'n-step', type: 'transformation', x: 300, y: 150, config: step },
      { id: 'n-snk', type: 'sink', ref_id: sink.id, x: 560, y: 150, config: { label: 'Console' } },
    ],
    edges: [
      { id: 'e-1', source_id: 'n-src', target_id: 'n-step', config: {} },
      { id: 'e-2', source_id: 'n-step', target_id: 'n-snk', config: {} },
    ],
  });
  created.workflows.push(workflow.id);
  const started = await apiRequest(page, `/api/workflows/${workflow.id}/toggle`, { method: 'POST' });
  expect(started.status, `the workflow did not start: ${started.body}`).toBeLessThan(400);
  return { workflow, hook, sinkName: sink.name as string };
}

async function sendContinuously(page: Page, hook: string) {
  await page.evaluate((url) => {
    (window as any).__tuningTimer = setInterval(() => {
      void fetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ id: Date.now(), total: 42 }) });
    }, 100);
  }, hook);
}

const tile = (page: Page, label: string) => page.locator(`[data-metric="${label}"]`);

test('the Tuning tab shows a running flow\'s throughput, steps and sinks', async ({ page }, testInfo) => {
  test.setTimeout(90_000);
  await login(page);
  const { workflow, hook, sinkName } = await runningFlow(page, 'healthy', { label: 'Tag order', transType: 'set', 'column.status': 'seen' });

  await page.goto(`/workflows/${workflow.id}?tab=tuning`);
  await sendContinuously(page, hook);

  await expect(tile(page, 'Throughput')).toContainText(/[1-9]\d*(\.\d)? msg\/s/, { timeout: 20000 });
  await expect(tile(page, 'Processed')).toHaveText(/^Processed[1-9]/, { timeout: 10000 });
  await expect(tile(page, 'Error rate')).toHaveText('Error rate0.00%');
  await expect(page.getByRole('table', { name: 'Steps' })).toContainText('Tag order');
  await expect(page.getByRole('table', { name: 'Sinks' })).toContainText(sinkName);
  await expect(page.getByText('Running within normal limits.')).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('tuning-healthy.png'), fullPage: true });
});

test('the Tuning tab shows the error rate of a flow whose step fails', async ({ page }) => {
  test.setTimeout(90_000);
  await login(page);
  // Nothing listens on port 9, so every lookup fails.
  const { workflow, hook } = await runningFlow(page, 'failing', {
    label: 'Look up customer', transType: 'api_lookup', method: 'GET', url: 'http://127.0.0.1:9/customer', targetField: 'customer', ttl: '0',
  });

  await page.goto(`/workflows/${workflow.id}?tab=tuning`);
  await sendContinuously(page, hook);

  await expect(tile(page, 'Error rate')).toHaveText(/^Error rate[1-9]\d*\.\d\d%$/, { timeout: 20000 });
  await expect(page.getByText(/of messages are failing/)).toBeVisible();
  const row = page.getByRole('table', { name: 'Steps' }).getByRole('row').filter({ hasText: 'Look up customer' });
  await expect(row.getByRole('cell').last()).not.toHaveText('0');
});
