import { test, expect } from '@playwright/test';
import { login } from './support/auth';

/**
 * Sources and sinks in a workspace.
 *
 * Both have carried workspace_id in the schema, in an index and in the storage
 * list filter since workspaces were added, but nothing in the UI ever set it
 * and ParseCommonFilter never read the query parameter — so the column, the
 * index and the filter were all dead weight.
 */
test('sources and sinks expose their workspace and can be filtered by it', async ({ page }) => {
  await login(page);

  // Made through the API so the test is about the source/sink screens, not
  // about Governance — which workspace_assignment_e2e already covers.
  const name = `e2e-ss-${Date.now().toString(36)}`;
  const created = await page.evaluate(async (name) => {
    const csrf = document.cookie.split('; ').find(c => c.startsWith('hermod_csrf='))?.split('=')[1] || '';
    const res = await fetch('/api/workspaces', {
      method: 'POST', credentials: 'include',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': decodeURIComponent(csrf) },
      body: JSON.stringify({ name }),
    });
    return { status: res.status, body: await res.json() };
  }, name);
  expect(created.status).toBe(201);
  // The id used to come back empty because the handler echoed the request body.
  expect(created.body.id, 'POST /api/workspaces must return the id it minted').toBeTruthy();

  for (const [route, label, empty] of [
    ['/sources', 'sources', 'No sources configured yet'],
    ['/sinks', 'sinks', 'No sinks found'],
  ] as const) {
    await page.goto(route);
    await expect(
      page.getByRole('columnheader', { name: 'Workspace' }),
      `${label} needs a Workspace column`,
    ).toBeVisible({ timeout: 15000 });

    const filter = page.getByLabel('Filter by workspace');

    // A brand-new workspace holds nothing, so the filter must empty the table —
    // proof the parameter reaches the query rather than being ignored.
    await filter.click();
    await page.getByRole('option', { name, exact: true }).click();
    await expect(page.getByText(empty)).toBeVisible({ timeout: 10000 });

    // "No workspace" is the unassigned view, not a workspace whose id is
    // literally "none".
    await filter.click();
    await page.getByRole('option', { name: 'No workspace', exact: true }).click();
    await expect(page.locator('table tbody tr').first()).toBeVisible({ timeout: 10000 });
    await expect(page.getByText(empty)).toHaveCount(0);
  }

  // The field exists on the forms that create them. It sits below the fold on
  // a 720px viewport, so scroll to it rather than asserting it is already on
  // screen.
  for (const route of ['/sources/new', '/sinks/new']) {
    await page.goto(route);
    const field = page.getByRole('combobox', { name: 'Workspace (Optional)' });
    await expect(field, `${route} should offer a workspace field`).toHaveCount(1, { timeout: 20000 });
    await field.scrollIntoViewIfNeeded();
    await expect(field).toBeVisible();
  }

  // Clean up the probe workspace.
  await page.evaluate(async (id) => {
    const csrf = document.cookie.split('; ').find(c => c.startsWith('hermod_csrf='))?.split('=')[1] || '';
    await fetch(`/api/workspaces/${id}`, {
      method: 'DELETE', credentials: 'include',
      headers: { 'X-CSRF-Token': decodeURIComponent(csrf) },
    });
  }, created.body.id);
});
