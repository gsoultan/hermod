/**
 * Scenario S: secrets and environment values.
 *
 * S1  A secret is added, rotated and deleted on the Secrets page, and its value
 *     is never shown again -- not on the page, not by the API.
 * S2  Every way a workflow can read one gives the stored value in Live Preview:
 *     secret("N"), {{secret.N}}, env("N") (env reads the secret manager first),
 *     a fallback for a missing name, and hmac_sha256 keyed by a secret.
 * S3  A running workflow reads it: webhook -> Set Fields -> HTTP sink, and the
 *     delivered record carries the value.
 * S4  env() falls back to the server's HERMOD_ENV_ variable. Runs when the
 *     suite is told the variable's value (HERMOD_E2E_ENV_VALUE, with the server
 *     started with HERMOD_ENV_E2E_REGION set to it, as CI does); otherwise
 *     skipped, and the report says so.
 */
import { test, expect, type Page } from '@playwright/test';
import { acceptConfirm } from '../support/confirm';
import {
  api,
  apiRaw,
  Cleanup,
  createSink,
  createSource,
  createWorkflow,
  login,
  postWebhook,
  Receiver,
  stamp,
  startWorkflow,
  watchErrors,
  webhookPath,
} from './helpers';

const VHOST = 'default';

async function putSecret(page: Page, cleanup: Cleanup, name: string, value: string) {
  await api(page, 'PUT', `/api/vhosts/${VHOST}/secrets/${name}`, { value });
  cleanup.add(() => apiRaw(page, 'DELETE', `/api/vhosts/${VHOST}/secrets/${name}`));
}

async function previewSet(page: Page, columns: Record<string, string>, message: Record<string, unknown> = { id: 1 }) {
  const config: Record<string, unknown> = { transType: 'set' };
  for (const [k, v] of Object.entries(columns)) config[`column.${k}`] = v;
  return api(page, 'POST', '/api/transformations/test', {
    transformation: { type: 'set', config },
    message,
    vhost: VHOST,
  });
}

const cleanup = new Cleanup();
test.afterEach(async () => cleanup.run());

test('S1: add, rotate and delete a secret on the Secrets page; its value is never shown back', async ({ page }) => {
  const watch = watchErrors(page);
  await login(page);
  const name = `E2E_S1_${Date.now()}`;
  const first = `first-${stamp()}`;
  const second = `second-${stamp()}`;
  cleanup.add(() => apiRaw(page, 'DELETE', `/api/vhosts/${VHOST}/secrets/${name}`));

  await page.goto('/secrets');
  // Secrets belong to one virtual host; the page asks which first.
  await page.getByRole('combobox', { name: 'Virtual host' }).click();
  await page.getByRole('option', { name: VHOST, exact: true }).click();
  await page.getByRole('button', { name: 'Add secret' }).first().click();
  let dialog = page.getByRole('dialog', { name: 'Add secret' });
  await dialog.getByRole('textbox', { name: 'Name' }).fill(name);
  await dialog.locator('input[type="password"], textarea').first().fill(first);
  await dialog.getByRole('button', { name: 'Save secret' }).click();
  await expect(dialog).toBeHidden();

  // Listed by how to use it, never by what it holds.
  await expect(page.getByText(`{{secret.${name}}}`)).toBeVisible();
  await expect(page.getByText(first)).toHaveCount(0);
  const listed = await apiRaw(page, 'GET', `/api/vhosts/${VHOST}/secrets`);
  expect(listed.status).toBe(200);
  expect(listed.body).toContain(name);
  expect(listed.body, 'the API returned a stored secret value').not.toContain(first);

  // Rotate.
  await page.getByRole('button', { name: `Rotate ${name}` }).click();
  dialog = page.getByRole('dialog', { name: `Rotate ${name}` });
  await dialog.locator('input[type="password"], textarea').first().fill(second);
  await dialog.getByRole('button', { name: 'Save secret' }).click();
  await expect(dialog).toBeHidden();
  const after = await previewSet(page, { v: `secret("${name}")` });
  expect(after.v, 'a workflow still reads the old value after rotating').toBe(second);

  // Delete.
  await page.getByRole('button', { name: `Delete ${name}` }).click();
  await acceptConfirm(page);
  await expect(page.getByText(`{{secret.${name}}}`)).toHaveCount(0);
  const gone = await previewSet(page, { v: `secret("${name}", "gone")` });
  expect(gone.v).toBe('gone');
  watch.expectClean('Secrets page');
});

