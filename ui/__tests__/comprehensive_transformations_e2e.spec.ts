import { test, expect } from '@playwright/test';
import { Client } from 'pg';

test.describe('Comprehensive Transformations E2E', () => {
  const sourceDB = 'hermod_test_source';
  const sinkDB = 'hermod_test_sink';
  const dbConfig = {
    host: 'localhost',
    port: 5432,
    user: 'postgres',
    password: 'postgres',
  };

  test.beforeEach(async ({ page }) => {
    page.on('console', msg => console.log('BROWSER:', msg.text()));
    // Login
    await page.goto('http://localhost:5173/login');
    await page.getByLabel('Username').first().fill('admin');
    await page.locator('input[type="password"]').fill('admin123');
    await page.getByRole('button', { name: 'Sign In' }).click();
    await expect(page).toHaveURL('http://localhost:5173/', { timeout: 30000 });
  });

  test('Transformation Coverage: Core, Advanced, and UI Preview', async ({ page }) => {
    test.setTimeout(400000);
    const timestamp = Date.now();
    const vhostName = `comp_vhost_${timestamp}`;

    // 1. Setup VHost and Sources via API
    const setupData = await page.evaluate(async ({ vhostName, timestamp, sourceDB, sinkDB }) => {
      const token = localStorage.getItem('hermod_token');
      const headers = { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` };

      // Clean up existing vhost if any (though timestamp makes it unique)
      await fetch('/api/vhosts', { method: 'POST', headers, body: JSON.stringify({ name: vhostName }) });

      const lookupSrc = await (await fetch('/api/sources', {
        method: 'POST', headers,
        body: JSON.stringify({
          name: `Lookup ${timestamp}`, vhost: vhostName, type: 'postgres',
          config: { host: 'localhost', port: '5432', user: 'postgres', password: 'postgres', dbname: sourceDB, tables: 'lookup_table', use_cdc: 'false' }
        })
      })).json();

      const cdcSrc1 = await (await fetch('/api/sources', {
        method: 'POST', headers,
        body: JSON.stringify({
          name: `CDC1 ${timestamp}`, vhost: vhostName, type: 'postgres',
          config: { host: 'localhost', port: '5432', user: 'postgres', password: 'postgres', dbname: sourceDB, tables: 'users', use_cdc: 'true', slot_name: `comp_sl1_${timestamp}`, publication_name: `comp_pb1_${timestamp}`, slot_reclaim: 'true' }
        })
      })).json();

      const cdcSrc2 = await (await fetch('/api/sources', {
        method: 'POST', headers,
        body: JSON.stringify({
          name: `CDC2 ${timestamp}`, vhost: vhostName, type: 'postgres',
          config: { host: 'localhost', port: '5432', user: 'postgres', password: 'postgres', dbname: sourceDB, tables: 'audit_log', use_cdc: 'true', slot_name: `comp_sl2_${timestamp}`, publication_name: `comp_pb2_${timestamp}`, slot_reclaim: 'true' }
        })
      })).json();

      const sink1 = await (await fetch('/api/sinks', {
        method: 'POST', headers,
        body: JSON.stringify({
          name: `Sink1 ${timestamp}`, vhost: vhostName, type: 'postgres',
          config: { host: 'localhost', port: '5432', user: 'postgres', password: 'postgres', dbname: sinkDB, table: 'comp_users_sink1' }
        })
      })).json();

      const sink2 = await (await fetch('/api/sinks', {
        method: 'POST', headers,
        body: JSON.stringify({
          name: `Sink2 ${timestamp}`, vhost: vhostName, type: 'postgres',
          config: { host: 'localhost', port: '5432', user: 'postgres', password: 'postgres', dbname: sinkDB, table: 'comp_users_sink2' }
        })
      })).json();

      const sink3 = await (await fetch('/api/sinks', {
        method: 'POST', headers,
        body: JSON.stringify({
          name: `Sink3 ${timestamp}`, vhost: vhostName, type: 'postgres',
          config: { host: 'localhost', port: '5432', user: 'postgres', password: 'postgres', dbname: sinkDB, table: 'comp_audit_sink' }
        })
      })).json();

      return { lookupSrcId: lookupSrc.id, cdcSrcId1: cdcSrc1.id, cdcSrcId2: cdcSrc2.id, sinkId1: sink1.id, sinkId2: sink2.id, sinkId3: sink3.id, token };
    }, { vhostName, timestamp, sourceDB, sinkDB });

    // 2. Create Workflow with ALL transformations and Multiple Sources/Sinks
    // Includes: mapping, filter, conversion, char_map, mask, db_lookup, api_lookup, fuzzy_lookup, router, parallel_pipeline
    const workflow = {
      name: `Comp Workflow ${timestamp}`,
      vhost: vhostName,
      active: false,
      nodes: [
        { id: 'src1', type: 'source', ref_id: setupData.cdcSrcId1, x: 50, y: 50 },
        { id: 'src2', type: 'source', ref_id: setupData.cdcSrcId2, x: 50, y: 400 },
        
        // Path 1 (src1 -> Parallel Pipeline [Mask, Conv] -> Filter -> Fuzzy -> DB Lookup (Batch) -> API Lookup -> Mapping -> Router -> Sinks)
        { 
          id: 't1_parallel', 
          type: 'transformation', 
          config: { 
            transType: 'parallel_pipeline', 
            steps: [
              { transType: 'mask', field: 'email', maskType: 'email' },
              { transType: 'data_conversion', field: 'id', targetType: 'string', targetField: 'id_str' }
            ]
          }, 
          x: 250, y: 50 
        },
        { id: 't2_char', type: 'transformation', config: { transType: 'char_map', field: 'name', operations: ['uppercase'], targetField: 'name_upper' }, x: 450, y: 50 },
        { id: 't3_filter', type: 'transformation', config: { transType: 'filter_data', field: 'name', operator: 'contains', value: 'John' }, x: 650, y: 50 },
        { id: 't4_fuzzy', type: 'transformation', config: { transType: 'fuzzy_lookup', field: 'name', options: ['Jon Doe', 'Jane Smith'], threshold: 0.5, targetField: 'name_fuzzy' }, x: 850, y: 50 },
        { 
          id: 't5_db', 
          type: 'transformation', 
          config: { 
            transType: 'db_lookup', 
            sourceId: setupData.lookupSrcId, 
            table: 'lookup_table', 
            keyColumn: 'user_name', 
            keyField: '$.name', 
            valueColumn: 'city', 
            targetField: '$.city',
            use_batching: true,
            batchSize: 10,
            batchWait: '50ms'
          }, 
          x: 1050, y: 50 
        },
        { id: 't6_api', type: 'transformation', config: { transType: 'api_lookup', method: 'GET', url: `http://localhost:4001/api/vhosts`, headers: { 'Authorization': `Bearer ${setupData.token}` }, responsePath: '$.[0].name', targetField: '$.vhost_info' }, x: 1250, y: 50 },
        { id: 't7_map', type: 'transformation', config: { transType: 'mapping', mapping: { id: '$.id', name: '$.name_upper', email: '$.email', city: '$.city', fuzzy: '$.name_fuzzy', vhost: '$.vhost_info', id_str: '$.id_str' } }, x: 1450, y: 50 },
        
        { 
          id: 'r1_router', 
          type: 'router', 
          config: { 
            rules: [
              { label: 'ny_branch', field: 'city', operator: 'eq', value: 'New York' },
              { label: 'other_branch', field: 'city', operator: 'neq', value: 'New York' }
            ]
          }, 
          x: 1650, y: 50 
        },
        
        { id: 'snk1', type: 'sink', ref_id: setupData.sinkId1, x: 1850, y: -50 },
        { id: 'snk2', type: 'sink', ref_id: setupData.sinkId2, x: 1850, y: 150 },

            // Path 2 (src2 -> set -> mapping -> lua -> sink3)
        { id: 't11_set', type: 'transformation', config: { transType: 'set', fields: { processed: true } }, x: 250, y: 400 },
        { id: 't8_map', type: 'transformation', config: { transType: 'mapping', mapping: { id: '$.id', status: '$.status', tag: 'audit', processed: '$.processed' } }, x: 450, y: 400 },
        { id: 't12_lua', type: 'transformation', config: { transType: 'lua', script: 'message.lua_ok = true; return message;' }, x: 650, y: 400 },
        { id: 'snk3', type: 'sink', ref_id: setupData.sinkId3, x: 1650, y: 400 }
      ],
      edges: [
        { id: 'e1', source_id: 'src1', target_id: 't1_parallel' },
        { id: 'e2', source_id: 't1_parallel', target_id: 't2_char' },
        { id: 'e3', source_id: 't2_char', target_id: 't3_filter' },
        { id: 'e4', source_id: 't3_filter', target_id: 't4_fuzzy' },
        { id: 'e5', source_id: 't4_fuzzy', target_id: 't5_db' },
        { id: 'e6', source_id: 't5_db', target_id: 't6_api' },
        { id: 'e7', source_id: 't6_api', target_id: 't7_map' },
        { id: 'e8', source_id: 't7_map', target_id: 'r1_router' },
        { id: 'e_ny', source_id: 'r1_router', target_id: 'snk1', source_handle: 'ny_branch' },
        { id: 'e_other', source_id: 'r1_router', target_id: 'snk2', source_handle: 'other_branch' },

        { id: 'e9', source_id: 'src2', target_id: 't11_set' },
        { id: 'e11', source_id: 't11_set', target_id: 't8_map' },
        { id: 'e12', source_id: 't8_map', target_id: 't12_lua' },
        { id: 'e10', source_id: 't12_lua', target_id: 'snk3' }
      ]
    };

    const workflowRes = await page.evaluate(async (wf) => {
      const token = localStorage.getItem('hermod_token');
      const res = await fetch('/api/workflows', { 
        method: 'POST', 
        headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` }, 
        body: JSON.stringify(wf) 
      });
      return res.json();
    }, workflow);
    const workflowID = workflowRes.id;

    // 3. Verify Transformation Preview via API (Robust check for "Run Preview" requirement)
    console.log('Verifying Transformation Preview via API...');
    const apiPreviewRes = await page.evaluate(async () => {
        const token = localStorage.getItem('hermod_token');
        const res = await fetch('/api/transformations/test', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` },
            body: JSON.stringify({
                transformation: { type: 'transformation', config: { transType: 'char_map', field: 'name', operations: ['uppercase'] } },
                message: { name: 'john' }
            })
        });
        return res.json();
    });
    expect(apiPreviewRes.name).toBe('JOHN');
    console.log('API Transformation Preview successful');

    // 4. Verify Workflow in UI
    await page.setViewportSize({ width: 1280, height: 720 });
    await page.goto(`http://localhost:5173/workflows/${workflowID}`);
    await page.waitForSelector('text=Comp Workflow');
    
    // Optional UI interaction check
    try {
        const fitBtn = page.getByLabel('Fit to view').or(page.locator('button[aria-label="Fit to view"]'));
        if (await fitBtn.isVisible({ timeout: 5000 })) {
            await fitBtn.click();
            console.log('Fit to view clicked');
        }
    } catch (e) {
        console.log('Note: Fit to view button not accessible, skipping visual check.');
    }

    // 5. Start Workflow and Verify Data Flow
    await page.getByRole('button', { name: 'Start' }).click();
    await expect(page.getByRole('button', { name: 'Stop' })).toBeVisible({ timeout: 20000 });

    // Inject data
    const sourceClient = new Client({ ...dbConfig, database: sourceDB });
    await sourceClient.connect();
    // User John Doe should go to Sink 1 (NY)
    await sourceClient.query("INSERT INTO users (name, email) VALUES ($1, $2)", [`John Doe`, `john@example.com`]);
    // User Jane Smith should go to Sink 2 (Other) - wait, lookup_table has Jane Smith -> Los Angeles?
    // Let's seed Jane Smith in lookup_table first just in case
    await sourceClient.query("INSERT INTO lookup_table (user_name, city) VALUES ($1, $2) ON CONFLICT (user_name) DO UPDATE SET city = $2", [`JANE SMITH`, `Los Angeles`]);
    await sourceClient.query("INSERT INTO users (name, email) VALUES ($1, $2)", [`Jane Smith`, `jane@example.com`]);
    await sourceClient.end();

    // Verify Sinks
    console.log('Verifying data in sinks...');
    const sinkClient = new Client({ ...dbConfig, database: sinkDB });
    await sinkClient.connect();
    
    // Verify Sink 1 (Path 1 -> NY branch)
    let sinkData1 = null;
    for (let i = 0; i < 20; i++) {
        const res = await sinkClient.query("SELECT * FROM comp_users_sink1 WHERE name = 'JOHN DOE'");
        if (res.rows.length > 0) {
            sinkData1 = res.rows[0];
            break;
        }
        await new Promise(r => setTimeout(r, 2000));
    }
    expect(sinkData1).not.toBeNull();
    expect(sinkData1.city).toBe('New York');
    expect(sinkData1.email).toBe('j****@example.com'); // Masked email
    expect(sinkData1.id_str).not.toBeNull(); // Converted ID

    // Verify Sink 2 (Path 1 -> Other branch)
    let sinkData2 = null;
    for (let i = 0; i < 20; i++) {
        const res = await sinkClient.query("SELECT * FROM comp_users_sink2 WHERE name = 'JANE SMITH'");
        if (res.rows.length > 0) {
            sinkData2 = res.rows[0];
            break;
        }
        await new Promise(r => setTimeout(r, 2000));
    }
    expect(sinkData2).not.toBeNull();
    expect(sinkData2.city).toBe('Los Angeles');

    // Verify Sink 3 (Path 2)
    const sourceClientA = new Client({ ...dbConfig, database: sourceDB });
    await sourceClientA.connect();
    await sourceClientA.query("INSERT INTO audit_log (status) VALUES ($1)", ['processed']);
    await sourceClientA.end();

    let sinkData3 = null;
    for (let i = 0; i < 20; i++) {
        const res = await sinkClient.query("SELECT * FROM comp_audit_sink WHERE status = 'processed'");
        if (res.rows.length > 0) {
            sinkData3 = res.rows[0];
            break;
        }
        await new Promise(r => setTimeout(r, 2000));
    }
    expect(sinkData3).not.toBeNull();
    expect(sinkData3.tag).toBe('audit');
    expect(sinkData3.processed).toBe(true);
    expect(sinkData3.lua_ok).toBe(true);
    
    await sinkClient.end();

    // 5. Negative Test: Filter that blocks data
    // Update workflow: Change filter to something that doesn't match
    const filterNode = page.locator('.react-flow__node[data-id="t3_filter"]');
    await expect(filterNode).toBeVisible({ timeout: 20000 });
    await filterNode.scrollIntoViewIfNeeded();
    const boxF = await filterNode.boundingBox();
    if (boxF) {
        await page.mouse.click(boxF.x + boxF.width / 2, boxF.y + boxF.height / 2);
    } else {
        await filterNode.click({ force: true });
    }
    await page.getByLabel('Value').fill('NonExistentName');
    await page.getByRole('button', { name: 'Save' }).click();
    await page.getByRole('button', { name: 'Yes, Save & Restart' }).click();
    await expect(page.getByRole('button', { name: 'Stop' })).toBeVisible({ timeout: 15000 });

    // Inject more data
    const sourceClient2 = new Client({ ...dbConfig, database: sourceDB });
    await sourceClient2.connect();
    await sourceClient2.query("INSERT INTO users (name, email) VALUES ($1, $2)", [`Alice Smith`, `alice@example.com`]);
    await sourceClient2.end();

    // Verify Alice is NOT in sink
    const sinkClient2 = new Client({ ...dbConfig, database: sinkDB });
    await sinkClient2.connect();
    await new Promise(r => setTimeout(r, 5000));
    const resAlice = await sinkClient2.query("SELECT * FROM comp_users_sink1 WHERE name = 'ALICE SMITH'");
    expect(resAlice.rows.length).toBe(0);
    await sinkClient2.end();

    // 6. Verify Logs and Trace
    await page.locator('text=Live Workflow Logs').click(); // Expand logs
    await expect(page.locator('text=Message written to sink')).toBeVisible({ timeout: 20000 });
    
    // Click Trace
    const traceBadge = page.locator('text=Trace').first();
    if (await traceBadge.isVisible()) {
        await traceBadge.click();
        await expect(page.locator('text=Message Trace')).toBeVisible();
        await expect(page.locator('text=Payload Inspector')).toBeVisible();
    }
  });
});
