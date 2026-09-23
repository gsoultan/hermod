import { test, expect, type Page } from '@playwright/test';
import { login } from './support/auth';
import { acceptConfirm } from './support/confirm';

/**
 * Putting a workflow in a workspace, end to end.
 *
 * The feature existed but had no reachable entry point: the Workflows list
 * showed a Workspace column and offered no way to set it, so the only route in
 * was the editor's settings drawer — a panel that starts closed, five clicks
 * deep. This drives the path the list page now offers instead.
 *
 * It also covers the two things that made a workspace unusable once created:
 * the quotas could never be edited, and deleting one left its members pointing
 * at an id that no longer resolved.
 */

const unique = () => `e2e-ws-${Date.now().toString(36)}`;

/** Seeded fixtures, removed after each test so this spec leaves no litter of its own. */
const createdWorkflows: string[] = [];
const createdSources: string[] = [];
const createdSinks: string[] = [];

/** The API as the logged-in browser session; the cookie travels with it. */
async function api(page: Page, method: string, path: string, body?: unknown) {
  return page.evaluate(
    async ({ method, path, body }) => {
      const csrf = document.cookie.split('; ').find((c) => c.startsWith('hermod_csrf='))?.split('=')[1] || '';
      const res = await fetch(path, {
        method,
        credentials: 'include',
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': decodeURIComponent(csrf) },
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      const text = await res.text();
      return { status: res.status, body: text ? JSON.parse(text) : null };
    },
    { method, path, body },
  );
}

test.describe('workspace assignment', () => {
  test.afterEach(async ({ page }) => {
    // Workflows first: a source cannot be deleted while a workflow names it.
    for (const id of createdWorkflows.splice(0)) {
      await api(page, 'DELETE', `/api/workflows/${id}`);
    }
    for (const id of createdSources.splice(0)) {
      await api(page, 'DELETE', `/api/sources/${id}`);
    }
    for (const id of createdSinks.splice(0)) {
      await api(page, 'DELETE', `/api/sinks/${id}`);
    }
  });

  test('create a workspace, move a workflow into it from the list, then edit and delete it', async ({ page }) => {
    await login(page);
    const name = unique();

    // Two workflows, seeded here rather than taken from whatever the list
    // happens to hold. This used to read `.first()` and then pick "some other
    // id" for the quota check, which worked only because
    // workflow_export_import_e2e leaked its fixtures -- three workflows per
    // run, never cleaned up. With that leak closed the list is empty and there
    // is nothing to move, so the dependency was on another spec's litter.
    // The test needs exactly two: one to move in, one the full workspace must
    // refuse.
    const movedName = `${name}-a`;
    const secondName = `${name}-b`;
    const seeded: string[] = [];
    for (const wfName of [movedName, secondName]) {
      // A workflow with no nodes is refused ("must contain at least one source
      // and one sink"), so each one gets a source and a sink of its own --
      // sources.name and sinks.name are NOT NULL UNIQUE, so they cannot be
      // shared. Neither is ever connected to anything: this test is about the
      // workspace quota, not about running a pipeline.
      const create = async (path: string, body: unknown) => {
        const res = await api(page, 'POST', path, body);
        expect(res.status, `seeding ${path} for ${wfName} failed: ${JSON.stringify(res.body)}`).toBeLessThan(300);
        return res.body.id as string;
      };
      const sourceID = await create('/api/sources', {
        name: `${wfName}-src`, type: 'postgres', vhost: 'default', active: false,
        config: { host: 'seed.invalid', port: '5432', database: 'd', username: 'u', db_password: 'p', use_cdc: 'false' },
      });
      const sinkID = await create('/api/sinks', {
        name: `${wfName}-snk`, type: 'postgres', vhost: 'default', active: false,
        config: { host: 'seed.invalid', port: '5432', database: 'd', username: 'u', db_password: 'p', table: 't' },
      });
      createdSources.push(sourceID);
      createdSinks.push(sinkID);

      const wfID = await create('/api/workflows', {
        name: wfName, vhost: 'default', active: false,
        nodes: [
          { id: 'n1', type: 'source', ref_id: sourceID, x: 0, y: 0 },
          { id: 'n2', type: 'sink', ref_id: sinkID, x: 200, y: 0 },
        ],
        edges: [{ id: 'e1', source_id: 'n1', target_id: 'n2' }],
      });
      seeded.push(wfID);
      createdWorkflows.push(wfID);
    }

    // --- Create, through Settings → Governance -------------------------------
    await page.goto('/settings');
    await page.getByRole('tab', { name: /governance/i }).click();
    await page.getByRole('button', { name: 'Create Workspace' }).click();

    await page.getByLabel('Workspace Name').fill(name);
    await page.getByLabel('Max Workflows').fill('1');
    await page.getByRole('button', { name: 'Create Workspace' }).last().click();

    // The row carries the quota it was created with. It used to be written
    // blind and never shown again.
    const row = page.locator('tr', { hasText: name });
    await expect(row).toBeVisible({ timeout: 10000 });
    await expect(row).toContainText('1');

    // --- Move a workflow in, from the list -----------------------------------
    await page.goto('/workflows');
    const movingRow = page.locator('tr').filter({ hasText: movedName }).first();
    await expect(movingRow).toBeVisible({ timeout: 15000 });

    await movingRow.getByRole('checkbox').check();
    await page.getByRole('button', { name: /Batch Actions/ }).click();
    await page.getByRole('menuitem', { name: /Move to Workspace/ }).click();

    await page.getByPlaceholder('Select a workspace').click();
    await page.getByRole('option', { name, exact: true }).click();
    await page.getByRole('button', { name: 'Move', exact: true }).click();

    // The badge in the row's Workspace column is the whole point.
    const movedRow = page.locator('tr', { hasText: movedName }).first();
    await expect(movedRow).toContainText(name, { timeout: 10000 });

    // --- The quota is enforced on that path ----------------------------------
    // A second workflow into a max_workflows=1 workspace must be refused. This
    // is what PUT /api/workflows/{id} used to wave through.
    const wss = await api(page, 'GET', '/api/workspaces');
    const ws = (wss.body || []).find((w: any) => w.name === name);
    expect(ws, 'the workspace we just created should be listed').toBeTruthy();

    const second = seeded[1];
    const before = await api(page, 'GET', `/api/workflows/${second}`);
    const refused = await api(page, 'PUT', `/api/workflows/${second}`, {
      ...before.body,
      workspace_id: ws.id,
    });
    expect(refused.status, 'a full workspace must refuse a second workflow').toBe(403);

    // --- Raise the quota, which used to be impossible -------------------------
    await page.goto('/settings');
    await page.getByRole('tab', { name: /governance/i }).click();
    await page.locator('tr', { hasText: name }).getByLabel('Edit workspace').click();
    await page.getByLabel('Max Workflows').fill('10');
    await page.getByRole('button', { name: 'Save Changes' }).click();
    await expect(page.locator('tr', { hasText: name })).toContainText('10', { timeout: 10000 });

    // The same request now succeeds.
    const admitted = await api(page, 'PUT', `/api/workflows/${second}`, {
      ...before.body,
      workspace_id: ws.id,
    });
    expect(admitted.status, 'raising the quota should admit the workflow').toBe(200);

    // --- Delete, and check nothing is left pointing at it ---------------------
    await page.reload();
    await page.getByRole('tab', { name: /governance/i }).click();
    await page.locator('tr', { hasText: name }).getByLabel('Delete workspace').click();
    await acceptConfirm(page);
    await expect(page.locator('tr', { hasText: name })).toHaveCount(0, { timeout: 10000 });

    // Both members come back unassigned rather than holding a dead id.
    for (const id of seeded) {
      const after = await api(page, 'GET', `/api/workflows/${id}`);
      expect(after.body.workspace_id || '', `workflow ${id} still references the deleted workspace`).toBe('');
    }
  });
});
