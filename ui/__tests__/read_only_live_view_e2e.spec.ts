import { test, expect, type Page } from '@playwright/test';
import { login, apiRequest } from './support/auth';

/**
 * A flow's read-only view shows that it is running, and what it is doing.
 *
 * It used to draw its own plain cards and lines and keep nothing of the status
 * feed but "running" or "stopped": a running flow looked like a stopped one,
 * with no counts, no moving lines and no latest record to open. This runs a
 * real engine and reads the view while messages cross it.
 *
 * HERMOD_SHOTS_DIR, when set, is where it saves screenshots of each state, on
 * a desktop and a phone, in light and dark.
 */

const created: { workflows: string[]; sinks: string[]; sources: string[] } = { workflows: [], sinks: [], sources: [] };
const shots = process.env.HERMOD_SHOTS_DIR;
let feeder: ReturnType<typeof setInterval> | undefined;

test.afterEach(async ({ page }) => {
  clearInterval(feeder);
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

/** Each colour scheme in turn: reloads in it, waits for `ready`, and saves the page. */
async function shoot(page: Page, name: string, ready: () => Promise<void>) {
  if (!shots) return;
  for (const scheme of ['light', 'dark'] as const) {
    await page.evaluate((s) => localStorage.setItem('mantine-color-scheme-value', s), scheme);
    await page.reload();
    await ready();
    await page.waitForTimeout(1500);
    await page.screenshot({ path: `${shots}/${name}-${scheme}.png` });
  }
}

test('the read-only view says a flow is live and shows what each step is doing', async ({ page }) => {
  test.setTimeout(120_000);
  await login(page);
  const stamp = Date.now();
  const hook = `/api/webhooks/ro-live-${stamp}`;
  const source = await post(page, '/api/sources', {
    name: `Orders webhook ${stamp}`, type: 'webhook', vhost: 'default', active: true, config: { path: hook },
  });
  created.sources.push(source.id);
  const sink = await post(page, '/api/sinks', { name: `Console ${stamp}`, type: 'stdout', vhost: 'default', config: {} });
  created.sinks.push(sink.id);
  const workflow = await post(page, '/api/workflows', {
    name: `Order intake ${stamp}`, vhost: 'default', active: false,
    nodes: [
      { id: 'n-src', type: 'source', ref_id: source.id, x: 40, y: 150, config: { label: 'Orders' } },
      { id: 'n-map', type: 'transformation', x: 360, y: 150, config: { label: 'Map order', transType: 'mapping' } },
      { id: 'n-snk', type: 'sink', ref_id: sink.id, x: 680, y: 150, config: { label: 'Console' } },
    ],
    edges: [
      { id: 'e-in', source_id: 'n-src', target_id: 'n-map', config: {} },
      { id: 'e-out', source_id: 'n-map', target_id: 'n-snk', config: {} },
    ],
  });
  created.workflows.push(workflow.id);

  for (const viewport of [{ name: 'desktop', width: 1280, height: 800 }, { name: 'phone', width: 390, height: 844 }]) {
    await page.setViewportSize({ width: viewport.width, height: viewport.height });
    await page.goto(`/workflows/${workflow.id}`);
    const note = page.getByTestId('flow-live-note');
    await expect(note).toHaveAttribute('data-state', 'stopped', { timeout: 20000 });
    await expect(note).toContainText(viewport.name === 'phone' ? 'Stopped' : 'Turn it on to see live data');
    await shoot(page, `${viewport.name}-stopped`, () => expect(note).toHaveAttribute('data-state', 'stopped', { timeout: 20000 }));
  }

  await page.setViewportSize({ width: 1280, height: 800 });
  const started = await apiRequest(page, `/api/workflows/${workflow.id}/toggle`, { method: 'POST' });
  expect(started.status, `the workflow did not start: ${started.body}`).toBeLessThan(400);

  // Five messages a second for as long as the view is watched. Sent from the
  // test rather than the page, which reloads.
  let n = 4200;
  feeder = setInterval(() => {
    n += 1;
    void page.context().request.post(hook, { data: { order_id: n, region: 'eu', total: 129.5 } }).catch(() => {});
  }, 200);

  for (const viewport of [{ name: 'desktop', width: 1280, height: 800 }, { name: 'phone', width: 390, height: 844 }]) {
    await page.setViewportSize({ width: viewport.width, height: viewport.height });
    await page.goto(`/workflows/${workflow.id}`);
    const note = page.getByTestId('flow-live-note');
    await expect(note).toHaveAttribute('data-state', 'live', { timeout: 20000 });

    // Each step says what it has done, and shows its latest record behind the eye.
    const sinkCard = page.getByTestId('rf__node-n-snk');
    await expect(sinkCard.getByText(/processed$/)).toBeVisible({ timeout: 20000 });
    await expect(sinkCard.getByRole('button', { name: /latest record/i })).toContainText('order_id');

    // The lines move, at the rate messages cross them.
    const line = page.getByTestId('rf__edge-e-out').locator('path.react-flow__edge-path');
    await expect(line).toHaveAttribute('data-flow-state', 'flowing', { timeout: 20000 });
    await expect(page.getByTestId('edge-chip-e-out')).toHaveText(/^\d+(\.\d)?\/s$/);
    const liveReady = async () => {
      await expect(note).toHaveAttribute('data-state', 'live', { timeout: 20000 });
      await expect(line).toHaveAttribute('data-flow-state', 'flowing', { timeout: 20000 });
    };
    await shoot(page, `${viewport.name}-live`, liveReady);

    // The eye opens the record in full.
    await sinkCard.getByRole('button', { name: /latest record/i }).click();
    const inspector = page.getByRole('dialog', { name: 'Latest record' });
    await expect(inspector).toBeVisible();
    await expect(inspector.getByRole('textbox', { name: 'Record' })).toHaveValue(/"order_id"/);
    await shoot(page, `${viewport.name}-record`, async () => {
      await liveReady();
      await sinkCard.getByRole('button', { name: /latest record/i }).click();
      await expect(inspector).toBeVisible();
    });
    await inspector.getByRole('button', { name: 'Close' }).click();
  }

  // Nothing on the view edits the flow.
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.getByTestId('rf__node-n-map').hover();
  await expect(page.getByRole('button', { name: 'Remove node' })).toHaveCount(0);

  clearInterval(feeder);
  await apiRequest(page, `/api/workflows/${workflow.id}/toggle`, { method: 'POST' });
});
