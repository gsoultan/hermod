/**
 * Scenario W: what a user does to a flow in the editor, end to end.
 *
 * W1  Connect two steps by dragging between their handles, rename the flow,
 *     and Save: the stored flow has the new edge and the new name.
 * W2  Every save is a version; Version history lists them and Restore brings
 *     an earlier one back.
 * W3  Turn on from the editor, deliver a record through the flow to a live
 *     HTTP endpoint, Turn off.
 * W4  Clone makes an independent copy; Delete flow removes the flow.
 *
 * Detail tabs, the simulation overlay, export/import and list-level toggle and
 * delete are covered by workflow_surface_e2e, simulation_path_e2e and
 * workflow_export_import_e2e; this spec does not repeat them.
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
  watchErrors,
  webhookPath,
} from './helpers';

const cleanup = new Cleanup();
test.afterEach(async () => cleanup.run());

/** Source -> Set Fields, and a sink not yet connected to anything. */
async function halfBuiltFlow(page: Page, sinkBody?: Record<string, unknown>) {
  const tag = stamp();
  const hook = webhookPath(`cov-wf-${tag}`);
  const src = await createSource(page, cleanup, { name: `cov-wf-src-${tag}`, type: 'webhook', config: { path: hook } });
  await api(page, 'PUT', `/api/sources/${src.id}/sample`, { sample: JSON.stringify({ id: 1, name: 'Ada' }) });
  const snk = await createSink(page, cleanup, sinkBody ?? { name: `cov-wf-snk-${tag}`, type: 'stdout' });
  const name = `cov-wf-${tag}`;
  const wf = await createWorkflow(page, cleanup, {
    name,
    nodes: [
      { id: 'n-src', type: 'source', ref_id: src.id, x: 40, y: 160 },
      { id: 'n-set', type: 'transformation', x: 380, y: 160, config: { transType: 'set', 'column.seen': "'yes'" } },
      { id: 'n-snk', type: 'sink', ref_id: snk.id, x: 760, y: 160 },
    ],
    edges: [{ id: 'e1', source_id: 'n-src', target_id: 'n-set' }],
  });
  return { id: wf.id, name, hook };
}

async function openEditor(page: Page, id: string) {
  await page.goto(`/workflows/${id}/edit`);
  await expect(page.getByTestId('rf__node-n-snk')).toBeVisible({ timeout: 20000 });
}

/**
 * Drag from one node's output handle to another node's input handle, the way a
 * user connects two steps. The canvas fits itself to view as it opens, so a
 * drag that started during that animation can miss: try again until the edge
 * is there.
 */
async function connect(page: Page, from: string, to: string) {
  const edges = page.locator('.react-flow__edge');
  const before = await edges.count();
  for (let attempt = 0; attempt < 3 && (await edges.count()) === before; attempt++) {
    await page.waitForTimeout(500);
    await dragHandle(page, from, to);
  }
  await expect(edges, `${from} -> ${to} was not connected`).toHaveCount(before + 1);
}

async function dragHandle(page: Page, from: string, to: string) {
  const out = page.getByTestId(`rf__node-${from}`).locator('.react-flow__handle.source').first();
  const into = page.getByTestId(`rf__node-${to}`).locator('.react-flow__handle.target').first();
  const a = (await out.boundingBox())!;
  const b = (await into.boundingBox())!;
  await page.mouse.move(a.x + a.width / 2, a.y + a.height / 2);
  await page.mouse.down();
  await page.mouse.move((a.x + b.x) / 2, (a.y + b.y) / 2, { steps: 8 });
  await page.mouse.move(b.x + b.width / 2, b.y + b.height / 2, { steps: 8 });
  await page.mouse.up();
}

async function save(page: Page) {
  const saved = page.waitForResponse(
    (r) => /\/api\/workflows\/[^/]+$/.test(new URL(r.url()).pathname) && r.request().method() === 'PUT',
    { timeout: 15000 },
  );
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  const res = await saved;
  expect(res.status(), `Save was refused: ${await res.text()}`).toBeLessThan(300);
}

async function moreMenu(page: Page, item: string) {
  await page.getByRole('button', { name: 'More', exact: true }).click();
  await page.getByRole('menuitem', { name: item }).click();
}

