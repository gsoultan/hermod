import { test, expect, type Page } from '@playwright/test';
import { E2E_USER, E2E_PASS } from './support/auth';

/**
 * A Foreach (Fan-out) node, configured the way a user configures one.
 *
 * Foreach is not a `transformation`, so it opens WorkflowNodeSettingsModal
 * rather than the config drawer, and that modal picked what to render from a
 * literal list of node types that foreach had never been added to. The panel
 * came up with a title, a Remove button and no editor at all: arrayPath is
 * required and there was no way to type it. Workflow validation then reported
 * the node as unconfigured, correctly, and with nothing the user could do.
 *
 * Every piece of this has a unit test — ForeachConfig renders, the registry
 * resolves it, the modal's predicate returns true. None of them opens the modal
 * the way clicking the palette does, which is where it was broken.
 */

const login = async (page: Page) => {
  await page.goto('/login');
  await page.getByPlaceholder('Your username').fill(E2E_USER);
  await page.getByPlaceholder('Your password').fill(E2E_PASS);
  await page.getByRole('button', { name: /sign in|login/i }).click();
  await page.waitForURL((u) => !u.pathname.startsWith('/login'), { timeout: 30000 });
};

test('a foreach node can be given its array path from the palette', async ({ page }) => {
  test.setTimeout(240000);
  await page.setViewportSize({ width: 1440, height: 900 });
  await login(page);

  await page.goto('/workflows/new', { waitUntil: 'networkidle' });
  await page.waitForTimeout(2000);

  if (!(await page.getByText('Workflow Panel').isVisible().catch(() => false))) {
    await page.getByRole('button', { name: 'Workflow panel' }).click();
  }
  await expect(page.getByText('Workflow Panel')).toBeVisible();

  // The Transformations tab stays disabled until the canvas has a source.
  await page.getByText('PostgreSQL', { exact: true }).first().click();
  await page.waitForTimeout(1500);
  await page.keyboard.press('Escape');
  await page.waitForTimeout(800);

  await page.getByRole('tab', { name: 'Transformations' }).click();
  await page.waitForTimeout(1000);
  await page.getByText('Foreach (Fan-out)', { exact: true }).first().click();
  await page.waitForTimeout(1500);

  const arrayPath = page.getByRole('textbox', { name: /array path/i });
  await expect(arrayPath, 'choosing Foreach (Fan-out) must open an editor with Array Path in it').toBeVisible();

  await arrayPath.fill('order.lines');
  await page.waitForTimeout(500);
  await expect(arrayPath).toHaveValue('order.lines');

  // The required-field warning is what the empty panel left standing for ever.
  await expect(page.getByText(/Array Path is required/i)).toBeHidden();
});
