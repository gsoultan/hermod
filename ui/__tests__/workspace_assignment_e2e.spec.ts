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
  test('create a workspace, move a workflow into it from the list, then edit and delete it', async ({ page }) => {
    await login(page);
    const name = unique();

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
    const firstRow = page.locator('table tbody tr').first();
    await expect(firstRow).toBeVisible({ timeout: 15000 });
    const movedName = (await firstRow.locator('td').nth(1).innerText()).trim();

    await firstRow.getByRole('checkbox').check();
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
    const wfs = await api(page, 'GET', '/api/workflows?limit=5');
    const ids: string[] = (wfs.body?.data || []).map((w: any) => w.id);
    const wss = await api(page, 'GET', '/api/workspaces');
    const ws = (wss.body || []).find((w: any) => w.name === name);
    expect(ws, 'the workspace we just created should be listed').toBeTruthy();

    const second = ids.find((id) => id !== ids[0]);
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
    for (const id of [ids[0], second]) {
      const after = await api(page, 'GET', `/api/workflows/${id}`);
      expect(after.body.workspace_id || '', `workflow ${id} still references the deleted workspace`).toBe('');
    }
  });
});
