import { test, expect } from '@playwright/test';
import { login, apiRequest } from './support/auth';

/**
 * A function applied to a Mapping node's field, and where its result goes.
 *
 * Mapping, Data Conversion, Aggregate, Fuzzy Lookup and Term Extraction take
 * "a field or an expression" and wrote the result to a field named after it.
 * For lower(source.status) that name is the expression's own text, which the
 * message splits at its dot: the mapped value landed under
 * {"lower(source": {"status)": ...}} and the node stayed green. Mapping had no
 * Target Field to set either, so in the editor a function there could not be
 * made to work at all.
 *
 * Driven from storage through the real editor and backend: the workflow is
 * saved, the node opened, a function picked into its field, and the Live
 * Preview is the engine's answer each time.
 */

const ROW = { id: 7, status: 'PAID' };
const MAPPING = JSON.stringify({ paid: 'Paid in full', PAID: 'Shouted' });

const created: { workflows: string[]; sources: string[] } = { workflows: [], sources: [] };

test.afterEach(async ({ page }) => {
  for (const id of created.workflows.splice(0)) {
    await apiRequest(page, `/api/workflows/${id}`, { method: 'DELETE' }).catch(() => {});
  }
  for (const id of created.sources.splice(0)) {
    await apiRequest(page, `/api/sources/${id}`, { method: 'DELETE' }).catch(() => {});
  }
});

test('a function on a Mapping field keeps its result in a real field', async ({ page }) => {
  test.setTimeout(120000);
  await login(page);
  const stamp = Date.now();

  const sourceRes = await apiRequest(page, '/api/sources', {
    method: 'POST',
    body: { name: `map-fn-src-${stamp}`, type: 'webhook', vhost: 'default', active: false, config: { path: `/map-fn-${stamp}` } },
  });
  expect(sourceRes.status, sourceRes.body).toBeLessThan(400);
  const source = JSON.parse(sourceRes.body);
  created.sources.push(source.id);

  const workflowWith = (config: Record<string, unknown>) => ({
    name: `map-fn-${stamp}`, vhost: 'default', active: false,
    nodes: [
      { id: 'n-src', type: 'source', ref_id: source.id, x: 100, y: 100 },
      { id: 'n-map', type: 'transformation', x: 400, y: 100, config: { transType: 'mapping', lastSample: ROW, mapping: MAPPING, ...config } },
    ],
    edges: [],
  });

  // A call with nowhere to write is refused when the workflow is saved, not
  // on its first message.
  const refused = await apiRequest(page, '/api/workflows', { method: 'POST', body: workflowWith({ field: 'lower(source.status)' }) });
  expect(refused.status).toBe(400);
  expect(refused.body).toContain('target field');

  const saved = await apiRequest(page, '/api/workflows', { method: 'POST', body: workflowWith({ field: 'status' }) });
  expect(saved.status, saved.body).toBeLessThan(400);
  const workflow = JSON.parse(saved.body);
  created.workflows.push(workflow.id);

  await page.goto(`/workflows/${workflow.id}/edit`);
  await page.waitForLoadState('networkidle');
  const nodes = page.locator('.react-flow__node');
  await expect(nodes).toHaveCount(2, { timeout: 30000 });
  await nodes.nth(1).dblclick();

  const field = page.getByRole('combobox', { name: 'Source Field' });
  const target = page.getByRole('textbox', { name: 'Target Field' });
  const preview = page.getByTestId('live-preview');
  await expect(field).toHaveValue('status', { timeout: 20000 });
  await expect(preview).toContainText('"status": "Shouted"', { timeout: 30000 });

  // Applying a function changes what is read, not where it is written: the
  // target becomes the field the node was writing to.
  await page.getByRole('button', { name: 'Insert function' }).first().click();
  const picker = page.locator('.mantine-Popover-dropdown').filter({ has: page.getByRole('textbox', { name: 'Search functions' }) });
  await picker.getByRole('textbox', { name: 'Search functions' }).fill('lowercase');
  await picker.getByRole('button', { name: 'Insert lower(text)' }).click();
  await expect(field).toHaveValue('lower(source.status)');
  await expect(target).toHaveValue('status');
  await expect(preview).toContainText('"status": "Paid in full"', { timeout: 30000 });
  await expect(preview).not.toContainText('lower(source');

  // With no target the node has nowhere to write: the editor says so beside
  // the field that fixes it, and the engine refuses rather than invent one.
  await target.fill('');
  await expect(page.getByText(/no field of its own/)).toBeVisible();
  await expect(preview).toContainText('set a target field', { timeout: 30000 });

  await target.fill('status_label');
  await expect(preview).toContainText('"status_label": "Paid in full"', { timeout: 30000 });
  await expect(preview).toContainText('"status": "PAID"');
});
