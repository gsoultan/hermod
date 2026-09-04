import { test, expect, type Page } from '@playwright/test';
import { login, apiRequest } from './support/auth';

/**
 * Guards for behaviour that was measured, fixed, and would otherwise rot.
 *
 * Each of these was a real defect with a number attached. The component tests
 * that accompanied the fixes prove the mechanism; these prove the outcome in
 * the assembled app, which is where a regression would actually be felt.
 */

const createdWorkflows: string[] = [];

test.afterEach(async ({ page }) => {
  for (const id of createdWorkflows.splice(0)) {
    await apiRequest(page, `/api/workflows/${id}`, { method: 'DELETE' }).catch(() => {});
  }
});

/** Minimal source -> transformation -> sink workflow, via the API. */
async function createWorkflow(page: Page, name: string): Promise<string> {
  // The source carries a sample. Without one the transformation node has no
  // incoming payload and the preview path never runs at all — in which case the
  // "does not poll" test below would pass against the original bug too, since
  // the old loop also bailed out with nothing to preview. The sample is what
  // makes the assertion mean something.
  const src = await apiRequest(page, '/api/sources', {
    method: 'POST',
    body: {
      name: `${name}-src`,
      type: 'webhook',
      config: {},
      vhost: 'default',
      sample: JSON.stringify({ id: 1, email: 'ada@example.com', status: 'active' }),
    },
  });
  const snk = await apiRequest(page, '/api/sinks', {
    method: 'POST',
    body: { name: `${name}-snk`, type: 'stdout', config: {}, vhost: 'default' },
  });
  if (src.status >= 400 || snk.status >= 400) {
    throw new Error(`fixture setup failed: source ${src.status} ${src.body}, sink ${snk.status} ${snk.body}`);
  }
  const wf = await apiRequest(page, '/api/workflows', {
    method: 'POST',
    body: {
      name,
      vhost: 'default',
      active: false,
      nodes: [
        { id: 'n-src', type: 'source', ref_id: JSON.parse(src.body).id, x: 100, y: 100 },
        { id: 'n-tx', type: 'transformation', config: { transType: 'set', 'column.tagged': "'yes'" }, x: 320, y: 100 },
        { id: 'n-snk', type: 'sink', ref_id: JSON.parse(snk.body).id, x: 540, y: 100 },
      ],
      edges: [
        { id: 'e1', source_id: 'n-src', target_id: 'n-tx' },
        { id: 'e2', source_id: 'n-tx', target_id: 'n-snk' },
      ],
    },
  });
  if (wf.status >= 400) throw new Error(`workflow create failed: ${wf.status} ${wf.body}`);
  const id = JSON.parse(wf.body).id;
  createdWorkflows.push(id);
  return id;
}

test.describe('performance guards', () => {
  /**
   * The app renders dark by default but used to apply the theme only after
   * hydration, so every cold load painted white first. The colour now comes
   * from index.html itself, before any script runs.
   */
  test('first paint is the dark theme, not a white flash', async ({ page }) => {
    await page.goto('/login', { waitUntil: 'commit' });
    const [background, scheme] = await page.evaluate(() => [
      getComputedStyle(document.documentElement).backgroundColor,
      document.documentElement.getAttribute('data-mantine-color-scheme'),
    ]);
    expect(scheme).toBe('dark');
    expect(background).toBe('rgb(26, 27, 30)');
  });

  /**
   * The transformation preview used to re-run once a second with the user idle:
   * a debounce that re-armed on every render behaved as a poll. Measured at four
   * requests in 5.6 seconds of nothing. It must fire once when the node opens
   * and then stay quiet.
   */
  test('an open transformation node does not poll the preview endpoint', async ({ page }) => {
    test.setTimeout(90000);
    await login(page);
    const workflowID = await createWorkflow(page, `e2e-perf-${Date.now()}`);

    const previews: number[] = [];
    page.on('request', (r) => {
      if (r.url().includes('/api/transformations/test')) previews.push(Date.now());
    });

    await page.goto(`/workflows/${workflowID}/edit`);
    await expect(page.locator('.react-flow').first()).toBeVisible({ timeout: 30000 });

    const node = page.locator('.react-flow__node').filter({ hasText: /transform|set/i }).first();
    await expect(node).toBeVisible({ timeout: 20000 });
    await node.dblclick();
    const drawer = page.getByRole('dialog').or(page.locator('.mantine-Drawer-content'));
    await expect(drawer.first()).toBeVisible({ timeout: 20000 });

    // Exactly one preview goes out when the node opens — proof the path is live
    // and this test is exercising it, not skipping it.
    await expect.poll(() => previews.length, { timeout: 5000 }).toBe(1);

    // Then six seconds of nothing. The old loop would have added ~6 here.
    await page.waitForTimeout(6000);
    expect(previews.length, 'preview requests after opening and idling').toBe(1);
  });

  /**
   * Every list used to mint a query key per keystroke. Typing eight characters
   * in the Sources search box was eight requests and eight table blank-outs.
   */
  test('typing in a list search issues one request, not one per keystroke', async ({ page }) => {
    await login(page);
    await page.goto('/sources');
    const box = page.getByPlaceholder(/search sources/i);
    await expect(box).toBeVisible({ timeout: 20000 });
    await page.waitForLoadState('networkidle');

    let requests = 0;
    page.on('request', (r) => {
      if (/\/api\/sources\?/.test(r.url())) requests++;
    });

    await box.pressSequentially('abcdefgh', { delay: 40 });
    await page.waitForTimeout(1000);

    // One after the 300ms debounce settles; a second is tolerated if the typing
    // straddled a debounce window. Eight is the bug.
    expect(requests).toBeLessThanOrEqual(2);
  });
});
