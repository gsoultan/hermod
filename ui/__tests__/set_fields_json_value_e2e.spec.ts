import { test, expect } from '@playwright/test';
import { login, apiRequest } from './support/auth';

/**
 * A Set Fields value that is a JSON object resolves its `source.` paths.
 *
 * Reported against `column.after.QueryParams` holding
 * `{"session": "source.after.session.sessions.0.access_token"}`: the node wrote
 * the object through as it was configured, so the preview -- and every message
 * -- carried the path text where the token belonged. The row editor showed the
 * same value as "[object Object]", and the raw-JSON box it had to be typed into
 * was two lines tall.
 *
 * Driven from storage through the real editor: the node config is saved, the
 * node is opened, and its Live Preview asks the backend.
 */

const TOKEN = 'v2.local.E2E-SET-TOKEN';

// A row in the report's shape: a list of sessions nested inside the row.
const ROW = {
  operation: 'snapshot',
  table: 'reminders',
  after: {
    id: '01a0e612-1148-77d7-b423-cd303d0cdf47',
    status: 'SCHEDULED',
    session: { sessions: [{ access_token: TOKEN, id: 's-1' }] },
  },
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

test('a JSON object value resolves its source paths, and is edited as JSON', async ({ page }) => {
  test.setTimeout(120000);
  await login(page);
  const stamp = Date.now();
  const post = async (path: string, body: unknown) => {
    const res = await apiRequest(page, path, { method: 'POST', body });
    if (res.status >= 400) throw new Error(`fixture setup failed: POST ${path} -> ${res.status} ${res.body}`);
    return JSON.parse(res.body);
  };

  // A workflow has to hold a source; the set node previews from its own
  // lastSample, as a node with nothing wired into it does.
  const source = await post('/api/sources', {
    name: `set-json-src-${stamp}`, type: 'webhook', vhost: 'default', active: false,
    config: { path: `/set-json-${stamp}` },
  });
  created.sources.push(source.id);
  const workflow = await post('/api/workflows', {
    name: `set-json-${stamp}`, vhost: 'default', active: false,
    nodes: [
      { id: 'n-src', type: 'source', ref_id: source.id, x: 100, y: 100 },
      {
        id: 'n-set', type: 'transformation', x: 400, y: 100,
        config: {
          transType: 'set', lastSample: ROW,
          'column.after.QueryParams': { session: 'source.after.session.sessions.0.access_token' },
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

  // The row shows the object as JSON, not as "[object Object]".
  const value = page.getByRole('textbox', { name: 'JSON value' });
  await expect(value).toBeVisible({ timeout: 20000 });
  expect(JSON.parse(await value.inputValue())).toEqual({ session: 'source.after.session.sessions.0.access_token' });
  await expect(page.getByRole('combobox', { name: 'Target path' })).toHaveValue('after.QueryParams');

  // The preview is the backend's answer: the path is read, not written through.
  const preview = page.getByTestId('live-preview');
  await expect(preview).toContainText(`"session": "${TOKEN}"`, { timeout: 30000 });
  await expect(preview).not.toContainText('"session": "source.after.session.sessions.0.access_token"');

  // The same config as JSON, in an editor that can be opened wide.
  await page.getByRole('radiogroup', { name: 'Editor view' }).getByText('JSON', { exact: true }).click();
  const json = page.getByRole('textbox', { name: 'Fields (JSON)' });
  expect(JSON.parse(await json.inputValue())).toEqual({
    'column.after.QueryParams': { session: 'source.after.session.sessions.0.access_token' },
  });
  await page.getByRole('button', { name: 'Expand JSON editor' }).click();
  const dialog = page.getByRole('dialog', { name: 'Fields (JSON)' });
  await expect(dialog).toBeVisible();
  await dialog.getByRole('button', { name: 'Done' }).click();
  await expect(dialog).toBeHidden();
});
