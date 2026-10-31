import { test, expect, type Page } from '@playwright/test';

/**
 * Regression guard: a flow's Traces tab must scroll a long message journey.
 *
 * The tab was laid out with Mantine's Grid at h="100%". Grid puts its columns
 * in an inner wrapper that has no height of its own, so the columns' 100% had
 * nothing to resolve against: the journey's scroll area grew to the full
 * height of its content (over 3000px for an eight-step trace) and the panel's
 * overflow: hidden cut it off. Nothing scrolled, and every step past the
 * first two was out of reach — the same for the list of traces.
 *
 * The API is answered here rather than by the stack: the bug is pure layout,
 * and a trace long enough to overflow is easier to state than to produce.
 * The assertions measure the rendered scroll areas, not CSS.
 */

const STEPS = Array.from({ length: 8 }, (_, i) => ({
  node_id: `n${i}`,
  timestamp: new Date(Date.UTC(2026, 9, 31, 6, 0, i)).toISOString(),
  duration_ms: 3 + i,
  before: { id: i, name: `order-${i}`, amount: 100 + i, items: [1, 2, 3] },
  after: { id: i, name: `order-${i}`, amount: 100 + i, status: 'ok' },
}));

const WORKFLOW = {
  id: 'wf-journey',
  name: 'Journey scroll',
  active: false,
  vhost: 'default',
  nodes: STEPS.map((s, i) => ({ id: s.node_id, type: 'transformation', config: { label: `Step ${i + 1}` }, x: i * 200, y: 100 })),
  edges: [],
};

const TRACES = Array.from({ length: 30 }, (_, i) => ({
  message_id: `msg-${1000 + i}`,
  created_at: new Date(Date.UTC(2026, 9, 31, 6, i)).toISOString(),
}));

const answer = (url: string): unknown => {
  const path = new URL(url).pathname;
  if (path === '/api/config/status') return { configured: true, user_setup: true };
  if (path === '/api/me') return { id: 'u1', username: 'admin', role: 'Administrator', vhosts: [] };
  if (path === `/api/workflows/${WORKFLOW.id}`) return WORKFLOW;
  if (path === `/api/workflows/${WORKFLOW.id}/traces`) return TRACES;
  if (path === `/api/workflows/${WORKFLOW.id}/traces/`) return { message_id: TRACES[0].message_id, steps: STEPS };
  return [];
};

const openJourney = async (page: Page) => {
  await page.route('**/api/**', (route) => route.fulfill({ json: answer(route.request().url()) }));
  await page.goto(`/workflows/${WORKFLOW.id}?tab=traces`);
  await page.getByText(TRACES[0].message_id, { exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Message Journey' })).toBeVisible();
};

/** The scroll viewport that holds `text`, measured, then scrolled to the end. */
const scrollViewportOf = (page: Page, text: string) =>
  page.evaluate((label) => {
    const anchor = [...document.querySelectorAll('h4, h5, p')].find((e) => e.textContent === label);
    const viewport = anchor?.closest('.mantine-ScrollArea-viewport') as HTMLElement | null;
    if (!viewport) return null;
    const rect = viewport.getBoundingClientRect();
    viewport.scrollTop = viewport.scrollHeight;
    return {
      clientHeight: viewport.clientHeight,
      scrollHeight: viewport.scrollHeight,
      scrollTop: viewport.scrollTop,
      bottom: rect.bottom,
      width: rect.width,
    };
  }, text);

for (const vp of [
  { name: 'desktop', width: 1440, height: 900 },
  { name: 'phone', width: 390, height: 844 },
]) {
  test.describe(`trace journey on ${vp.name}`, () => {
    test.use({ viewport: { width: vp.width, height: vp.height } });

    test('the message journey scrolls within the screen', async ({ page }) => {
      await openJourney(page);
      const journey = await scrollViewportOf(page, 'Message Journey');
      expect(journey, 'journey is not inside a scroll area').not.toBeNull();
      // Bounded by the screen, so the content overflows it and it scrolls.
      expect(journey!.bottom).toBeLessThanOrEqual(vp.height);
      expect(journey!.scrollHeight).toBeGreaterThan(journey!.clientHeight);
      expect(journey!.scrollTop).toBeGreaterThan(0);
      // The last step can be brought into view.
      const last = page.getByText('Node: Step 8');
      await last.scrollIntoViewIfNeeded();
      await expect(last).toBeInViewport();
    });

    test('the journey has room to read on this screen', async ({ page }) => {
      await openJourney(page);
      const journey = await scrollViewportOf(page, 'Message Journey');
      // On a phone the journey takes the width, less the page's gutters; the
      // trace list was a 4/12 column there, which left the journey 210px.
      expect(journey!.width).toBeGreaterThanOrEqual(vp.name === 'phone' ? vp.width - 60 : 800);
    });

    test('the list of traces scrolls within the screen', async ({ page }) => {
      await page.route('**/api/**', (route) => route.fulfill({ json: answer(route.request().url()) }));
      await page.goto(`/workflows/${WORKFLOW.id}?tab=traces`);
      await expect(page.getByText(TRACES[0].message_id, { exact: true })).toBeVisible();
      const list = await scrollViewportOf(page, TRACES[0].message_id);
      expect(list, 'trace list is not inside a scroll area').not.toBeNull();
      expect(list!.bottom).toBeLessThanOrEqual(vp.height);
      expect(list!.scrollTop).toBeGreaterThan(0);
      const last = page.getByText(TRACES[TRACES.length - 1].message_id, { exact: true });
      await last.scrollIntoViewIfNeeded();
      await expect(last).toBeInViewport();
    });
  });
}
