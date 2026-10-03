import { test, expect } from '@playwright/test';
import { login, apiRequest } from './support/auth';

/**
 * A vhost's own secrets, from saving one to a workflow reading it.
 *
 * There was nowhere in Hermod to save a secret: secret("NAME") read the
 * server's environment or an external manager, the same for every vhost. This
 * goes the whole way round through the real UI and backend: the secret is saved
 * on the Secrets page, a Set Fields node of a workflow in that vhost names it,
 * and the node's Live Preview shows the value the backend resolved.
 */

const created: { workflows: string[]; sources: string[]; secrets: Array<[string, string]> } = {
  workflows: [], sources: [], secrets: [],
};

test.afterEach(async ({ page }) => {
  for (const id of created.workflows.splice(0)) {
    await apiRequest(page, `/api/workflows/${id}`, { method: 'DELETE' }).catch(() => {});
  }
  for (const id of created.sources.splice(0)) {
    await apiRequest(page, `/api/sources/${id}`, { method: 'DELETE' }).catch(() => {});
  }
  for (const [vhost, name] of created.secrets.splice(0)) {
    await apiRequest(page, `/api/vhosts/${vhost}/secrets/${name}`, { method: 'DELETE' }).catch(() => {});
  }
});

test('a secret saved for a vhost is read by that vhost’s workflow', async ({ page }) => {
  test.setTimeout(120000);
  await login(page);
  const stamp = Date.now();
  const name = `E2E_KEY_${stamp}`;
  const value = `e2e-secret-${stamp}`;

  // Save it on the Secrets page, in the default vhost.
  await page.evaluate(() => localStorage.setItem('hermod_selected_vhost', 'default'));
  await page.goto('/secrets');
  await page.getByRole('button', { name: 'Add secret' }).first().click();
  const dialog = page.getByRole('dialog', { name: 'Add secret' });
  await dialog.getByRole('textbox', { name: 'Name' }).fill(name);
  await dialog.locator('input[type="password"]').fill(value);
  created.secrets.push(['default', name]);
  await dialog.getByRole('button', { name: 'Save secret' }).click();
  await expect(dialog).toBeHidden();

  // It is listed by name; the value is nowhere on the page.
  await expect(page.getByText(`secret("${name}")`)).toBeVisible();
  await expect(page.getByText(value)).toHaveCount(0);

  // The API lists the name and never the value.
  const listed = await apiRequest(page, '/api/vhosts/default/secrets');
  expect(listed.body).toContain(name);
  expect(listed.body).not.toContain(value);

  // A workflow of the same vhost names it in a Set Fields value.
  const post = async (path: string, body: unknown) => {
    const res = await apiRequest(page, path, { method: 'POST', body });
    if (res.status >= 400) throw new Error(`fixture setup failed: POST ${path} -> ${res.status} ${res.body}`);
    return JSON.parse(res.body);
  };
  const source = await post('/api/sources', {
    name: `vhost-secret-src-${stamp}`, type: 'webhook', vhost: 'default', active: false,
    config: { path: `/vhost-secret-${stamp}` },
  });
  created.sources.push(source.id);
  const workflow = await post('/api/workflows', {
    name: `vhost-secret-${stamp}`, vhost: 'default', active: false,
    nodes: [
      { id: 'n-src', type: 'source', ref_id: source.id, x: 100, y: 100 },
      {
        id: 'n-set', type: 'transformation', x: 400, y: 100,
        config: { transType: 'set', lastSample: { id: 1 }, 'column.key': `secret("${name}")` },
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

  // The preview is the backend's answer for this workflow's vhost.
  await expect(page.getByTestId('live-preview')).toContainText(`"key": "${value}"`, { timeout: 30000 });
});
