import { test, expect, type Page } from '@playwright/test';
import { login } from './support/auth';

/**
 * The encrypt and decrypt transformations, from palette to configuration.
 *
 * One editor serves both types and switches on `transType`, so the two differ
 * only in wording and in whether the failure-policy control is rendered. That
 * branch is invisible to the Go tests and to the config component's own types:
 * a registry entry pointing at the wrong component, or a palette entry whose
 * subType does not match the transformer name, produces a node that renders
 * something plausible and configures nothing. This covers that wiring.
 *
 * Relative URLs and role-based selectors on purpose, matching the other specs
 * here -- the ones that rotted did so by hardcoding ports and coordinates.
 */

// Narrower than this and the toolbar wraps, moving the panel toggle.
test.use({ viewport: { width: 1440, height: 900 } });

const openPalette = async (page: Page) => {
  await page.goto('/workflows/new');
  await page.waitForTimeout(2000);
  // The panel remembers whether it was open, so only toggle when it is shut.
  if (!(await page.getByText('Workflow Panel').isVisible().catch(() => false))) {
    await page.getByRole('button', { name: 'Workflow panel' }).click();
  }
  await expect(page.getByText('Workflow Panel')).toBeVisible();
};

// Inactive tab panels stay mounted, so a bare getByText resolves to a hidden
// copy in the Sources tab and the click times out. Scope to what is visible.
const paletteItem = (page: Page, name: string) =>
  page.locator('button:visible', { hasText: name }).first();

// One Escape is not enough: with focus in the tags combobox the first press
// closes its dropdown and leaves the drawer up, so the next tab click is
// intercepted by the drawer's portal. Press until the drawer is actually gone.
const closeDrawer = async (page: Page) => {
  const drawer = page.locator('[class*=mantine-Drawer-content], [role=dialog]').first();
  for (let i = 0; i < 4; i++) {
    if (!(await drawer.isVisible().catch(() => false))) return;
    await page.keyboard.press('Escape');
    await page.waitForTimeout(600);
  }
};

const openTransformations = async (page: Page) => {
  await page.getByRole('tab', { name: 'Transformations' }).click();
  await page.waitForTimeout(1000);
};

test('encrypt and decrypt configure from the palette', async ({ page }) => {
  await login(page);
  await openPalette(page);

  // A source has to exist before the Transformations tab unlocks.
  await paletteItem(page, 'PostgreSQL').click();
  await page.waitForTimeout(1500);
  await closeDrawer(page);

  await openTransformations(page);
  await expect(page.getByText('Encrypt Fields', { exact: true })).toBeVisible();
  await expect(page.getByText('Decrypt Fields', { exact: true })).toBeVisible();

  // Choosing a palette item adds the node and opens its configuration in one go.
  await paletteItem(page, 'Encrypt Fields').click();
  await page.waitForTimeout(2000);

  await expect(page.getByText('Fields to encrypt')).toBeVisible();
  await expect(page.getByRole('textbox', { name: 'Encryption key' })).toBeVisible();
  // The failure policy only applies to decryption and must not appear here.
  await expect(page.getByText('On decryption failure')).toHaveCount(0);

  // The controls have to be wired to node config, not merely rendered.
  await page.getByRole('combobox', { name: 'Fields' }).fill('ssn');
  await page.keyboard.press('Enter');
  await expect(page.getByText('ssn')).toBeVisible();

  await closeDrawer(page);
  await openTransformations(page);
  await paletteItem(page, 'Decrypt Fields').click();
  await page.waitForTimeout(2000);

  await expect(page.getByText('Fields to decrypt')).toBeVisible();
  await expect(page.getByText('On decryption failure')).toBeVisible();

  // The shared editor used to show encrypt's wording on the decrypt node,
  // telling operators that "encrypting every field would destroy keys" on a
  // panel that does not encrypt anything.
  await expect(page.getByText(/encrypting every field would destroy keys/)).toHaveCount(0);
  await expect(page.getByText(/List the same fields the encrypt node was given/)).toBeVisible();
});
