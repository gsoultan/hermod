import { test, expect } from '@playwright/test';
import { login, apiRequest } from './support/auth';

/**
 * An export file leaves connection passwords out, and the import wizard asks
 * for the ones this instance does not already have.
 *
 * The file is downloaded, mailed and committed, so a source's password stays
 * on the instance that wrote it: the file holds `[REDACTED]` in its place. An
 * import of a connection that is not here cannot be saved without one, so the
 * wizard shows the field empty, says the file left it out, and the server
 * refuses the import (writing nothing) until it is typed.
 */

const PASSWORD = 'S3cret-e2e-export';

test('an exported source password stays home, and the wizard asks for it on import', async ({ page }) => {
  test.setTimeout(120000);
  await login(page);
  const stamp = Date.now();
  const post = async (path: string, body: unknown) => {
    const res = await apiRequest(page, path, { method: 'POST', body });
    expect(res.status, `seeding ${path}: ${res.body}`).toBeLessThan(300);
    return JSON.parse(res.body);
  };

  const sourceName = `export-cred-src-${stamp}`;
  const source = await post('/api/sources', {
    name: sourceName, type: 'postgres', vhost: 'default', active: false,
    config: { host: 'orders.invalid', port: '5432', database: 'orders', user: 'app', password: PASSWORD, use_cdc: 'false' },
  });
  const sink = await post('/api/sinks', {
    name: `export-cred-snk-${stamp}`, type: 'stdout', vhost: 'default', active: false, config: {},
  });
  const workflow = await post('/api/workflows', {
    name: `export-cred-${stamp}`, vhost: 'default', active: false,
    nodes: [
      { id: 'n1', type: 'source', ref_id: source.id, x: 0, y: 0 },
      { id: 'n2', type: 'sink', ref_id: sink.id, x: 300, y: 0 },
    ],
    edges: [{ id: 'e1', source_id: 'n1', target_id: 'n2' }],
  });

  try {
    // --- The file -------------------------------------------------------
    const exported = await apiRequest(page, `/api/workflows/${workflow.id}/export`);
    expect(exported.status).toBe(200);
    expect(exported.body, 'the export file must not carry the source password').not.toContain(PASSWORD);
    const bundle = JSON.parse(exported.body);
    expect(bundle.sources[0].config.password).toBe('[REDACTED]');
    expect(bundle.sources[0].config.host, 'everything but the credential travels').toBe('orders.invalid');

    // Somewhere that has never seen this connection.
    expect((await apiRequest(page, `/api/workflows/${workflow.id}`, { method: 'DELETE' })).status).toBeLessThan(300);
    expect((await apiRequest(page, `/api/sources/${source.id}`, { method: 'DELETE' })).status).toBeLessThan(300);

    // --- The wizard -----------------------------------------------------
    await page.goto('/workflows');
    await page.getByRole('button', { name: 'More ways to make a flow' }).click();
    await page.getByRole('menuitem', { name: 'Import JSON' }).click();
    const dialog = page.getByRole('dialog', { name: 'Import a flow' });
    await dialog.locator('textarea').first().fill(exported.body.trim());

    const next = dialog.getByRole('button', { name: 'Next' });
    await next.click(); // -> Workflow
    await next.click(); // -> Sources

    const card = dialog.locator('.mantine-Card-root').filter({ hasText: sourceName });
    await expect(card.getByTestId('withheld-credentials')).toContainText('password');
    const password = card.getByLabel('Password', { exact: false }).first();
    await expect(password, 'a left-out password is shown empty, not as [REDACTED]').toHaveValue('');

    const goToReview = async () => {
      while (!(await dialog.getByRole('button', { name: 'Import workflow' }).isVisible())) {
        await next.click();
      }
    };

    // Left empty, the import is refused and names what it needs.
    await goToReview();
    await dialog.getByRole('button', { name: 'Import workflow' }).click();
    await expect(dialog.getByText(new RegExp(`Missing: source ${sourceName}: password`))).toBeVisible({ timeout: 15000 });
    expect((await apiRequest(page, `/api/sources/${source.id}`)).status, 'a refused import writes nothing').toBe(404);

    // Typed in, it imports with the typed password.
    for (let i = 0; i < 6 && !(await dialog.getByRole('heading', { name: 'Sources' }).isVisible()); i++) {
      await dialog.getByRole('button', { name: 'Back' }).click();
    }
    await card.getByLabel('Password', { exact: false }).first().fill('typed-on-import');
    await goToReview();
    await dialog.getByRole('button', { name: 'Import workflow' }).click();
    await expect(page.getByText(/is ready\. It arrives stopped/i)).toBeVisible({ timeout: 15000 });

    const imported = await apiRequest(page, `/api/sources/${source.id}`);
    expect(imported.status).toBe(200);
    expect(JSON.parse(imported.body).config.password).toBe('typed-on-import');
  } finally {
    await apiRequest(page, `/api/workflows/${workflow.id}`, { method: 'DELETE' }).catch(() => {});
    await apiRequest(page, `/api/sources/${source.id}`, { method: 'DELETE' }).catch(() => {});
    await apiRequest(page, `/api/sinks/${sink.id}`, { method: 'DELETE' }).catch(() => {});
  }
});
