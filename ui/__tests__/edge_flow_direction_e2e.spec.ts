import { test, expect, type Page } from '@playwright/test';
import { login, apiRequest } from './support/auth';

/**
 * Data Pulse animates every edge so data visibly flows through the workflow. It
 * flowed backwards. The keyframe raised stroke-dashoffset, and a larger offset
 * starts the dash pattern further along, so the dashes slid toward the start of
 * the path: from the target back to the source.
 *
 * Direction cannot be seen in jsdom, which runs no animations, so this reads the
 * running offset from a real browser: it has to fall for the dashes to travel
 * from source to target.
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

test('the Data Pulse on an edge travels from source to target', async ({ page }) => {
  await login(page);
  const stamp = Date.now();
  const source = await post(page, '/api/sources', {
    name: `flow-dir-src-${stamp}`, type: 'webhook', vhost: 'default', active: true, config: { path: `/flow-dir-${stamp}` },
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
  const path = page.getByTestId('rf__edge-e-flow').locator('path.react-flow__edge-path');
  // Attached, not visible: a straight edge has a zero-height box, which
  // Playwright counts as hidden.
  await expect(path).toBeAttached({ timeout: 20000 });

  // Six readings, 60ms apart. Each loop of the animation ends by jumping back to
  // where it started, so at most one step can run the wrong way.
  const offsets: number[] = [];
  for (let i = 0; i < 6; i++) {
    offsets.push(await path.evaluate((el) => parseFloat(getComputedStyle(el).strokeDashoffset)));
    await page.waitForTimeout(60);
  }
  const forward = offsets.slice(1).filter((o, i) => o < offsets[i]).length;
  expect(forward, `stroke-dashoffset readings: ${offsets.join(', ')}`).toBeGreaterThanOrEqual(4);
});
