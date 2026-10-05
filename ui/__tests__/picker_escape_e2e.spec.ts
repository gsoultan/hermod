import { test, expect, type Page } from '@playwright/test';
import { login, apiRequest } from './support/auth';

/**
 * Escape in a picker closes the picker, not the node's settings behind it.
 *
 * A node is configured in a drawer, and "Insert variable" and "Insert function"
 * open a list over it. Mantine's modal listens for Escape on the window and
 * closes unless the key's target carries data-mantine-stop-propagation. So
 * pressing Escape to dismiss the list closed the whole drawer: with the focus
 * still on the button that opened it, in the list's search box, or on nothing
 * after a click on the list's own text.
 *
 * Only a browser sees this. jsdom keeps the drawer "visible" for its closing
 * transition, and the first sign in a spec was a Live Preview that had stopped
 * updating -- so each case waits out the transition and then edits the value.
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

async function openSetFields(page: Page) {
  await login(page);
  const stamp = Date.now();
  const post = async (path: string, body: unknown) => {
    const res = await apiRequest(page, path, { method: 'POST', body });
    if (res.status >= 400) throw new Error(`fixture setup failed: POST ${path} -> ${res.status} ${res.body}`);
    return JSON.parse(res.body);
  };
  const source = await post('/api/sources', {
    name: `escape-src-${stamp}`, type: 'webhook', vhost: 'default', active: false, config: { path: `/escape-${stamp}` },
  });
  created.sources.push(source.id);
  const workflow = await post('/api/workflows', {
    name: `escape-${stamp}`, vhost: 'default', active: false,
    nodes: [
      { id: 'n-src', type: 'source', ref_id: source.id, x: 100, y: 100 },
      {
        id: 'n-set', type: 'transformation', x: 400, y: 100,
        config: { transType: 'set', lastSample: { id: 7, city: 'Jakarta' }, 'column.note': 'source.city' },
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
  await expect(page.getByRole('textbox', { name: 'Value or expression' })).toHaveValue('source.city', { timeout: 20000 });
}

/** The drawer is still there, and still editing the node. */
async function expectSettingsStillOpen(page: Page, marker: string) {
  // Long enough for a closing drawer to have gone.
  await page.waitForTimeout(600);
  const value = page.getByRole('textbox', { name: 'Value or expression' });
  await expect(value).toHaveCount(1);
  await value.fill(`'${marker}'`);
  await expect(page.getByTestId('live-preview')).toContainText(`"note": "${marker}"`, { timeout: 30000 });
}

const variables = (page: Page) =>
  page.locator('.mantine-Popover-dropdown').filter({ has: page.getByPlaceholder('Search fields...') });
const functions = (page: Page) =>
  page.locator('.mantine-Popover-dropdown').filter({ has: page.getByRole('textbox', { name: 'Search functions' }) });

test('Escape right after opening Insert variable', async ({ page }) => {
  test.setTimeout(120000);
  await openSetFields(page);

  await page.getByRole('button', { name: 'Insert variable' }).click();
  await expect(variables(page)).toBeVisible();
  await page.keyboard.press('Escape');

  await expect(variables(page)).toBeHidden();
  await expectSettingsStillOpen(page, 'after-open');
});

test('Escape from the search box of Insert variable', async ({ page }) => {
  test.setTimeout(120000);
  await openSetFields(page);

  await page.getByRole('button', { name: 'Insert variable' }).click();
  await variables(page).getByPlaceholder('Search fields...').fill('ci');
  await page.keyboard.press('Escape');

  await expect(variables(page)).toBeHidden();
  await expectSettingsStillOpen(page, 'after-search');
});

// A click on the list's own text puts the focus on nothing in particular.
test('Escape after a click on the text of Insert variable', async ({ page }) => {
  test.setTimeout(120000);
  await openSetFields(page);

  await page.getByRole('button', { name: 'Insert variable' }).click();
  await variables(page).getByText(/Tip: Click a field/).click();
  await page.keyboard.press('Escape');

  await expect(variables(page)).toBeHidden();
  await expectSettingsStillOpen(page, 'after-text');
});

test('Escape after a click on the text of Insert function', async ({ page }) => {
  test.setTimeout(120000);
  await openSetFields(page);

  await page.getByRole('button', { name: 'Insert function' }).click();
  await functions(page).getByText('Date & time', { exact: true }).click();
  await page.keyboard.press('Escape');

  await expect(functions(page)).toBeHidden();
  await expectSettingsStillOpen(page, 'after-function-text');
});
