import { test, expect } from '@playwright/test';
import { Client } from 'pg';

test.describe('Hermod Production Grade E2E', () => {
  const sourceDB = 'hermod_test_source';
  const sinkDB = 'hermod_test_sink';
  const dbConfig = {
    host: '127.0.0.1',
    port: 5432,
    user: 'postgres',
    password: 'postgres',
  };

  test.beforeEach(async ({ page }) => {
    page.on('console', msg => console.log('BROWSER:', msg.text()));
    // Login
    await page.goto('http://localhost:5175/login');
    await page.getByPlaceholder('Your username').fill('admin');
    await page.getByPlaceholder('Your password').fill('admin123');
    await page.getByRole('button', { name: 'Sign In' }).click();
    await expect(page).toHaveURL('http://localhost:5175/', { timeout: 30000 });
  });

  test('End-to-End Pipeline: Multi-Source, Multi-Sink (Sequential/Parallel), Advanced Transforms, and Observability', async ({ page }) => {
    test.setTimeout(300000);
    const timestamp = Date.now();
    const vhostName = `prod_vhost_${timestamp}`;

    // 1. Setup Infrastructure via evaluate (faster)
    const setupData = await page.evaluate(async ({ vhostName, timestamp, sourceDB, sinkDB }) => {
      const token = localStorage.getItem('hermod_token');
      const headers = { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` };

      await fetch('/api/vhosts', { method: 'POST', headers, body: JSON.stringify({ name: vhostName }) });

      const lookupSrc = await (await fetch('/api/sources', {
        method: 'POST', headers,
        body: JSON.stringify({
          name: `Lookup ${timestamp}`, vhost: vhostName, type: 'postgres',
          config: { host: '127.0.0.1', port: '5432', user: 'postgres', password: 'postgres', dbname: sourceDB, tables: 'lookup_table', use_cdc: 'false' }
        })
      })).json();

      const cdcSrc = await (await fetch('/api/sources', {
        method: 'POST', headers,
        body: JSON.stringify({
          name: `CDC ${timestamp}`, vhost: vhostName, type: 'postgres',
          config: { host: '127.0.0.1', port: '5432', user: 'postgres', password: 'postgres', dbname: sourceDB, tables: 'users', use_cdc: 'true', slot_name: `prod_sl_${timestamp}`, publication_name: `prod_pb_${timestamp}`, slot_reclaim: 'true' }
        })
      })).json();

      const sinkSeq = await (await fetch('/api/sinks', {
        method: 'POST', headers,
        body: JSON.stringify({
          name: `Sink Sequential ${timestamp}`, vhost: vhostName, type: 'postgres',
          config: { host: '127.0.0.1', port: '5432', user: 'postgres', password: 'postgres', dbname: sinkDB, table: 'prod_sink_seq', sequential: 'true' }
        })
      })).json();

      const sinkPar = await (await fetch('/api/sinks', {
        method: 'POST', headers,
        body: JSON.stringify({
          name: `Sink Parallel ${timestamp}`, vhost: vhostName, type: 'postgres',
          config: { host: '127.0.0.1', port: '5432', user: 'postgres', password: 'postgres', dbname: sinkDB, table: 'prod_sink_par' }
        })
      })).json();

      return { lookupSrcId: lookupSrc.id, cdcSrcId: cdcSrc.id, sinkSeqId: sinkSeq.id, sinkParId: sinkPar.id, token };
    }, { vhostName, timestamp, sourceDB, sinkDB });

    // 2. Build Complex Workflow
    await page.goto('http://localhost:5175/workflows/new');
    await page.getByLabel('Workflow Name').fill(`Prod Workflow ${timestamp}`);
    await page.getByLabel('Virtual Host').selectOption(vhostName);
    await page.getByRole('button', { name: 'Create Workflow' }).click();

    await page.waitForURL(/\/workflows\/[a-z0-9-]+/);
    const workflowURL = page.url();
    const workflowId = workflowURL.split('/').pop()!;

    // Add nodes via API to save time in UI interaction, then reload to verify UI
    await page.evaluate(async ({ workflowId, setupData, timestamp }) => {
      const token = localStorage.getItem('hermod_token');
      const headers = { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` };
      
      const workflow = await (await fetch(`/api/workflows/${workflowId}`, { headers })).json();
      
      workflow.nodes = [
        { id: 'src1', type: 'source', ref_id: setupData.cdcSrcId, x: 50, y: 150 },
        { id: 't1_lua', type: 'transformation', config: { transType: 'lua', script: 'msg.processed_by = "lua"; return msg' }, x: 250, y: 150 },
        { id: 't2_db', type: 'transformation', config: { transType: 'db_lookup', sourceId: setupData.lookupSrcId, table: 'lookup_table', keyColumn: 'user_name', keyField: '$.name', valueColumn: 'city', targetField: '$.city' }, x: 450, y: 150 },
        { id: 't3_map', type: 'transformation', config: { transType: 'mapping', mapping: { id: '$.id', name: '$.name', city: '$.city', email: '$.email', p: '$.processed_by' } }, x: 650, y: 150 },
        { id: 'snk_seq', type: 'sink', ref_id: setupData.sinkSeqId, x: 850, y: 50 },
        { id: 'snk_par', type: 'sink', ref_id: setupData.sinkParId, x: 850, y: 250 },
      ];
      workflow.edges = [
        { id: 'e1', source_id: 'src1', target_id: 't1_lua' },
        { id: 'e2', source_id: 't1_lua', target_id: 't2_db' },
        { id: 'e3', source_id: 't2_db', target_id: 't3_map' },
        { id: 'e4', source_id: 't3_map', target_id: 'snk_seq' },
        { id: 'e5', source_id: 't3_map', target_id: 'snk_par' },
      ];

      await fetch(`/api/workflows/${workflowId}`, {
        method: 'PUT',
        headers,
        body: JSON.stringify(workflow)
      });
    }, { workflowId, setupData, timestamp });

    await page.reload();
    await expect(page.locator('.react-flow__node')).toHaveCount(6);

    // 3. Start Workflow
    await page.getByRole('button', { name: 'Start' }).click();
    await expect(page.getByRole('button', { name: 'Stop' })).toBeVisible({ timeout: 20000 });

    // 4. Inject Data and Verify Flow
    console.log('Waiting for CDC readiness...');
    await new Promise(r => setTimeout(r, 10000));

    const sourceClient = new Client({ ...dbConfig, database: sourceDB });
    await sourceClient.connect();
    await sourceClient.query("INSERT INTO users (name, email) VALUES ($1, $2)", ['JOHN DOE', 'john@prod.io']);
    await sourceClient.end();

    // 5. Verify Sinks
    const sinkClient = new Client({ ...dbConfig, database: sinkDB });
    await sinkClient.connect();
    
    // Check Sequential Sink
    let sinkDataSeq = null;
    for (let i = 0; i < 10; i++) {
        const res = await sinkClient.query("SELECT * FROM prod_sink_seq WHERE name = 'JOHN DOE'");
        if (res.rows.length > 0) {
            sinkDataSeq = res.rows[0];
            break;
        }
        await new Promise(r => setTimeout(r, 2000));
    }
    expect(sinkDataSeq).not.toBeNull();
    expect(sinkDataSeq.city).toBe('New York');
    expect(sinkDataSeq.email).toBe('john@prod.io');

    // Check Parallel Sink
    const sinkResPar = await sinkClient.query("SELECT * FROM prod_sink_par WHERE name = 'JOHN DOE'");
    expect(sinkResPar.rows.length).toBeGreaterThan(0);
    await sinkClient.end();

    // 6. Verify Observability: Traces
    await page.locator('button:has-text("Message Traces")').click();
    // Wait for trace list to populate
    const traceItem = page.locator('button').filter({ hasText: /JOHN DOE/i }).or(page.locator('button').filter({ hasText: /john@prod.io/i }));
    await expect(traceItem.first()).toBeVisible({ timeout: 20000 });
    await traceItem.first().click();

    // Verify journey steps
    await expect(page.getByText(/Node: src1/i)).toBeVisible();
    await expect(page.getByText(/Node: t1_lua/i)).toBeVisible();
    await expect(page.getByText(/Node: snk_seq/i)).toBeVisible();

    // 7. Verify Observability: Logs
    await page.locator('button:has-text("Logs")').click();
    await expect(page.locator('table')).toContainText(/Received change/i, { timeout: 10000 });
    await expect(page.locator('table')).toContainText(/JOHN DOE/i);

    // 8. Verify Observability: History
    await page.locator('button:has-text("History")').click();
    await expect(page.getByText(/Version 1/i)).toBeVisible();

    // 9. Stop Workflow and Cleanup
    await page.getByRole('button', { name: 'Stop' }).click();
    await expect(page.getByRole('button', { name: 'Start' })).toBeVisible();
  });
});
