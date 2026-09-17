import { test, expect, type Page } from '@playwright/test';
import { login, apiRequest } from './support/auth';

/**
 * Export and import, driven the way an operator drives them.
 *
 * Two things here are only visible end to end. The bundle has to carry the
 * sources a *transformation* names in its config — a db_lookup node holds one
 * under `sourceId`, which the export's node scan did not look at, so the
 * downloaded file described a workflow that could not start anywhere else. And
 * the import modal's JSON field reformats itself on blur; clicking Import while
 * it still has focus re-renders the modal between mousedown and mouseup, so the
 * click never becomes a click and the first press does nothing at all.
 */

/**
 * sources.name and sinks.name are `NOT NULL UNIQUE`, so fixtures cannot share a
 * name across tests — the second one fails while seeding, with a constraint
 * error that reads like a product bug.
 */
function names(tag: string) {
  const suffix = `${tag}${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`;
  return {
    WF_NAME: `E2E export ${suffix}`,
    MAIN_SOURCE: `E2E main ${suffix}`,
    LOOKUP_SOURCE: `E2E lookup ${suffix}`,
    SINK_NAME: `E2E sink ${suffix}`,
  };
}
type Names = ReturnType<typeof names>;

async function seed(page: Page, n: Names) {
  const created = async (path: string, body: unknown) => {
    const res = await apiRequest(page, path, { method: 'POST', body });
    expect(res.status, `seeding ${path} failed: ${res.body}`).toBeLessThan(300);
    return JSON.parse(res.body).id as string;
  };

  const mainID = await created('/api/sources', {
    name: n.MAIN_SOURCE, type: 'postgres', vhost: 'default', active: false,
    config: { host: 'orders.invalid', port: '5432', database: 'orders', username: 'h', db_password: 'p', use_cdc: 'false' },
  });
  const lookupID = await created('/api/sources', {
    name: n.LOOKUP_SOURCE, type: 'mysql', vhost: 'default', active: false,
    config: { host: 'crm.invalid', port: '3306', database: 'crm', username: 'h', db_password: 'p', use_cdc: 'false' },
  });
  const sinkID = await created('/api/sinks', {
    name: n.SINK_NAME, type: 'postgres', vhost: 'default', active: false,
    config: { host: 'dw.invalid', port: '5432', database: 'dw', username: 'h', db_password: 'p', table: 't' },
  });
  const wfID = await created('/api/workflows', {
    name: n.WF_NAME, vhost: 'default', active: false,
    nodes: [
      { id: 'n1', type: 'source', ref_id: mainID, x: 0, y: 0 },
      // The reference that lives in a node's config rather than its ref_id.
      { id: 'n2', type: 'transformation', x: 200, y: 0,
        config: { transType: 'db_lookup', sourceId: lookupID, table: 'customers', keyColumn: 'id', keyField: '$.id', targetField: 'customer' } },
      { id: 'n3', type: 'sink', ref_id: sinkID, x: 400, y: 0 },
    ],
    edges: [
      { id: 'e1', source_id: 'n1', target_id: 'n2' },
      { id: 'e2', source_id: 'n2', target_id: 'n3' },
    ],
  });
  return { mainID, lookupID, sinkID, wfID };
}

