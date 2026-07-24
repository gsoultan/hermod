import { test, expect } from '@playwright/test';
import { Client } from 'pg';

test.describe('Postgres CDC E2E workflow Comprehensive', () => {
  const sourceDB = 'hermod_test_source';
  const sinkDB = 'hermod_test_sink';
  const dbConfig = {
    host: 'localhost',
    port: 5432,
    user: 'postgres',
    password: 'postgres',
  };

  test.beforeEach(async ({ page }) => {
    // 1. Login
    console.log('Navigating to login...');
    await page.goto('http://localhost:5173/login');
    await page.getByLabel('Username').first().fill('admin');
    await page.locator('input[type="password"]').fill('admin123');
    await page.getByRole('button', { name: 'Login' }).click();
    await expect(page).toHaveURL('http://localhost:5173/', { timeout: 20000 });
    console.log('Login successful');
  });

  test('Scenario 1-3: Lifecycle, UI Features, and Basic CDC', async ({ page }) => {
    test.setTimeout(300000);
    const timestamp = Date.now();
    const vhostName = `vhost_life_${timestamp}`;

    // Create VHost via API
    await page.evaluate(async (name) => {
      const token = localStorage.getItem('hermod_token');
      await fetch('/api/vhosts', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` },
        body: JSON.stringify({ name, description: 'E2E Lifecycle VHost' })
      });
    }, vhostName);

    // 2. Create Source via UI (Testing Test Connection, Simulation)
    console.log('Creating source via UI...');
    await page.goto('http://localhost:5173/sources');
    await page.getByRole('button', { name: 'Add Source' }).click();
    await page.getByLabel('Name').fill(`Postgres Source E2E ${timestamp}`);
    await page.getByLabel('VHost').click();
    await page.getByRole('option', { name: vhostName }).click();
    await page.getByLabel('Type').click();
    await page.getByRole('option', { name: 'Postgres' }).click();
    await page.getByRole('button', { name: 'Next Step' }).click();

    await page.getByLabel('Host').fill('localhost');
    await page.getByLabel('Port').fill('5432');
    await page.getByLabel('User').fill('postgres');
    await page.getByLabel('Password').fill('postgres');
    await page.getByLabel('Database').fill(sourceDB);
    await page.getByLabel('Tables (comma separated)').fill('users');
    await page.getByLabel('Use CDC').click();
    await page.getByLabel('Slot Name').fill(`slot_${timestamp}`);
    await page.getByLabel('Publication Name').fill(`pub_${timestamp}`);

    console.log('Testing source connection...');
    await page.getByRole('button', { name: 'Test Connection' }).click();
    await expect(page.locator('.mantine-Alert-title')).toContainText('Connected', { timeout: 10000 });

    console.log('Running source simulation...');
    await page.getByRole('button', { name: 'Simulate' }).click();
    await expect(page.locator('text=Simulation Result')).toBeVisible({ timeout: 10000 });
    await page.keyboard.press('Escape');

    await page.getByRole('button', { name: 'Next Step' }).click();
    await page.getByRole('button', { name: 'Next Step' }).click();
    
    const sourceCreatePromise = page.waitForResponse(r => r.url().includes('/api/sources') && r.request().method() === 'POST');
    await page.getByRole('button', { name: 'Create Source' }).click();
    const sourceID = (await (await sourceCreatePromise).json()).id;

    // 3. Create Sink via UI (Testing PgBouncer, Smart Map, Truncate)
    console.log('Creating sink via UI...');
    await page.goto('http://localhost:5173/sinks');
    await page.getByRole('button', { name: 'Add Sink' }).click();
    await page.getByLabel('Sink Name').fill(`Postgres Sink E2E ${timestamp}`);
    await page.getByLabel('VHost').click();
    await page.getByRole('option', { name: vhostName }).click();
    await page.getByLabel('Type').click();
    await page.getByRole('option', { name: 'Postgres' }).click();
    await page.getByRole('button', { name: 'Next Step' }).click();

    // Use PgBouncer marker
    await page.getByLabel('OR Connection String').fill(`postgres://postgres:postgres@localhost:5432/${sinkDB}?pgbouncer=true`);
    await page.getByRole('button', { name: 'Test Connection' }).click();
    await expect(page.locator('.mantine-Alert-title')).toContainText('Connected', { timeout: 10000 });

    await page.getByLabel('Target Table').fill('users_sink');
    await page.getByLabel('Use existing table').click();
    await page.getByRole('button', { name: 'Smart Map from Sink' }).click();
    await expect(page.locator('table >> text=full_name')).toBeVisible();
    await page.getByLabel('Truncate Table (on start)').click();

    await page.getByRole('button', { name: 'Next Step' }).click();
    await page.getByRole('button', { name: 'Next Step' }).click();
    
    const sinkCreatePromise = page.waitForResponse(r => r.url().includes('/api/sinks') && r.request().method() === 'POST');
    await page.getByRole('button', { name: 'Create Sink' }).click();
    const sinkID = (await (await sinkCreatePromise).json()).id;

    // 4. Test Workflow Start/Stop
    console.log('Testing workflow lifecycle...');
    const workflow = {
      name: `Lifecycle Workflow ${timestamp}`,
      vhost: vhostName,
      active: true,
      nodes: [
        { id: 'src', type: 'source', ref_id: sourceID, x: 100, y: 100, config: { type: 'postgres' } },
        { id: 'trans', type: 'transformation', config: { transType: 'mapping', mapping: { id: '$.id', full_name: '$.name', email: '$.email' } }, x: 300, y: 100 },
        { id: 'snk', type: 'sink', ref_id: sinkID, x: 500, y: 100, config: { type: 'postgres' } }
      ],
      edges: [{ id: 'e1', source_id: 'src', target_id: 'trans' }, { id: 'e2', source_id: 'trans', target_id: 'snk' }]
    };

    await page.evaluate(async (wf) => {
      const token = localStorage.getItem('hermod_token');
      await fetch('/api/workflows', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` },
        body: JSON.stringify(wf)
      });
    }, workflow);

    await page.goto('http://localhost:5173/workflows');
    const wfRow = page.locator('tr', { hasText: workflow.name });
    await wfRow.locator('button[aria-label="Stop workflow"]').click();
    await expect(wfRow.locator('text=Inactive')).toBeVisible();
    await wfRow.locator('button[aria-label="Start workflow"]').click();
    await expect(wfRow.locator('text=Active')).toBeVisible();

    // Verify data flow
    const client = new Client({ ...dbConfig, database: sourceDB });
    await client.connect();
    const testName = `User ${timestamp}`;
    await client.query("INSERT INTO users (name, email) VALUES ($1, $2)", [testName, `user-${timestamp}@example.com`]);
    await client.end();

    const sinkClient = new Client({ ...dbConfig, database: sinkDB });
    await sinkClient.connect();
    let rows = [];
    for (let i = 0; i < 30; i++) {
      const res = await sinkClient.query("SELECT * FROM users_sink WHERE full_name = $1", [testName]);
      rows = res.rows;
      if (rows.length > 0) break;
      await new Promise(r => setTimeout(r, 1000));
    }
    expect(rows.length).toBe(1);
    await sinkClient.end();
  });

  test('Scenario 4-7: Advanced Transformations and Multi-node Workflow', async ({ page }) => {
    test.setTimeout(400000);
    const timestamp = Date.now();
    const vhostName = `vhost_adv_${timestamp}`;

    // Setup via API for speed
    const setupData = await page.evaluate(async ({ vhostName, timestamp, sourceDB, sinkDB }) => {
      const token = localStorage.getItem('hermod_token');
      const headers = { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` };

      await fetch('/api/vhosts', { method: 'POST', headers, body: JSON.stringify({ name: vhostName }) });

      const lookupSrc = await (await fetch('/api/sources', {
        method: 'POST', headers,
        body: JSON.stringify({
          name: `Lookup ${timestamp}`, vhost: vhostName, type: 'postgres',
          config: { host: 'localhost', port: '5432', user: 'postgres', password: 'postgres', dbname: sourceDB, tables: 'lookup_table', use_cdc: false }
        })
      })).json();

      const cdcSrc = await (await fetch('/api/sources', {
        method: 'POST', headers,
        body: JSON.stringify({
          name: `CDC ${timestamp}`, vhost: vhostName, type: 'postgres',
          config: { host: 'localhost', port: '5432', user: 'postgres', password: 'postgres', dbname: sourceDB, tables: 'users', use_cdc: true, slot_name: `sl_adv_${timestamp}`, publication_name: `pb_adv_${timestamp}` }
        })
      })).json();

      const sink1 = await (await fetch('/api/sinks', {
        method: 'POST', headers,
        body: JSON.stringify({
          name: `Sink 1 ${timestamp}`, vhost: vhostName, type: 'postgres',
          config: { connection_string: `postgres://postgres:postgres@localhost:5432/${sinkDB}`, table: 'users_sink_adv', use_existing_table: false }
        })
      })).json();

      const sink2 = await (await fetch('/api/sinks', {
        method: 'POST', headers,
        body: JSON.stringify({
          name: `Sink 2 ${timestamp}`, vhost: vhostName, type: 'postgres',
          config: { connection_string: `postgres://postgres:postgres@localhost:5432/${sinkDB}`, table: 'users_sink_adv_2', use_existing_table: false }
        })
      })).json();

      return { lookupSrcId: lookupSrc.id, cdcSrcId: cdcSrc.id, sink1Id: sink1.id, sink2Id: sink2.id, token };
    }, { vhostName, timestamp, sourceDB, sinkDB });

    // Create Advanced Multi-node Workflow
    const workflow = {
      name: `Advanced E2E ${timestamp}`,
      vhost: vhostName,
      active: true,
      nodes: [
        { id: 'src', type: 'source', ref_id: setupData.cdcSrcId, x: 50, y: 150 },
        { id: 'db_lookup', type: 'transformation', config: { transType: 'db_lookup', sourceId: setupData.lookupSrcId, table: 'lookup_table', keyColumn: 'user_name', keyField: '$.name', valueColumn: 'city', targetField: '$.city' }, x: 250, y: 150 },
        { id: 'api_lookup', type: 'transformation', config: { transType: 'api_lookup', method: 'GET', url: 'http://localhost:5173/api/vhosts', headers: JSON.stringify({ 'Authorization': `Bearer ${setupData.token}` }), responsePath: '$.[0].name', targetField: '$.vhost_info' }, x: 450, y: 150 },
        { id: 'snk1', type: 'sink', ref_id: setupData.sink1Id, x: 700, y: 50 },
        { id: 'snk2', type: 'sink', ref_id: setupData.sink2Id, x: 700, y: 250 }
      ],
      edges: [
        { id: 'e1', source_id: 'src', target_id: 'db_lookup' },
        { id: 'e2', source_id: 'db_lookup', target_id: 'api_lookup' },
        { id: 'e3', source_id: 'api_lookup', target_id: 'snk1' },
        { id: 'e4', source_id: 'api_lookup', target_id: 'snk2' }
      ]
    };

    await page.evaluate(async (wf) => {
      const token = localStorage.getItem('hermod_token');
      await fetch('/api/workflows', { method: 'POST', headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` }, body: JSON.stringify(wf) });
    }, workflow);

    // Testing Log Live Preview and Message Trace UI
    console.log('Testing Live Preview UI...');
    await page.goto('http://localhost:5173/workflows');
    await page.locator('tr', { hasText: workflow.name }).locator('button[aria-label="Edit workflow"]').click();
    await expect(page.locator('.react-flow__renderer')).toBeVisible();

    // Trigger data
    const sourceClient = new Client({ ...dbConfig, database: sourceDB });
    await sourceClient.connect();
    const testEmail = `adv-${timestamp}@example.com`;
    await sourceClient.query("INSERT INTO users (name, email) VALUES ($1, $2)", ['John Doe', testEmail]);
    await sourceClient.end();

    console.log('Waiting for logs and trace...');
    const logEntry = page.locator('.mantine-Table-tbody tr').first();
    await expect(logEntry).toBeVisible({ timeout: 60000 });
    await logEntry.click();
    await expect(page.locator('text=Trace').or(page.locator('text=Message Trace')).first()).toBeVisible({ timeout: 10000 });

    // Verify enriched data in both sinks
    const sinkClient = new Client({ ...dbConfig, database: sinkDB });
    await sinkClient.connect();
    let found1 = false;
    let found2 = false;
    for (let i = 0; i < 40; i++) {
        if (!found1) {
            const res1 = await sinkClient.query("SELECT * FROM users_sink_adv WHERE email = $1", [testEmail]);
            if (res1.rows.length > 0 && res1.rows[0].city === 'New York') found1 = true;
        }
        if (!found2) {
            const res2 = await sinkClient.query("SELECT * FROM users_sink_adv_2 WHERE email = $1", [testEmail]);
            if (res2.rows.length > 0 && res2.rows[0].city === 'New York') found2 = true;
        }
        if (found1 && found2) break;
        await new Promise(r => setTimeout(r, 2000));
    }
    expect(found1).toBe(true);
    expect(found2).toBe(true);
    await sinkClient.end();
  });
});
