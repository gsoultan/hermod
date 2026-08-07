import { test, expect, type Page } from '@playwright/test';
import { login, apiRequest } from './support/auth';

/**
 * A required Select must not clear itself when the user clicks the option that
 * is already chosen.
 *
 * Mantine's Select enables `allowDeselect` by default, so clicking the current
 * value deselects it. On a required field that is never the user's intent, and
 * here it was actively deceptive: the source form defaults Type to "postgres",
 * so opening the dropdown to confirm the choice and clicking "PostgreSQL"
 * emptied it — while the searchable input went on displaying "PostgreSQL". The
 * form looked complete and Next Step stayed disabled with
 * "Required: Select a type", pointing at a field that visibly had a value.
 *
 * This lives in Playwright rather than jsdom on purpose: the behaviour is
 * Mantine's real combobox interaction, which jsdom does not reproduce.
 */
async function startAddSource(page: Page, vhostName: string) {
  await page.goto('/sources');
  await page.getByRole('button', { name: 'Add Source' }).click();
  await page.getByLabel('Name').fill(`Select regression ${Date.now()}`);
  await page.getByRole('combobox', { name: 'VHost', exact: true }).click();
  await page.getByRole('option', { name: vhostName }).click();
}

test.describe('required Selects do not deselect on re-click', () => {
  let vhostName: string;

  test.beforeEach(async ({ page }) => {
    await login(page);
    vhostName = `vh_sel_${Date.now()}`;
    await apiRequest(page, '/api/vhosts', {
      method: 'POST',
      body: { name: vhostName, description: 'select regression' },
    });
  });

  test('re-clicking the already-selected Type keeps the form valid', async ({ page }) => {
    test.setTimeout(120000);
    await startAddSource(page, vhostName);

    // Type defaults to postgres, so this click lands on the current value.
    await page.getByRole('combobox', { name: 'Type', exact: true }).click();
    await page.getByRole('option', { name: 'PostgreSQL', exact: true }).click();

    const next = page.getByRole('button', { name: 'Next Step' });
    await expect(
      next,
      'clicking the already-selected Type cleared it; the wizard cannot be advanced'
    ).toBeEnabled({ timeout: 10000 });
  });

  test('re-clicking the already-selected VHost keeps the form valid', async ({ page }) => {
    test.setTimeout(120000);
    await startAddSource(page, vhostName);

    await page.getByRole('combobox', { name: 'VHost', exact: true }).click();
    await page.getByRole('option', { name: vhostName }).click();

    await expect(
      page.getByRole('button', { name: 'Next Step' }),
      'clicking the already-selected VHost cleared it'
    ).toBeEnabled({ timeout: 10000 });
  });

  test('choosing a different Type still changes it', async ({ page }) => {
    test.setTimeout(120000);
    await startAddSource(page, vhostName);

    await page.getByRole('combobox', { name: 'Type', exact: true }).click();
    await page.getByRole('option', { name: 'MySQL', exact: true }).click();

    await expect(page.getByRole('combobox', { name: 'Type', exact: true })).toHaveValue('MySQL');
    await expect(page.getByRole('button', { name: 'Next Step' })).toBeEnabled();
  });

  test('the sink form has the same guarantee', async ({ page }) => {
    test.setTimeout(120000);
    await page.goto('/sinks');
    await page.getByRole('button', { name: 'Add Sink' }).click();
    await page.getByLabel('Name').fill(`Sink select ${Date.now()}`);
    await page.getByRole('combobox', { name: 'VHost', exact: true }).click();
    await page.getByRole('option', { name: vhostName }).click();

    await page.getByRole('combobox', { name: 'Type', exact: true }).click();
    const current = await page.getByRole('combobox', { name: 'Type', exact: true }).inputValue();
    const option = page.getByRole('option', { name: current, exact: true }).first();
    if (await option.count()) {
      await option.click();
      await expect(
        page.getByRole('button', { name: 'Next Step' }),
        'clicking the already-selected sink Type cleared it'
      ).toBeEnabled({ timeout: 10000 });
    }
  });
});