test('a workflow exports with every source it references, and the wizard imports it with edits', async ({ page }) => {
  await login(page);
  const n = names('a');
  const { WF_NAME, MAIN_SOURCE, LOOKUP_SOURCE, SINK_NAME } = n;
  const ids = await seed(page, n);

  await page.goto('/workflows');
  await page.getByPlaceholder('Search workflows...').fill(WF_NAME);
  const row = page.getByRole('row', { name: new RegExp(WF_NAME) });
  await expect(row).toBeVisible({ timeout: 15000 });

  // --- Export -----------------------------------------------------------
  const downloadPromise = page.waitForEvent('download', { timeout: 15000 });
  await row.getByRole('button', { name: 'Export workflow to JSON' }).click();
  const download = await downloadPromise;

  expect(download.suggestedFilename(), 'the filename should be derived from the workflow name, safely')
    .toMatch(/^workflow-[A-Za-z0-9._-]+\.json$/);

  const stream = await download.createReadStream();
  const chunks: Buffer[] = [];
  for await (const c of stream) chunks.push(c as Buffer);
  const raw = Buffer.concat(chunks).toString();
  const bundle = JSON.parse(raw);

  const bundledSourceNames = (bundle.sources ?? []).map((s: { name: string }) => s.name).sort();
  expect(bundledSourceNames, 'the bundle must carry the source the db_lookup node names in its config')
    .toEqual([MAIN_SOURCE, LOOKUP_SOURCE].sort());
  expect((bundle.sinks ?? []).map((s: { name: string }) => s.name)).toEqual([SINK_NAME]);
  expect(bundle.missing_refs ?? null, 'nothing should be missing from a freshly seeded workflow').toBeNull();
  expect(bundle.workflow.worker_id, 'a bundle describes a workflow, not a run of one').toBeFalsy();
  for (const src of bundle.sources) {
    expect(src.state ?? null, 'a source cursor belongs to the instance that produced it').toBeNull();
  }

  // --- Import through the wizard ---------------------------------------
  // Delete the lookup source so the import has something to restore. It is
  // referenced only from a node's config, so nothing guards it.
  const del = await apiRequest(page, `/api/sources/${ids.lookupID}`, { method: 'DELETE' });
  expect(del.status).toBeLessThan(300);

  const posts: number[] = [];
  page.on('response', (r) => { if (r.url().includes('/workflows/import')) posts.push(r.status()); });

  await page.reload();
  await page.getByRole('button', { name: 'Import JSON' }).click();

  const dialog = page.getByRole('dialog', { name: 'Import Workflow' });
  await dialog.locator('textarea').first().fill(raw.trim());

  // The summary reads the file, rather than waiting for a submit to fail.
  await expect(dialog.getByText('2 sources')).toBeVisible({ timeout: 10000 });
  await expect(dialog.getByText('1 node to review')).toBeVisible();

  const next = dialog.getByRole('button', { name: 'Next' });
  await next.click();                                     // -> Workflow
  await expect(dialog.getByRole('heading', { name: 'The workflow' })).toBeVisible();

  await next.click();                                     // -> Sources
  await expect(dialog.getByRole('heading', { name: 'Sources' })).toBeVisible();

  // Edit the lookup source the way an operator moving between environments
  // would: a different host, and a name that says where it came from.
  const lookupCard = dialog.locator('.mantine-Card-root').filter({ hasText: LOOKUP_SOURCE });
  // getByLabel would have to match "Name *" — the required marker is part of
  // the label's text. The accessible name is the plain one.
  await lookupCard.getByRole('textbox', { name: 'Name', exact: true }).fill(`${LOOKUP_SOURCE} edited`);
  await lookupCard.getByRole('textbox', { name: 'Host', exact: true }).fill('crm.edited.invalid');

  await next.click();                                     // -> Sinks
  await expect(dialog.getByRole('heading', { name: 'Sinks' })).toBeVisible();

  await next.click();                                     // -> Nodes
  await expect(dialog.getByRole('heading', { name: /environment-specific/i })).toBeVisible();

  await next.click();                                     // -> Review
  await expect(dialog.getByRole('heading', { name: 'What will happen' })).toBeVisible();
  // Two sources, one sink, one workflow.
  await expect(dialog.getByRole('row')).toHaveCount(5);

  await dialog.getByRole('button', { name: 'Import workflow' }).click();

  await expect(page.getByText(/is ready\. It arrives stopped/i)).toBeVisible({ timeout: 15000 });
  expect(posts.length, 'the wizard must submit exactly once').toBe(1);

  // The edits made in the wizard are what was written, not what the file said.
  const restored = await apiRequest(page, `/api/sources/${ids.lookupID}`);
  expect(restored.status, 'the import should have restored the deleted lookup source').toBe(200);
  const body = JSON.parse(restored.body);
  expect(body.name).toBe(`${LOOKUP_SOURCE} edited`);
  expect(body.config.host, 'the host typed in the wizard is the one that was saved').toBe('crm.edited.invalid');
});

/**
 * Importing a bundle that collides with what is already here.
 *
 * Choosing "import as a separate copy" hands out new ids, and every reference
 * inside the bundle has to follow — a source node's ref_id, a db_lookup's
 * sourceId, a batch_sql's source_id, the dead-letter sink. Missing one produces
 * a workflow that imports cleanly and then cannot start, which is precisely the
 * failure the export side already had. So this drives the real screen and then
 * reads the references back out of storage.
 */
