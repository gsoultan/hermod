import { test, expect } from '@playwright/test';
import { login, apiRequest } from './support/auth';

/**
 * A function is picked into a Set Fields value, and the backend runs it.
 *
 * The engine has always evaluated lower(), now() and the rest in a Set Fields
 * value, but the editor never offered them there: the only list sat beside the
 * Formulas node, held 13 of the 33, and added a new row rather than writing
 * into the value at hand. So the functions were typed from memory, and a guess
 * such as time.now() is not an error -- it is written out as that text.
 *
 * Driven from storage through the real editor: the node is saved, opened, a
 * function is picked into each row, and the Live Preview is the backend's
 * answer for what was written.
 */

const ROW = {
  operation: 'snapshot',
  table: 'customers',
  after: { id: 7, email: 'Ada@Example.COM', city: 'Jakarta' },
};

const created: { workflows: string[]; sources: string[] } = { workflows: [], sources: [] };

test.afterEach(async ({ page }) => {
  for (const id of created.workflows.splice(0)) {
    await apiRequest(page, `/api/workflows/${id}`, { method: 'DELETE' }).catch(() => {});
  }
  for (const id of created.sources.splice(0)) {
    await apiRequest(page, `/api/sources/${id}`, { method: 'DELETE' }).catch(() => {});
  }
});

test('a function picked into a Set Fields value is what the preview runs', async ({ page }) => {
  test.setTimeout(120000);
  await login(page);
  const stamp = Date.now();
  const post = async (path: string, body: unknown) => {
    const res = await apiRequest(page, path, { method: 'POST', body });
    if (res.status >= 400) throw new Error(`fixture setup failed: POST ${path} -> ${res.status} ${res.body}`);
    return JSON.parse(res.body);
  };

  const source = await post('/api/sources', {
    name: `fn-picker-src-${stamp}`, type: 'webhook', vhost: 'default', active: false,
    config: { path: `/fn-picker-${stamp}` },
  });
  created.sources.push(source.id);
  const workflow = await post('/api/workflows', {
    name: `fn-picker-${stamp}`, vhost: 'default', active: false,
    nodes: [
      { id: 'n-src', type: 'source', ref_id: source.id, x: 100, y: 100 },
      {
        id: 'n-set', type: 'transformation', x: 400, y: 100,
        config: {
          transType: 'set', lastSample: ROW,
          // The row "+" beside a field makes, and one nobody has filled in.
          'column.after.email': 'source.after.email',
          'column.after.note': '',
        },
      },
    ],
    edges: [],
  });
  created.workflows.push(workflow.id);

  await page.goto(`/workflows/${workflow.id}/edit`);
  await page.waitForLoadState('networkidle');
  const nodes = page.locator('.react-flow__node');
  await expect(nodes).toHaveCount(2, { timeout: 30000 });
  await nodes.nth(1).dblclick();

  const values = page.getByRole('textbox', { name: 'Value or expression' });
  await expect(values).toHaveCount(2, { timeout: 20000 });
  await expect(values.nth(0)).toHaveValue('source.after.email');

  const preview = page.getByTestId('live-preview');
  const insert = page.getByRole('button', { name: 'Insert function' });
  // The library beside the rows has a search box of the same name.
  const picker = page.locator('.mantine-Popover-dropdown').filter({ has: page.getByRole('textbox', { name: 'Search functions' }) });

  // Found by what it does, and applied to the value the row already holds.
  await insert.nth(0).click();
  await picker.getByRole('textbox', { name: 'Search functions' }).fill('lowercase');
  await picker.getByRole('button', { name: 'Insert lower(text)' }).click();
  await expect(values.nth(0)).toHaveValue('lower(source.after.email)');
  await expect(preview).toContainText('"email": "ada@example.com"', { timeout: 30000 });

  // An empty value gets the call with its first placeholder selected, so the
  // variable picked next becomes the argument.
  await insert.nth(1).click();
  await picker.getByRole('textbox', { name: 'Search functions' }).fill('default');
  await picker.getByRole('button', { name: 'Insert coalesce(a, b, ...)' }).click();
  await expect(values.nth(1)).toHaveValue("coalesce(value, 'default')");
  await page.getByRole('button', { name: 'Insert variable' }).nth(1).click();
  await page.locator('.mantine-Popover-dropdown').getByText('after.city', { exact: true }).click();
  await expect(values.nth(1)).toHaveValue("coalesce(source.after.city, 'default')");
  await expect(preview).toContainText('"note": "Jakarta"', { timeout: 30000 });

  // The spelling people guess finds the function that exists -- and Escape
  // closes the list, not the node's settings behind it.
  await insert.nth(1).click();
  await picker.getByRole('textbox', { name: 'Search functions' }).fill('time.now');
  await expect(picker.getByRole('button', { name: 'Insert now()' })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(picker).toBeHidden();
  // Long enough for a closing drawer to have gone; the edit and the preview
  // below need it open as well.
  await page.waitForTimeout(600);
  await expect(values).toHaveCount(2);

  // The guess itself is written out as text, and the row says so.
  await values.nth(1).fill('time.now()');
  await expect(page.getByText(/time\.now is not a function/)).toBeVisible();
  await expect(preview).toContainText('"note": "time.now()"', { timeout: 30000 });

  // The whole list is beside the rows of a Set Fields node, not only Formulas.
  const library = page.getByRole('region', { name: 'Function library' });
  await expect(library.getByRole('button', { name: 'Insert uuid()' })).toBeVisible();
});
