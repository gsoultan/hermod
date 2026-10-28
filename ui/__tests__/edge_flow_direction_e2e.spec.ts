import { test, expect, type Page } from '@playwright/test';
import { login, apiRequest } from './support/auth';

/**
 * An edge moves only while messages cross it, at the speed they cross it, and
 * from source to target.
 *
 * It used to animate every edge all the time, running or not, and backwards:
 * the keyframe raised stroke-dashoffset, which slides the dashes toward the
 * start of the path. Motion and direction cannot be seen in jsdom, which runs no
 * animations, so this reads them from a real browser against a real engine.
 */

const created: { workflows: string[]; sinks: string[]; sources: string[] } = { workflows: [], sinks: [], sources: [] };

test.afterEach(async ({ page }) => {
  for (const id of created.workflows.splice(0)) {
    await apiRequest(page, `/api/workflows/${id}`, { method: 'DELETE' }).catch(() => {});
  }
  for (const id of created.sinks.splice(0)) {
    await apiRequest(page, `/api/sinks/${id}`, { method: 'DELETE' }).catch(() => {});
  }
  for (const id of created.sources.splice(0)) {
    await apiRequest(page, `/api/sources/${id}`, { method: 'DELETE' }).catch(() => {});
  }
});

async function post(page: Page, path: string, body: unknown): Promise<any> {
  const res = await apiRequest(page, path, { method: 'POST', body });
  if (res.status >= 400) throw new Error(`fixture setup failed: POST ${path} -> ${res.status} ${res.body}`);
  return JSON.parse(res.body);
}

async function dashOffsets(path: ReturnType<Page['locator']>): Promise<number[]> {
  // Six readings, 60ms apart. Each loop of the animation ends by jumping back to
  // where it started, so at most one step can run the wrong way.
  const offsets: number[] = [];
  for (let i = 0; i < 6; i++) {
    offsets.push(await path.evaluate((el) => parseFloat(getComputedStyle(el).strokeDashoffset)));
    await path.page().waitForTimeout(60);
  }
  return offsets;
}

test('an edge moves from source to target while messages cross it, and only then', async ({ page }, testInfo) => {
  test.setTimeout(90_000);
  await login(page);
  const stamp = Date.now();
  const hook = `/api/webhooks/flow-dir-${stamp}`;
  const source = await post(page, '/api/sources', {
    name: `flow-dir-src-${stamp}`, type: 'webhook', vhost: 'default', active: true, config: { path: hook },
  });
  created.sources.push(source.id);
  const sink = await post(page, '/api/sinks', { name: `flow-dir-snk-${stamp}`, type: 'stdout', vhost: 'default', config: {} });
  created.sinks.push(sink.id);
  const workflow = await post(page, '/api/workflows', {
    name: `flow-dir-wf-${stamp}`, vhost: 'default', active: false,
    nodes: [
      { id: 'n-src', type: 'source', ref_id: source.id, x: 40, y: 150, config: { label: 'Orders' } },
      { id: 'n-snk', type: 'sink', ref_id: sink.id, x: 520, y: 150, config: { label: 'Console' } },
    ],
    edges: [{ id: 'e-flow', source_id: 'n-src', target_id: 'n-snk', config: { label: '' } }],
  });
  created.workflows.push(workflow.id);

  await page.goto(`/workflows/${workflow.id}/edit`);
  const edge = page.getByTestId('rf__edge-e-flow');
  const path = edge.locator('path.react-flow__edge-path');
  const dots = edge.locator('path.hermod-edge-dots');
  // Attached, not visible: a straight edge has a zero-height box, which
  // Playwright counts as hidden.
  await expect(path).toBeAttached({ timeout: 20000 });

  // Stopped: nothing moves.
  await expect(path).toHaveAttribute('data-flow-state', 'idle');
  await expect(dots).toHaveCount(0);
  const still = await dashOffsets(path);
  expect(new Set(still).size, `stroke-dashoffset readings: ${still.join(', ')}`).toBe(1);

  const started = await apiRequest(page, `/api/workflows/${workflow.id}/toggle`, { method: 'POST' });
  expect(started.status, `the workflow did not start: ${started.body}`).toBeLessThan(400);
  await expect(path).toHaveAttribute('data-flow-state', 'quiet', { timeout: 20000 });
  await expect(dots).toHaveCount(0);

  // Ten messages a second for as long as the edge is watched.
  await page.evaluate((url) => {
    (window as any).__flowTimer = setInterval(() => {
      void fetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{"n":1}' });
    }, 100);
  }, hook);

  await expect(path).toHaveAttribute('data-flow-state', 'flowing', { timeout: 15000 });
  await expect(dots).toBeAttached();
  await expect(page.getByTestId('edge-chip-e-flow')).toHaveText(/^\d+(\.\d)?\/s$/);
  await page.screenshot({ path: testInfo.outputPath('edge-flowing.png') });
  const offsets = await dashOffsets(dots);
  const forward = offsets.slice(1).filter((o, i) => o < offsets[i]).length;
  expect(forward, `stroke-dashoffset readings: ${offsets.join(', ')}`).toBeGreaterThanOrEqual(4);

  // Messages stop: the edge stops within a few seconds.
  await page.evaluate(() => clearInterval((window as any).__flowTimer));
  await expect(path).toHaveAttribute('data-flow-state', 'quiet', { timeout: 10000 });
  await expect(dots).toHaveCount(0);
  await expect(page.getByTestId('edge-chip-e-flow')).toHaveText('0/s');

  await apiRequest(page, `/api/workflows/${workflow.id}/toggle`, { method: 'POST' });
});