test('importing a colliding bundle as a copy leaves the original alone and repoints every reference', async ({ page }) => {
  await login(page);
  const n = names('b');
  const { WF_NAME, MAIN_SOURCE, LOOKUP_SOURCE, SINK_NAME } = n;
  const ids = await seed(page, n);

  await page.goto('/workflows');
  await page.getByPlaceholder('Search workflows...').fill(WF_NAME);
  const row = page.getByRole('row', { name: new RegExp(WF_NAME) });
  await expect(row).toBeVisible({ timeout: 15000 });

  const downloadPromise = page.waitForEvent('download', { timeout: 15000 });
  await row.getByRole('button', { name: 'Export workflow to JSON' }).click();
  const download = await downloadPromise;
  const stream = await download.createReadStream();
  const chunks: Buffer[] = [];
  for await (const c of stream) chunks.push(c as Buffer);
  const raw = Buffer.concat(chunks).toString();

  // Nothing is deleted this time: every id in the bundle already names
  // something here.
  await page.getByRole('button', { name: 'Import JSON' }).click();
  const dialog = page.getByRole('dialog', { name: 'Import Workflow' });
  await dialog.locator('textarea').first().fill(raw.trim());

  await expect(dialog.getByText('4 id collisions')).toBeVisible({ timeout: 10000 });

  const next = dialog.getByRole('button', { name: 'Next' });
  await next.click();                                     // -> Workflow

  // Renaming the workflow must not take the wizard down with it: reading
  // e.currentTarget inside the state updater threw, unmounted the modal and
  // lost every edit made so far.
  await dialog.getByRole('textbox', { name: 'Name', exact: true }).fill(`${WF_NAME} copy`);
  await expect(dialog).toBeVisible();
  await dialog.getByRole('combobox', { name: 'What should happen to it' }).click();
  await page.getByRole('option', { name: 'Import alongside it, as a new workflow' }).click();

  await next.click();                                     // -> Sources

  // Changing your mind must put the name back. sources.name is UNIQUE, so
  // choosing "copy" renames the incoming record; leaving that rename in place
  // after switching back to "replace" would rename the record being kept.
  const mainCard = dialog.locator('.mantine-Card-root').filter({ hasText: MAIN_SOURCE });
  const mainName = mainCard.getByRole('textbox', { name: 'Name', exact: true });
  await mainCard.getByRole('radio', { name: 'Import as a separate copy' }).check();
  await expect(mainName).toHaveValue(`${MAIN_SOURCE} (copy)`);
  await mainCard.getByRole('radio', { name: /^Replace / }).check();
  await expect(mainName).toHaveValue(MAIN_SOURCE);
  await mainCard.getByRole('radio', { name: 'Import as a separate copy' }).check();

  const lookupCard2 = dialog.locator('.mantine-Card-root').filter({ hasText: LOOKUP_SOURCE });
  await lookupCard2.getByRole('radio', { name: 'Import as a separate copy' }).check();

  await next.click();                                     // -> Sinks
  await dialog.locator('.mantine-Card-root').filter({ hasText: SINK_NAME })
    .getByRole('radio', { name: 'Import as a separate copy' }).check();

  await next.click();                                     // -> Nodes
  await next.click();                                     // -> Review

  // Four creates: nothing is replaced when everything is copied.
  await expect(dialog.getByRole('row').filter({ hasText: 'replace' })).toHaveCount(0);
  await dialog.getByRole('button', { name: 'Import workflow' }).click();
  await expect(page.getByText(/is ready\. It arrives stopped/i)).toBeVisible({ timeout: 15000 });

  // The originals are untouched.
  for (const id of [ids.mainID, ids.lookupID, ids.sinkID, ids.wfID]) {
    const res = await apiRequest(page, `/api/sources/${id}`);
    const alt = res.status === 200 ? res : await apiRequest(page, `/api/sinks/${id}`);
    const final = alt.status === 200 ? alt : await apiRequest(page, `/api/workflows/${id}`);
    expect(final.status, `the original ${id} should still be here`).toBe(200);
  }

  // The copy is a second workflow, and its lookup points at the copied source.
  const list = await apiRequest(page, `/api/workflows?limit=200&search=${encodeURIComponent(WF_NAME)}`);
  const workflows = JSON.parse(list.body).data as any[];
  const copy = workflows.find((w) => w.id !== ids.wfID && w.name.includes(WF_NAME));
  expect(copy, 'the copy should be a second workflow, not a replacement').toBeTruthy();

  const lookupNode = copy.nodes.find((n: any) => n.config?.transType === 'db_lookup');
  expect(lookupNode.config.sourceId,
    'the copied db_lookup must point at the copied source, not the original')
    .not.toBe(ids.lookupID);

  const pointed = await apiRequest(page, `/api/sources/${lookupNode.config.sourceId}`);
  expect(pointed.status, 'the id the copied lookup points at must exist').toBe(200);
  // sources.name is UNIQUE, so a copy cannot keep the original's name — the
  // wizard suggests the nearest free one when "copy" is chosen.
  expect(JSON.parse(pointed.body).name).toBe(`${LOOKUP_SOURCE} (copy)`);

  const sourceNode = copy.nodes.find((n: any) => n.type === 'source');
  expect(sourceNode.ref_id).not.toBe(ids.mainID);
  expect((await apiRequest(page, `/api/sources/${sourceNode.ref_id}`)).status).toBe(200);

  const sinkNode = copy.nodes.find((n: any) => n.type === 'sink');
  expect(sinkNode.ref_id).not.toBe(ids.sinkID);
  expect((await apiRequest(page, `/api/sinks/${sinkNode.ref_id}`)).status).toBe(200);
});