test('S2: every way an expression reads a secret gives its value in Live Preview', async ({ page }) => {
  await login(page);
  const name = `E2E_S2_${Date.now()}`;
  const value = `v-${stamp()}`;
  await putSecret(page, cleanup, name, value);

  const out = await previewSet(page, {
    call: `secret("${name}")`,
    token: `{{secret.${name}}}`,
    inText: `key=${'{{'}secret.${name}${'}}'};`,
    env: `env("${name}")`,
    missing: `secret("E2E_NOT_THERE_${Date.now()}", "fallback")`,
    signed: `hmac_sha256(source.payload, "${name}")`,
  }, { payload: 'hello' });

  expect(out.call).toBe(value);
  expect(out.token).toBe(value);
  expect(out.inText).toBe(`key=${value};`);
  expect(out.env, 'env() reads the vhost secret manager first').toBe(value);
  expect(out.missing).toBe('fallback');
  expect(String(out.signed)).toMatch(/^[0-9a-f]{64}$/);

  // The same secret in an If condition's value.
  const cond = await api(page, 'POST', '/api/transformations/test', {
    transformation: {
      type: 'condition',
      config: { conditions: [{ field: 'token', operator: '=', value: `{{secret.${name}}}` }] },
    },
    message: { token: value },
    vhost: VHOST,
  });
  expect(cond.branch).toBe('true');
});

test('S3: a running workflow reads a secret and delivers it', async ({ page }) => {
  await login(page);
  const receiver = new Receiver();
  const base = await receiver.start();
  cleanup.add(() => receiver.stop());
  const tag = stamp();
  const name = `E2E_S3_${Date.now()}`;
  const value = `live-${tag}`;
  await putSecret(page, cleanup, name, value);

  const hook = webhookPath(`cov-secret-${tag}`);
  const src = await createSource(page, cleanup, { name: `cov-secret-src-${tag}`, type: 'webhook', config: { path: hook } });
  const snk = await createSink(page, cleanup, {
    name: `cov-secret-snk-${tag}`,
    type: 'http',
    config: { url: `${base}/secret`, method: 'POST' },
  });
  const wf = await createWorkflow(page, cleanup, {
    name: `cov-secret-wf-${tag}`,
    nodes: [
      { id: 's', type: 'source', ref_id: src.id, x: 0, y: 0 },
      { id: 'set', type: 'transformation', x: 250, y: 0, config: { transType: 'set', 'column.key': `secret("${name}")` } },
      { id: 'k', type: 'sink', ref_id: snk.id, x: 500, y: 0 },
    ],
    edges: [
      { id: 'e1', source_id: 's', target_id: 'set' },
      { id: 'e2', source_id: 'set', target_id: 'k' },
    ],
  });
  await startWorkflow(page, wf.id);
  await postWebhook(page, hook, { order: 1 });
  const got = await receiver.waitFor('/secret');
  expect(got, 'nothing was delivered').toHaveLength(1);
  expect(got[0].body.key).toBe(value);
});

test('S4: env() falls back to the server variable HERMOD_ENV_<NAME>', async ({ page }) => {
  const expected = process.env.HERMOD_E2E_ENV_VALUE;
  test.skip(!expected, 'set HERMOD_E2E_ENV_VALUE and start Hermod with HERMOD_ENV_E2E_REGION to the same value');
  await login(page);
  const out = await previewSet(page, { region: 'env("E2E_REGION")', fallback: 'env("E2E_UNSET_VARIABLE", "none")' });
  expect(out.region).toBe(expected);
  expect(out.fallback).toBe('none');
});