test('W1+W2: connect, rename, save; every save is a version and Restore brings one back', async ({ page }) => {
  const watch = watchErrors(page);
  await login(page);
  const flow = await halfBuiltFlow(page);
  await openEditor(page, flow.id);

  // W1
  await connect(page, 'n-set', 'n-snk');
  const renamed = `${flow.name}-renamed`;
  await page.getByRole('textbox', { name: 'Flow name' }).fill(renamed);
  await save(page);
  const stored = await api(page, 'GET', `/api/workflows/${flow.id}`);
  expect(stored.name).toBe(renamed);
  expect(
    stored.edges.some((e: any) => e.source_id === 'n-set' && e.target_id === 'n-snk'),
    'the dragged connection was not saved',
  ).toBe(true);

  // A second save, so there are two versions to choose between.
  const second = `${flow.name}-second`;
  await page.getByRole('textbox', { name: 'Flow name' }).fill(second);
  await save(page);

  // W2
  await moreMenu(page, 'Version history');
  const history = page.getByRole('dialog', { name: /Workflow Version History/ });
  await expect(history).toBeVisible();
  const rows = history.locator('tbody tr');
  await expect.poll(() => rows.count(), { message: 'two saves made fewer than two versions' }).toBeGreaterThanOrEqual(2);
  const versions = await api(page, 'GET', `/api/workflows/${flow.id}/versions`).catch(() => null);
  // Newest first or not, the one to restore is the version saved as `renamed`.
  // Restore the version saved under the first new name.
  const target = Array.isArray(versions)
    ? versions.find((v: any) => JSON.stringify(v).includes(renamed) && !JSON.stringify(v).includes(second))
    : undefined;
  const row = target ? rows.filter({ hasText: `v${target.version}` }).first() : rows.nth(1);
  const restored = page.waitForResponse((r) => r.url().includes(`/api/workflows/${flow.id}/rollback/`), { timeout: 15000 });
  await row.getByRole('button', { name: 'Restore' }).click();
  // Asked over the history dialog, so named rather than "the dialog".
  await page.getByRole('dialog', { name: 'Roll back workflow' }).getByRole('button', { name: 'Roll back' }).click();
  expect((await restored).status()).toBeLessThan(300);
  await expect
    .poll(async () => (await api(page, 'GET', `/api/workflows/${flow.id}`)).name, { message: 'Restore did not bring the version back' })
    .toEqual(target ? renamed : expect.not.stringMatching(/-second$/));
  watch.expectClean('editor save and version history');
});

test('W3: Turn on from the editor, a record flows to a live endpoint, Turn off', async ({ page }) => {
  await login(page);
  const receiver = new Receiver();
  const base = await receiver.start();
  cleanup.add(() => receiver.stop());
  const tag = stamp();
  const flow = await halfBuiltFlow(page, {
    name: `cov-wf-http-${tag}`,
    type: 'http',
    config: { url: `${base}/w3`, method: 'POST' },
  });
  await openEditor(page, flow.id);
  await connect(page, 'n-set', 'n-snk');
  await save(page);

  await page.getByRole('button', { name: 'Turn on' }).click();
  await expect(page.getByRole('button', { name: 'Turn off' })).toBeVisible({ timeout: 30000 });
  await expect
    .poll(async () => (await api(page, 'GET', `/api/workflows/${flow.id}`)).status, { timeout: 30000 })
    .toMatch(/active|running/i);

  await postWebhook(page, flow.hook, { id: 42, name: 'Grace' });
  const got = await receiver.waitFor('/w3');
  expect(got, 'nothing was delivered').toHaveLength(1);
  expect(got[0].body).toMatchObject({ id: 42, name: 'Grace', seen: 'yes' });

  await page.getByRole('button', { name: 'Turn off' }).click();
  await acceptConfirm(page).catch(() => {});
  await expect(page.getByRole('button', { name: 'Turn on' })).toBeVisible({ timeout: 30000 });
  expect((await api(page, 'GET', `/api/workflows/${flow.id}`)).active).toBe(false);
});

test('W4: Clone makes an independent copy, and Delete flow removes the flow', async ({ page }) => {
  const watch = watchErrors(page);
  await login(page);
  const flow = await halfBuiltFlow(page);
  await openEditor(page, flow.id);

  await moreMenu(page, 'Clone');
  await page.waitForURL(/\/workflows(\?|$)/, { timeout: 15000 });
  const list = await api(page, 'GET', `/api/workflows?search=${encodeURIComponent(flow.name)}&limit=50`);
  const all: any[] = Array.isArray(list) ? list : list.data ?? [];
  const copy = all.find((w) => w.name === `${flow.name} (Copy)`);
  expect(copy, 'the clone is not in the flows list').toBeTruthy();
  cleanup.add(() => apiRaw(page, 'DELETE', `/api/workflows/${copy.id}`));
  expect(copy.id).not.toBe(flow.id);
  const original = await api(page, 'GET', `/api/workflows/${flow.id}`);
  const cloned = await api(page, 'GET', `/api/workflows/${copy.id}`);
  expect(cloned.nodes.length).toBe(original.nodes.length);
  expect(cloned.active).toBe(false);

  await openEditor(page, copy.id);
  await moreMenu(page, 'Delete flow');
  await acceptConfirm(page);
  await page.waitForURL(/\/workflows(\?|$)/, { timeout: 15000 });
  expect((await apiRaw(page, 'GET', `/api/workflows/${copy.id}`)).status).toBe(404);
  // The original is untouched.
  expect((await apiRaw(page, 'GET', `/api/workflows/${flow.id}`)).status).toBe(200);
  watch.expectClean('clone and delete');
});
