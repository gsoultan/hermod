import { test, expect } from '@playwright/test';
import { apiRequest, login } from './support/auth';

/**
 * What a workflow saved from the editor holds.
 *
 * Setting up a source or sink in its drawer used to spread the whole
 * connection the drawer returned over the node, so the workflow row, each of
 * its versions, every export and what a promotion sent all carried the
 * connection's config with its password in plain text. A password typed into
 * a step (API Lookup's Basic auth here) was saved as typed.
 *
 * Now the node keeps only its own settings, and a typed step credential is
 * stored as a vhost secret with {{secret.NAME}} left in the step.
 */
test('a workflow saved from the editor holds no connection copy or typed credential', async ({ page }) => {
  await login(page);
  const tag = Date.now().toString(36);

  const src = await apiRequest(page, '/api/sources', { method: 'POST', body: {
    name: `e2e-clean-src-${tag}`, type: 'postgres', vhost: 'default',
    config: { host: 'db.invalid', port: '5432', user: 'svc_reader', password: `SrcPW-${tag}`, dbname: 'app', use_cdc: 'false', tables: 'public.users' },
  } });
  expect(src.status).toBeLessThan(300);
  const srcId = JSON.parse(src.body).id;

  const wf = await apiRequest(page, '/api/workflows', { method: 'POST', body: {
    name: `e2e-clean-${tag}`, vhost: 'default',
    nodes: [
      { id: 'n1', type: 'source', ref_id: srcId, config: { label: `e2e-clean-src-${tag}`, type: 'postgres' }, x: 0, y: 0 },
      { id: 'n2', type: 'transformation', config: { label: 'lookup', transType: 'api_lookup', url: 'https://api.example.com/u', targetField: 'profile', authType: 'basic', username: 'apiuser' }, x: 300, y: 0 },
    ],
    edges: [{ id: 'e1', source_id: 'n1', target_id: 'n2' }],
  } });
  expect(wf.status).toBe(201);
  const wfId = JSON.parse(wf.body).id;

  await page.goto(`/workflows/${wfId}/edit`);
  // The source drawer's own save is what used to copy the connection in.
  await page.getByText(`e2e-clean-src-${tag}`, { exact: true }).click();
  const saveWorkflow = page.waitForResponse(r => r.url().endsWith(`/api/workflows/${wfId}`) && r.request().method() === 'PUT');
  await page.getByRole('button', { name: 'Update Source' }).click();
  expect((await saveWorkflow).status()).toBe(200);

  // A password typed into a step, then the toolbar's Save.
  await page.locator('p[title="lookup"]').click();
  // Toasts from the first save sit over the tabs; dispatch rather than click.
  await page.getByRole('tab', { name: 'Auth' }).dispatchEvent('click');
  await page.getByRole('textbox', { name: 'Password' }).fill(`StepPW-${tag}`);
  await page.getByRole('button', { name: 'Close' }).first().dispatchEvent('click');
  const saveAgain = page.waitForResponse(r => r.url().endsWith(`/api/workflows/${wfId}`) && r.request().method() === 'PUT');
  await page.getByRole('button', { name: 'Save', exact: true }).first().dispatchEvent('click');
  const saved = await saveAgain;
  expect(saved.status()).toBe(200);
  // The request itself no longer carries the copy.
  expect(saved.request().postData() || '').not.toContain(`SrcPW-${tag}`);

  const stored = await apiRequest(page, `/api/workflows/${wfId}`);
  expect(stored.body).not.toContain(`SrcPW-${tag}`);
  expect(stored.body).not.toContain(`StepPW-${tag}`);
  expect(stored.body).not.toContain('svc_reader');
  const nodes = JSON.parse(stored.body).nodes;
  expect(nodes[0].config).toEqual({ label: `e2e-clean-src-${tag}`, type: 'postgres' });
  expect(nodes[1].config.password).toMatch(/^\{\{secret\.WF_[A-Z0-9_]+_PASSWORD\}\}$/);
  expect(nodes[1].config.username).toMatch(/^\{\{secret\.WF_[A-Z0-9_]+_USERNAME\}\}$/);

  const exported = await apiRequest(page, `/api/workflows/${wfId}/export`);
  const bundle = JSON.parse(exported.body);
  expect(JSON.stringify(bundle.workflow)).not.toContain(`StepPW-${tag}`);
  expect(JSON.stringify(bundle.workflow)).not.toContain(`SrcPW-${tag}`);

  // The secret holds the value the step had; its name is listed, never its value.
  const secrets = await apiRequest(page, '/api/vhosts/default/secrets');
  expect(secrets.body).toContain(nodes[1].config.password.slice('{{secret.'.length, -2));
  expect(secrets.body).not.toContain(`StepPW-${tag}`);

  await apiRequest(page, `/api/workflows/${wfId}`, { method: 'DELETE' });
  await apiRequest(page, `/api/sources/${srcId}`, { method: 'DELETE' });
});
