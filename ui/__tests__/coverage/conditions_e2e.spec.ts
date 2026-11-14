/**
 * Scenario K: a function on a field, compared, in an If and a Switch.
 *
 * Gembit's report: an If node on `array_len(source.users) > 0` "is always
 * false" although the list has one entry. The engine answers true -- in Live
 * Preview's request, in the Test simulation and in a running workflow, all
 * three checked here. What showed false was the drawer: Live Preview dropped
 * the branch and said the node "returned the message unchanged", which reads
 * as a condition that did not hold. Two neighbouring mistakes really are
 * always false, and the editor now says so beside the field:
 *
 *  - a function name that does not exist (`array_length`) gives nothing;
 *  - a bare field inside a call (`array_len(users)`) is the text "users".
 *
 * K1  If drawer: the condition is built the way a user builds it (type the
 *     field, Insert function, pick the operator, type the value) and Live
 *     Preview shows the branch taken, true and then false.
 * K2  The two traps show a warning; the correct expression shows none.
 * K3  Switch drawer: the same expression routes to the case that holds.
 * K4  Test simulation takes the true path on the stored sample.
 * K5  A running workflow delivers a record with users to the true sink and
 *     one without to the false sink.
 * K6  A source sample stored as a list of rows: Live Preview and Test run on
 *     the newest row and take the true branch. Before, Preview read the list
 *     as {"0": row} and went false, and Test was refused.
 */
