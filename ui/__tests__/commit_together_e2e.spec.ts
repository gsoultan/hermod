import { test, expect, type Page } from '@playwright/test';
import { login, apiRequest } from './support/auth';

/**
 * Sink nodes that commit together, as the editor shows them.
 *
 * A workflow with two database sink nodes in one transaction is created
 * through the API, as a saved one looks. The canvas must name the transaction
 * on both members and on nobody else, and opening a member must show
 * "Commit together" on with the other member listed. The server's own refusal
 * — a transaction with one member cannot be saved — is checked through the API
 * the editor saves with, so the two cannot drift apart.
 */

const createdSinks: string[] = [];
const createdSources: string[] = [];
const createdWorkflows: string[] = [];

test.afterEach(async ({ page }) => {
  for (const id of createdWorkflows.splice(0)) {
    await apiRequest(page, `/api/workflows/${id}`, { method: 'DELETE' }).catch(() => {});
  }
  for (const id of createdSinks.splice(0)) {
    await apiRequest(page, `/api/sinks/${id}`, { method: 'DELETE' }).catch(() => {});
  }
  for (const id of createdSources.splice(0)) {
    await apiRequest(page, `/api/sources/${id}`, { method: 'DELETE' }).catch(() => {});
  }
});

async function post(page: Page, path: string, body: unknown): Promise<any> {
  const res = await apiRequest(page, path, { method: 'POST', body });
  if (res.status >= 400) {
    throw new Error(`fixture setup failed: POST ${path} -> ${res.status} ${res.body}`);
  }
  return JSON.parse(res.body);
}

/** A stored database sink record. Nothing connects to it in this test. */
const dbSink = (name: string, type: 'postgres' | 'mysql', table: string) => ({
  name, type, vhost: 'default',
  config: {
    connection_string: type === 'postgres'
      ? 'postgres://hermod:hermod@127.0.0.1:1/orders?sslmode=disable'
      : 'hermod:hermod@tcp(127.0.0.1:1)/finance',
    table,
  },
});

test.describe('commit together', () => {
  test.beforeEach(async ({ page }) => {
    page.on('pageerror', (err) => {
      throw new Error(`uncaught page error: ${err.message}`);
    });
    await login(page);
  });

  test('the canvas names the transaction on its members and the drawer shows who is in it', async ({ page }) => {
    test.setTimeout(120000);
    const stamp = Date.now();

    const source = await post(page, '/api/sources', {
      name: `ct-orders-${stamp}`, type: 'webhook', vhost: 'default', active: true,
      config: { path: `/ct-${stamp}` },
    });
    createdSources.push(source.id);
    const pg = await post(page, '/api/sinks', dbSink(`ct-orders-db-${stamp}`, 'postgres', 'orders'));
    const my = await post(page, '/api/sinks', dbSink(`ct-ledger-db-${stamp}`, 'mysql', 'ledger'));
    const out = await post(page, '/api/sinks', { name: `ct-console-${stamp}`, type: 'stdout', vhost: 'default', config: {} });
    createdSinks.push(pg.id, my.id, out.id);

    const workflow = await post(page, '/api/workflows', {
      name: `ct-wf-${stamp}`, vhost: 'default', active: false,
      nodes: [
        { id: 'n-src', type: 'source', ref_id: source.id, x: 40, y: 200, config: { label: 'Orders webhook', type: 'webhook' } },
        { id: 'n-pg', type: 'sink', ref_id: pg.id, x: 480, y: 80, config: { label: 'Orders DB', type: 'postgres', transaction: 'orders-ledger' } },
        { id: 'n-my', type: 'sink', ref_id: my.id, x: 480, y: 240, config: { label: 'Ledger DB', type: 'mysql', transaction: 'orders-ledger' } },
        { id: 'n-out', type: 'sink', ref_id: out.id, x: 480, y: 400, config: { label: 'Console', type: 'stdout' } },
      ],
      edges: [
        { id: 'e-pg', source_id: 'n-src', target_id: 'n-pg' },
        { id: 'e-my', source_id: 'n-src', target_id: 'n-my' },
        { id: 'e-out', source_id: 'n-src', target_id: 'n-out' },
      ],
    });
    createdWorkflows.push(workflow.id);

    await page.goto(`/workflows/${workflow.id}/edit`);
    await page.waitForLoadState('networkidle');
    await expect(page.locator('.react-flow__node')).toHaveCount(4, { timeout: 30000 });

    // Both members carry the name; the console sink does not.
    const badges = page.getByLabel('Commits together in transaction orders-ledger');
    await expect(badges).toHaveCount(2, { timeout: 15000 });
    await expect(page.locator('[data-id="n-out"]').getByLabel(/commits together/i)).toHaveCount(0);

    // Open the PostgreSQL member: the switch is on and the MySQL member is listed.
    await page.locator('[data-id="n-pg"]').dblclick();
    const section = page.getByTestId('commit-together');
    await expect(section).toBeVisible({ timeout: 20000 });
    await expect(section.getByRole('switch', { name: /commit together/i })).toBeChecked();
    await expect(section.getByText('Ledger DB · MySQL')).toBeVisible();
    await expect(section.getByText(/2 databases in “orders-ledger”/)).toBeVisible();
    // "Every database must get a row" is offered for the transaction, off by default.
    await expect(section.getByRole('switch', { name: /every database must get a row/i })).not.toBeChecked();
  });

  test('the server refuses to save a transaction with one member, naming the node', async ({ page }) => {
    test.setTimeout(60000);
    const stamp = Date.now();
    const source = await post(page, '/api/sources', {
      name: `ct-alone-src-${stamp}`, type: 'webhook', vhost: 'default', active: true,
      config: { path: `/ct-alone-${stamp}` },
    });
    createdSources.push(source.id);
    const pg = await post(page, '/api/sinks', dbSink(`ct-alone-db-${stamp}`, 'postgres', 'orders'));
    createdSinks.push(pg.id);

    const res = await apiRequest(page, '/api/workflows', {
      method: 'POST',
      body: {
        name: `ct-alone-${stamp}`, vhost: 'default', active: false,
        nodes: [
          { id: 'n-src', type: 'source', ref_id: source.id, x: 40, y: 200, config: { label: 'Orders webhook' } },
          { id: 'n-pg', type: 'sink', ref_id: pg.id, x: 480, y: 200, config: { label: 'Orders DB', transaction: 'solo' } },
        ],
        edges: [{ id: 'e-pg', source_id: 'n-src', target_id: 'n-pg' }],
      },
    });
    if (res.status < 400) {
      createdWorkflows.push(JSON.parse(res.body).id);
    }
    expect(res.status, 'a one-member transaction must be refused at save').toBeGreaterThanOrEqual(400);
    const message: string = JSON.parse(res.body).error;
    expect(message).toMatch(/transaction "solo"/);
    expect(message).toMatch(/Orders DB/);
  });
});
