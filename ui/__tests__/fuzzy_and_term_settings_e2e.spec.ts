import { test, expect, type Page } from '@playwright/test';
import { login, apiRequest } from './support/auth';

/**
 * Two nodes whose editor settings did not reach them.
 *
 * Fuzzy Lookup's Options box stores JSON text; the node read only a list, so a
 * node built in the editor had no options and passed every record through
 * unmatched. Term Extraction's "Min Word Length" and "Stopwords" wrote keys the
 * node never read. All of it stayed green.
 *
 * Driven through the real editor and backend: each setting is typed into the
 * node's form, and the Live Preview is the engine's answer for it.
 */

const created: { workflows: string[]; sources: string[] } = { workflows: [], sources: [] };

test.afterEach(async ({ page }) => {
  for (const id of created.workflows.splice(0)) {
    await apiRequest(page, `/api/workflows/${id}`, { method: 'DELETE' }).catch(() => {});
  }
  for (const id of created.sources.splice(0)) {
    await apiRequest(page, `/api/sources/${id}`, { method: 'DELETE' }).catch(() => {});
  }
});

/** Saves a workflow holding one transformation node and opens that node. */
async function openNode(page: Page, name: string, config: Record<string, unknown>) {
  await login(page);
  const stamp = Date.now();
  const post = async (path: string, body: unknown) => {
    const res = await apiRequest(page, path, { method: 'POST', body });
    if (res.status >= 400) throw new Error(`fixture setup failed: POST ${path} -> ${res.status} ${res.body}`);
    return JSON.parse(res.body);
  };
  const source = await post('/api/sources', {
    name: `${name}-src-${stamp}`, type: 'webhook', vhost: 'default', active: false, config: { path: `/${name}-${stamp}` },
  });
  created.sources.push(source.id);
  const workflow = await post('/api/workflows', {
    name: `${name}-${stamp}`, vhost: 'default', active: false,
    nodes: [
      { id: 'n-src', type: 'source', ref_id: source.id, x: 100, y: 100 },
      { id: 'n-node', type: 'transformation', x: 400, y: 100, config },
    ],
    edges: [],
  });
  created.workflows.push(workflow.id);

  await page.goto(`/workflows/${workflow.id}/edit`);
  await page.waitForLoadState('networkidle');
  const nodes = page.locator('.react-flow__node');
  await expect(nodes).toHaveCount(2, { timeout: 30000 });
  await nodes.nth(1).dblclick();
}

test('options typed into a Fuzzy Lookup are what it matches against', async ({ page }) => {
  test.setTimeout(120000);
  await openNode(page, 'fuzzy-opts', { transType: 'fuzzy_lookup', lastSample: { id: 7, city: 'jakrta' }, field: 'city' });

  const options = page.getByRole('textbox', { name: 'Options (JSON Array)' });
  const preview = page.getByTestId('live-preview');
  await expect(options).toBeVisible({ timeout: 20000 });
  await expect(preview).toContainText('"city": "jakrta"', { timeout: 30000 });

  // The text the editor stores is what the node reads.
  await options.fill('["Jakarta", "Bandung"]');
  await expect(preview).toContainText('"city_fuzzy": "Jakarta"', { timeout: 30000 });

  // Text that is not a list is refused by the node, and the editor says so
  // first. It used to count as no options at all.
  await options.fill('Jakarta, Bandung');
  await expect(page.getByText(/Options must be a JSON list/)).toBeVisible();
  await expect(preview).toContainText('options must be a JSON list', { timeout: 30000 });
});

test('the word length and stop words set on a Term Extraction are the ones it uses', async ({ page }) => {
  test.setTimeout(120000);
  await openNode(page, 'term-settings', {
    transType: 'term_extraction',
    lastSample: { id: 7, note: 'The urgent refund was for an old order' },
    field: 'note',
  });

  const length = page.getByRole('textbox', { name: 'Min Word Length' });
  const preview = page.getByTestId('live-preview');
  await expect(length).toBeVisible({ timeout: 20000 });
  // Three letters and the built-in stop words, as before.
  await expect(preview).toContainText(/"note_terms": \[\s*"urgent",\s*"refund",\s*"old",\s*"order"\s*\]/, { timeout: 30000 });

  await length.fill('5');
  await expect(preview).toContainText(/"note_terms": \[\s*"urgent",\s*"refund",\s*"order"\s*\]/, { timeout: 30000 });

  const stopWords = page.getByPlaceholder('Add words to ignore');
  await stopWords.fill('Refund');
  await stopWords.press('Enter');
  await expect(preview).toContainText(/"note_terms": \[\s*"urgent",\s*"order"\s*\]/, { timeout: 30000 });
});