import { test, expect, type Page } from '@playwright/test';
import {
  api,
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

const SAMPLE = { id: 7, users: [{ id: 1, name: 'Ada' }] };

const cleanup = new Cleanup();
test.afterEach(async () => cleanup.run());

/** Source (with SAMPLE stored) -> `node` -> two stdout sinks on its branches. */
async function flowAround(
  page: Page,
  node: Record<string, unknown>,
  branches: string[],
  sinkFor?: (branch: string) => Promise<{ id: string }>,
  sample: unknown = SAMPLE,
) {
  const tag = stamp();
  const hook = webhookPath(`cov-cond-${tag}`);
  const src = await createSource(page, cleanup, { name: `cov-cond-src-${tag}`, type: 'webhook', config: { path: hook } });
  await api(page, 'PUT', `/api/sources/${src.id}/sample`, { sample: JSON.stringify(sample) });
  const nodes: Record<string, unknown>[] = [
    { id: 'n-src', type: 'source', ref_id: src.id, x: 40, y: 200, config: { label: 'Signups' } },
    { id: 'n-logic', x: 360, y: 200, ...node },
  ];
  const edges: Record<string, unknown>[] = [{ id: 'e-in', source_id: 'n-src', target_id: 'n-logic' }];
  let y = 40;
  for (const branch of branches) {
    const sink = sinkFor
      ? await sinkFor(branch)
      : await createSink(page, cleanup, { name: `cov-cond-${branch}-${tag}`, type: 'stdout' });
    nodes.push({ id: `n-${branch}`, type: 'sink', ref_id: sink.id, x: 720, y, config: { label: `${branch} sink` } });
    edges.push({
      id: `e-${branch}`,
      source_id: 'n-logic',
      target_id: `n-${branch}`,
      source_handle: branch,
      config: { label: branch },
    });
    y += 220;
  }
  const wf = await createWorkflow(page, cleanup, { name: `cov-cond-wf-${tag}`, nodes, edges });
  return { workflowId: wf.id, hook };
}

async function openNode(page: Page, workflowId: string, nodeId: string) {
  await page.goto(`/workflows/${workflowId}/edit`);
  const node = page.getByTestId(`rf__node-${nodeId}`);
  await expect(node).toBeVisible({ timeout: 20000 });
  await node.dblclick();
  const drawer = page.getByRole('dialog').last();
  await expect(drawer.getByTestId('live-preview')).toBeVisible({ timeout: 20000 });
  return drawer;
}

async function runPreview(page: Page, drawer: ReturnType<Page['getByRole']>) {
  const answered = page.waitForResponse((r) => r.url().endsWith('/api/transformations/test'), { timeout: 20000 });
  await drawer.getByRole('button', { name: 'Run Preview' }).click();
  const res = await answered;
  expect(res.status(), await res.text()).toBe(200);
  return res.json();
}

test('K1+K2: If on array_len(source.users) > 0, built in the drawer, shows the branch it takes', async ({ page }) => {
  const watch = watchErrors(page);
  await login(page);
  // Saved with a placeholder condition: a condition node needs one to save.
  const { workflowId } = await flowAround(
    page,
    { type: 'condition', config: { label: 'Has users?', conditions: [{ field: 'id', operator: '=', value: '7' }] } },
    ['true', 'false'],
  );
  const drawer = await openNode(page, workflowId, 'n-logic');

  // The field the way a user writes it: the field's name, then a function.
  const field = drawer.getByRole('combobox', { name: 'Condition 1 field' });
  await field.fill('users');
  await page.keyboard.press('Escape'); // close the field's suggestions, not the drawer
  await expect(drawer).toBeVisible();
  await drawer.getByRole('button', { name: 'Insert function' }).first().click();
  const picker = page
    .locator('.mantine-Popover-dropdown')
    .filter({ has: page.getByRole('textbox', { name: 'Search functions' }) });
  await picker.getByRole('textbox', { name: 'Search functions' }).fill('array_len');
  await picker.getByRole('button', { name: /^Insert array_len\(/ }).click();
  await expect(field).toHaveValue('array_len(source.users)');
  await expect(drawer.getByTestId('expression-warning')).toHaveCount(0);

  // Operator and value.
  await drawer.getByRole('combobox', { name: 'Condition 1 operator' }).click();
  await page.getByRole('option', { name: '>', exact: true }).click();
  const value = drawer.getByRole('textbox', { name: 'Condition 1 value' });
  await value.fill('0');

  let out = await runPreview(page, drawer);
  expect(out.branch, 'the engine said').toBe('true');
  const shown = drawer.getByTestId('preview-branch');
  await expect(shown, 'Live Preview does not show the branch taken').toBeVisible();
  await expect(shown).toContainText('true');
  await expect(drawer.getByTestId('live-preview')).not.toContainText('returned the message unchanged');

  // And false, when it does not hold.
  await value.fill('5');
  out = await runPreview(page, drawer);
  expect(out.branch).toBe('false');
  await expect(shown).toContainText('false');

  // K2: the two ways it really is always false are named beside the field.
  await field.fill('array_len(users)');
  await expect(drawer.getByTestId('expression-warning')).toContainText('source.users');
  await field.fill('array_length(source.users)');
  await expect(drawer.getByTestId('expression-warning')).toContainText('There is no function named array_length');
  await expect(drawer.getByTestId('expression-warning')).toContainText('array_len');
  await field.fill('array_len(source.users)');
  await expect(drawer.getByTestId('expression-warning')).toHaveCount(0);
  watch.expectClean('If drawer');
});

test('K3: Switch on array_len(source.users) routes to the case that holds', async ({ page }) => {
  const watch = watchErrors(page);
  await login(page);
  const { workflowId } = await flowAround(
    page,
    {
      type: 'switch',
      config: {
        label: 'How many users?',
        field: 'array_len(source.users)',
        cases: [
          { label: 'none', operator: '=', value: '0' },
          { label: 'has-users', operator: '>', value: '0' },
        ],
      },
    },
    ['none', 'has-users', 'default'],
  );
  const drawer = await openNode(page, workflowId, 'n-logic');
  await expect(drawer.getByTestId('expression-warning')).toHaveCount(0);
  const out = await runPreview(page, drawer);
  expect(out.branch).toBe('has-users');
  await expect(drawer.getByTestId('preview-branch')).toContainText('has-users');

  // The Switch field opens as a structured call: the function, and a field
  // (not text) as its argument -- the shape that reads source.users.
  await expect(drawer.getByRole('combobox', { name: 'Switch field type' })).toHaveValue('Function');
  await expect(drawer.getByRole('combobox', { name: 'Switch field function' })).toHaveValue('array_len');
  await expect(drawer.getByRole('combobox', { name: 'Switch field › list type' })).toHaveValue('Field');
  await expect(drawer.getByRole('combobox', { name: 'Switch field › list field' })).toHaveValue('users');
  watch.expectClean('Switch drawer');
});

test('K4: the Test simulation takes the true path for array_len(source.users) > 0', async ({ page }) => {
  await login(page);
  const { workflowId } = await flowAround(
    page,
    { type: 'condition', config: { label: 'Has users?', conditions: [{ field: 'array_len(source.users)', operator: '>', value: '0' }] } },
    ['true', 'false'],
  );
  await page.goto(`/workflows/${workflowId}/edit`);
  await expect(page.getByTestId('rf__node-n-true')).toBeVisible({ timeout: 20000 });
  const simulated = page.waitForResponse((r) => r.url().endsWith('/api/workflows/test') && r.request().method() === 'POST');
  await page.getByRole('button', { name: 'Test', exact: true }).click();
  expect((await simulated).status()).toBe(200);
  const edge = (id: string) => page.getByTestId(`rf__edge-${id}`).locator('path.react-flow__edge-path');
  await expect(edge('e-true')).toHaveAttribute('data-simulation', 'taken');
  await expect(edge('e-false')).toHaveAttribute('data-simulation', 'untaken');
  await expect(page.getByTestId('rf__node-n-logic').getByText(/^Simulation:/)).toHaveText(/took branch "true"/);
});

test('K5: a running workflow sends a record with users down true, and one without down false', async ({ page }) => {
  await login(page);
  const receiver = new Receiver();
  const base = await receiver.start();
  cleanup.add(() => receiver.stop());
  const tag = stamp();
  const { workflowId, hook } = await flowAround(
    page,
    { type: 'condition', config: { label: 'Has users?', conditions: [{ field: 'array_len(source.users)', operator: '>', value: '0' }] } },
    ['true', 'false'],
    (branch) =>
      createSink(page, cleanup, {
        name: `cov-cond-live-${branch}-${tag}`,
        type: 'http',
        config: { url: `${base}/${branch}`, method: 'POST' },
      }),
  );
  await startWorkflow(page, workflowId);
  await postWebhook(page, hook, { id: 1, users: [{ id: 1 }] });
  await postWebhook(page, hook, { id: 2, users: [] });
  const yes = await receiver.waitFor('/true');
  const no = await receiver.waitFor('/false');
  expect(yes.map((d) => d.body.id), 'the record with one user').toEqual([1]);
  expect(no.map((d) => d.body.id), 'the record with none').toEqual([2]);
});

test('K6: a sample stored as a list of rows is run on its newest row, in Preview and in Test', async ({ page }) => {
  const watch = watchErrors(page);
  await login(page);
  const { workflowId } = await flowAround(
    page,
    { type: 'condition', config: { label: 'Has users?', conditions: [{ field: 'array_len(source.users)', operator: '>', value: '0' }] } },
    ['true', 'false'],
    undefined,
    [{ id: 6, users: [] }, SAMPLE],
  );
  const drawer = await openNode(page, workflowId, 'n-logic');
  const out = await runPreview(page, drawer);
  expect(out.branch, 'Live Preview did not run on one record').toBe('true');
  await page.keyboard.press('Escape');

  const simulated = page.waitForResponse((r) => r.url().endsWith('/api/workflows/test') && r.request().method() === 'POST');
  await page.getByRole('button', { name: 'Test', exact: true }).click();
  const res = await simulated;
  expect(res.status(), `the simulation was refused: ${await res.text()}`).toBe(200);
  const edge = (id: string) => page.getByTestId(`rf__edge-${id}`).locator('path.react-flow__edge-path');
  await expect(edge('e-true')).toHaveAttribute('data-simulation', 'taken');
  await expect(edge('e-false')).toHaveAttribute('data-simulation', 'untaken');
  watch.expectClean('list-shaped sample');
});
