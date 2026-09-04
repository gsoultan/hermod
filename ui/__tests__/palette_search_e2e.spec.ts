import { test, expect, type Page } from '@playwright/test';
import { E2E_USER, E2E_PASS } from './support/auth';

/**
 * The workflow panel's palette search.
 *
 * The palette lists well over a hundred sources, sinks and transformations
 * across three tabs, and finding one meant scrolling until you recognised it.
 * The matching rules are unit-tested in paletteSearch.test.ts; this covers the
 * wiring those tests cannot see -- that the box filters the list it is sitting
 * above, and that a tab with no matches says where the matches are instead of
 * showing an empty column.
 *
 * Relative URLs and role-based selectors on purpose: the specs that rotted
 * before this one did so by hardcoding localhost:5175 and clicking fixed
 * coordinates.
 */

// Narrower than this and the toolbar wraps, moving the panel toggle.
test.use({ viewport: { width: 1440, height: 900 } });

const login = async (page: Page) => {
  await page.goto('/login');
  await page.getByPlaceholder('Your username').fill(E2E_USER);
  await page.getByPlaceholder('Your password').fill(E2E_PASS);
  await page.getByRole('button', { name: /sign in|login/i }).click();
  await page.waitForURL((u) => !u.pathname.startsWith('/login'), { timeout: 30000 });
};

const openPalette = async (page: Page) => {
  await page.goto('/workflows/new');
  await page.waitForTimeout(2000);
  // The panel remembers whether it was open, so only toggle when it is shut.
  if (!(await page.getByText('Workflow Panel').isVisible().catch(() => false))) {
    await page.getByRole('button', { name: 'Workflow panel' }).click();
  }
  await expect(page.getByText('Workflow Panel')).toBeVisible();
};

test('palette search filters the list and points at other tabs', async ({ page }) => {
  await login(page);
  await openPalette(page);

  const search = page.getByLabel('Search the node palette');
  await expect(search).toBeVisible();

  // A term that exists in the open tab narrows the list to it.
  await search.fill('postgres');
  await expect(page.getByText('PostgreSQL', { exact: false }).first()).toBeVisible();
  // Categories that match nothing are gone rather than left as empty headings.
  await expect(page.getByText('FILES', { exact: true })).toHaveCount(0);

  // A term whose matches live in another tab says so, rather than showing an
  // empty panel and leaving the user to guess which tab to try.
  await search.fill('mapping');
  await expect(page.getByText(/Nothing here matches/)).toBeVisible();
  await expect(page.getByText(/in Transformations/)).toBeVisible();

  // This workflow has no source yet, so Transformations is locked and switching
  // to it would bounce straight back. The hint has to say that rather than
  // render a link that does nothing.
  await expect(page.getByText(/add a source first/)).toBeVisible();
  await expect(page.getByRole('button', { name: /in Transformations/ })).toHaveCount(0);

  // A term that matches nowhere says so without offering a tab.
  await search.fill('zzzznotathing');
  await expect(page.getByText(/Nothing here matches/)).toBeVisible();
  await expect(page.getByText(/in Sources|in Sinks|in Transformations/)).toHaveCount(0);

  // Clearing restores the full palette.
  await page.getByRole('button', { name: 'Clear search' }).click();
  await expect(search).toHaveValue('');
  await expect(page.getByText(/Nothing here matches/)).toHaveCount(0);
});
