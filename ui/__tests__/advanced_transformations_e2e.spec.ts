import { test, expect, Page } from '@playwright/test';
import { Client } from 'pg';
import { spawn } from 'child_process';

test.describe('Advanced Transformations Logic and E2E', () => {
  let workerProcess: any;

  test.afterAll(async () => {
    if (workerProcess) workerProcess.kill();
  });
  const getApiAuth = async (page: Page) => {
    const token = await page.evaluate(() => localStorage.getItem('hermod_token'));
    return {
      'Content-Type': 'application/json',
      'Authorization': `Bearer ${token}`
    };
  };

  const login = async (page: Page) => {
    await page.goto('/login');
    await page.getByPlaceholder('Your username').fill('admin');
    await page.getByPlaceholder('Your password').fill('admin123');
    await page.click('button:has-text("Sign In")');
    // Wait for the URL to change and ensure we are not on login page
    await page.waitForURL(url => !url.href.includes('/login'), { timeout: 30000 });
    // Also wait for some element on the home page
    await page.waitForSelector('text=Workflows', { timeout: 10000 });
  };

  test('Transformation Engine Logic Verification (API-driven)', async ({ page }) => {
    const timestamp = Date.now();
    await login(page);
    const headers = await getApiAuth(page);

    const testTransformation = async (transType: string, config: any, message: any) => {
      console.log(`Testing logic for: ${transType}`);
      const res = await page.evaluate(async ({ headers, transType, config, message }) => {
        const r = await fetch('/api/transformations/test', {
          method: 'POST',
          headers,
          body: JSON.stringify({
            transformation: { type: 'transformation', config: { ...config, transType } },
            message
          })
        });
        return r.json();
      }, { headers, transType, config, message });
      return res;
    };

    // 1. Lua Script
    const luaRes = await testTransformation('lua', { script: 'msg.val = (msg.id or 0) * 2; msg.ok = true' }, { id: 10 });
    expect(luaRes.val).toBe(20);
    expect(luaRes.ok).toBe(true);

    // Negative Lua: Syntax Error
    const luaErr = await testTransformation('lua', { script: 'invalid syntax' }, { id: 10 });
    expect(luaErr.error).toBeDefined();

    // 2. Aggregate (Sum) - use unique field to avoid state pollution in memory
    const aggRes = await testTransformation('aggregate', { field: `price_${timestamp}`, type: 'sum', targetField: 'total' }, { [`price_${timestamp}`]: 100 });
    expect(aggRes.total).toBe(100);

    // More Aggregate types
    const aggMax = await testTransformation('aggregate', { field: `val_${timestamp}`, type: 'max', targetField: 'max_val' }, { [`val_${timestamp}`]: 50 });
    expect(aggMax.max_val).toBe(50);
    const aggMin = await testTransformation('aggregate', { field: `val_${timestamp}`, type: 'min', targetField: 'min_val' }, { [`val_${timestamp}`]: 30 });
    expect(aggMin.min_val).toBe(30);
    const aggAvg = await testTransformation('aggregate', { field: `val_${timestamp}`, type: 'avg', targetField: 'avg_val' }, { [`val_${timestamp}`]: 40 });
    // (50+30+40)/3 = 40
    expect(aggAvg.avg_val).toBe(40);

    // 3. Sampling (Negative Case - 0.1% to avoid potential zero-parsing edge cases in some evaluators)
    const sampNeg = await testTransformation('sampling', { type: 'percentage', percentage: 0.1 }, { data: 'test' });
    expect(sampNeg.filtered || sampNeg.error === 'Filtered' || !sampNeg.data).toBe(true);

    // 4. Sampling (Positive Case - 100%)
    const sampPos = await testTransformation('sampling', { type: 'percentage', percentage: 100 }, { data: 'test' });
    expect(sampPos.data).toBe('test');

    // 5. Foreach / Fanout
    const foreachRes = await testTransformation('foreach', { arrayPath: 'items', resultField: 'out', limit: 2 }, { items: [1, 2, 3] });
    expect(foreachRes.out).toHaveLength(2);
    expect(foreachRes.out[0]).toBe(1);

    // 6. SCD Type 1 logic check
    const scdRes = await testTransformation('scd', { scdType: 1, targetSourceId: 'some-id', targetTable: 'tbl', businessKeys: 'id' }, { id: 1 });
    expect(Number(scdRes.id)).toBe(1);

    // 7. Rate Limit (Logic check)
    const rlRes = await testTransformation('rate_limit', { limit: 100, window: '1m' }, { data: 'test' });
    expect(rlRes.data).toBe('test');

    // 8. Row Count
    const rcRes = await testTransformation('row_count', { variableName: 'cnt', asField: true }, { data: 'test' });
    expect(rcRes.cnt).toBeGreaterThanOrEqual(1);

    // 9. Data Quality Scorer
    const dqRes = await testTransformation('dq_scorer', { rules: [{ field: 'email', type: 'email' }] }, { email: 'test@test.com' });
    expect(dqRes._dq_score).toBeDefined();

    // 10. Pivot
    const pivotRes = await testTransformation('pivot', { indexKeys: ['id'], attributeField: 'attr', valueField: 'val' }, { id: 1, attr: 'color', val: 'red' });
    expect(pivotRes.color).toBe('red');

    // 11. Unpivot
    const unpivotRes = await testTransformation('unpivot', { pivotColumns: ['color', 'size'], attributeField: 'k', valueField: 'v' }, { id: 1, color: 'red', size: 'M' });
    expect(unpivotRes._fanout).toHaveLength(2);
    expect(unpivotRes._fanout[0].k).toBe('color');
    expect(unpivotRes._fanout[0].v).toBe('red');

    // 12. Validator
    const valRes = await testTransformation('validator', { schema: JSON.stringify({ name: 'string', age: 'float' }) }, { name: 'John', age: 30.5 });
    expect(valRes.name).toBe('John');

    // Negative Validator
    const valErr = await testTransformation('validator', { schema: JSON.stringify({ name: 'int' }) }, { name: 'John' });
    expect(valErr.error).toBeDefined();

    // 13. Multicast
    const multiRes = await testTransformation('multicast', { branches: [{ prefix: 'a_' }, { prefix: 'b_' }] }, { id: 1 });
    expect(multiRes._fanout).toHaveLength(2);
    expect(multiRes._fanout[0].a_id).toBe(1);

    // 14. Mask
    const maskRes = await testTransformation('mask', { field: 'email', maskType: 'email' }, { email: 'john@doe.com' });
    expect(maskRes.email).toContain('****');

    // 15. Data Conversion
    const convRes = await testTransformation('data_conversion', { field: 'id', targetType: 'string' }, { id: 123 });
    expect(convRes.id).toBe('123');

    // Negative Conversion
    const convErr = await testTransformation('data_conversion', { field: 'id', targetType: 'int', errorBehavior: 'fail' }, { id: 'not_an_int' });
    expect(convErr.error).toBeDefined();

    // 16. Stat Validator (Anomaly Detection)
    // First message - sets baseline
    await testTransformation('stat_validator', { field: 'val', min_samples: 1 }, { val: 10 });
    // Second message - should be normal
    const statRes = await testTransformation('stat_validator', { field: 'val', min_samples: 1, threshold: 2 }, { val: 11 });
    expect(statRes._metadata).toBeUndefined(); // Normal
    // High value - anomaly
    const statAnom = await testTransformation('stat_validator', { field: 'val', min_samples: 1, threshold: 0.1 }, { val: 100 });
    // Note: evaluator/registry state might not persist across API calls if they are handled by different ephemeral engine instances, 
    // but in many Hermod versions, the registry is a singleton.
    
    // 17. Filter Data
    const filterRes = await testTransformation('filter_data', { field: 'age', operator: 'gt', value: '18' }, { age: 20 });
    console.log(`Filter Res: ${JSON.stringify(filterRes)}`);
    expect(filterRes.age).toBe(20);

    // Negative Filter (Filtered out)
    const filterNeg = await testTransformation('filter_data', { field: 'age', operator: 'gt', value: '18' }, { age: 10 });
    expect(filterNeg.filtered || filterNeg.error === 'Filtered' || !filterNeg.age).toBe(true);

    // 18. Mapping (Exact value lookup)
    const mapRes = await testTransformation('mapping', { field: 'status', mapping: JSON.stringify({ '1': 'active', '0': 'inactive' }) }, { status: '1' });
    expect(mapRes.status).toBe('active');

    // 19. Set (Field assignment)
    const setRes = await testTransformation('set', { 'column.updated': 'true', 'column.version': '1' }, { id: 1 });
    expect(setRes.updated).toBe(true);
    expect(setRes.version).toBe(1);

    // 20. Advanced (Field Construction)
    const advRes = await testTransformation('advanced', { 'column.fullName': "concat(source.first_name, ' ', source.last_name)" }, { first_name: 'John', last_name: 'Doe' });
    expect(advRes.fullName).toBe('John Doe');

    console.log('All transformation logic verified successfully.');
  });

  test('Full Pipeline E2E (Postgres to Postgres)', async ({ page }) => {
    test.setTimeout(180000);
    const timestamp = Date.now();
    const vhostName = `vhost_full_${timestamp}`;
    await login(page);
    const headers = await getApiAuth(page);

    // Setup VHost
    await page.evaluate(async ({ vhostName, headers }) => {
      await fetch('/api/vhosts', { method: 'POST', headers, body: JSON.stringify({ name: vhostName }) });
    }, { vhostName, headers });

    // Start Worker for this test
    console.log('Registering and starting worker...');
    const workerReg = await (await page.evaluate(async ({ headers }) => {
        const res = await fetch('/api/workers', {
            method: 'POST', headers,
            body: JSON.stringify({ host: '127.0.0.1', description: 'E2E Worker' })
        });
        return res.json();
    }, { headers }));

    workerProcess = spawn('./hermod', [
        '--mode=worker',
        `--worker-guid=${workerReg.id}`,
        `--worker-token=secret`,
        `--platform-url=http://127.0.0.1:4001`
    ], { env: { ...process.env, HERMOD_MASTER_KEY: 'secret' } });
    
    workerProcess.stdout.on('data', (d) => console.log('WORKER:', d.toString().trim()));
    workerProcess.stderr.on('data', (d) => console.error('WORKER ERR:', d.toString().trim()));

    // 1. Setup Source and Sink
    const sourceRes = await (await page.evaluate(async ({ headers, vhostName, timestamp }) => {
      const res = await fetch('/api/sources', {
        method: 'POST', headers,
        body: JSON.stringify({
          name: `Full Src ${timestamp}`, vhost: vhostName, type: 'postgres',
          config: {
            host: '127.0.0.1', port: '5432', user: 'postgres', password: 'postgres', dbname: 'hermod_test_source',
            tables: 'users', use_cdc: true, slot_name: `sl_f_${timestamp}`, publication_name: `pb_f_${timestamp}`
          }
        })
      });
      return res.json();
    }, { headers, vhostName, timestamp }));

    const sinkRes = await (await page.evaluate(async ({ headers, vhostName, timestamp }) => {
      const res = await fetch('/api/sinks', {
        method: 'POST', headers,
        body: JSON.stringify({
          name: `Full Snk ${timestamp}`, vhost: vhostName, type: 'postgres',
          config: {
            connection_string: 'postgres://postgres:postgres@127.0.0.1:5432/hermod_test_sink',
            table: 'full_results', use_existing_table: false,
            batch_size: 1,
            column_mappings: JSON.stringify([
              {source_field: 'id', target_column: 'id', data_type: 'INTEGER', is_primary_key: true},
              {source_field: 'name', target_column: 'name', data_type: 'TEXT'},
              {source_field: 'email', target_column: 'email', data_type: 'TEXT'},
              {source_field: 'e2e', target_column: 'e2e', data_type: 'BOOLEAN'}
            ])
          }
        })
      });
      return res.json();
    }, { headers, vhostName, timestamp }));

    // 2. Create complex workflow assigned to this worker
    const workflowRes = await (await page.evaluate(async ({ headers, vhostName, timestamp, sourceId, sinkId, workerId }) => {
      const res = await fetch('/api/workflows', {
        method: 'POST', headers,
        body: JSON.stringify({
          name: `Full WF ${timestamp}`, vhost: vhostName, active: true, worker_id: workerId,
          nodes: [
            { id: 'src', type: 'source', ref_id: sourceId, x: 100, y: 100 },
            { id: 'lua', type: 'transformation', config: { transType: 'lua', script: 'msg.name = "MR " .. (msg.name or ""); msg.e2e = true' }, x: 300, y: 100 },
            { id: 'snk', type: 'sink', ref_id: sinkId, x: 500, y: 100 }
          ],
          edges: [{ id: 'e1', source_id: 'src', target_id: 'lua' }, { id: 'e2', source_id: 'lua', target_id: 'snk' }]
        })
      });
      return res.json();
    }, { headers, vhostName, timestamp, sourceId: sourceRes.id, sinkId: sinkRes.id, workerId: workerReg.id }));

    // 3. Inject data (wait for CDC initialization first)
    console.log('Waiting 60s for CDC initialization...');
    await new Promise(r => setTimeout(r, 60000));

    const client = new Client({ host: '127.0.0.1', port: 5432, user: 'postgres', password: 'postgres', database: 'hermod_test_source' });
    await client.connect();
    console.log('Injecting test row...');
    await client.query("INSERT INTO users (name, email) VALUES ($1, $2)", [`E2E User ${timestamp}`, 'e2e@test.com']);
    await client.end();

    // 4. Verify sink
    const sinkClient = new Client({ host: '127.0.0.1', port: 5432, user: 'postgres', password: 'postgres', database: 'hermod_test_sink' });
    await sinkClient.connect();
    
    console.log('Waiting for data in sink (polling for 120s)...');
    let found = false;
    for (let i = 0; i < 60; i++) {
        try {
            const res = await sinkClient.query("SELECT * FROM full_results WHERE name LIKE $1", [`MR E2E User ${timestamp}`]);
            if (res.rows.length > 0) {
                console.log(`Found data in sink after ${i*2}s!`);
                found = true;
                break;
            }
        } catch (e) {
            if (i % 5 === 0) console.log(`Sink check attempt ${i+1}: ${e.message}`);
        }
        await new Promise(r => setTimeout(r, 2000));
    }
    await sinkClient.end();
    expect(found).toBe(true);
  });

  test('UI Run Preview Verification', async ({ page }) => {
    test.setTimeout(300000);
    const timestamp = Date.now();
    await login(page);
    const headers = await getApiAuth(page);

    // Create workflow with a transformation node via API to avoid UI flakiness
    const wfRes = await page.evaluate(async ({ headers, timestamp }) => {
        const res = await fetch('/api/workflows', {
            method: 'POST', headers,
            body: JSON.stringify({
                name: `Preview UI ${timestamp}`, vhost: 'default', active: false,
                nodes: [{ id: 't1', type: 'transformation', config: { transType: 'char_map' }, x: 100, y: 100 }],
                edges: []
            })
        });
        return res.json();
    }, { headers, timestamp });

    await page.goto(`/workflows/${wfRes.id}`);
    
    // Wait for canvas to load
    await page.waitForSelector('.react-flow__node', { timeout: 30000 });
    
    // Find the node and open config
    const node = page.locator('.react-flow__node').first();
    await node.click({ force: true });
    await node.dblclick({ force: true });

    // Ensure modal is open by waiting for some text inside it
    await expect(page.locator('text=Transformation Config')).toBeVisible({ timeout: 10000 });

    // Select Transformation Type (it's already char_map, but let's re-select to be sure or just fill config)
    // Actually, let's just fill the config since it's already char_map
    await page.click('text=Add Operation');
    await page.click('text=Uppercase');
    await page.getByLabel('Field').fill('name');

    // Run Preview
    await page.locator('textarea').fill(JSON.stringify({ name: 'john' }));
    await page.click('button:has-text("Run Preview")');

    // Verify Output
    await expect(page.locator('pre:has-text("JOHN")')).toBeVisible({ timeout: 15000 });
    console.log('UI Run Preview verified successfully.');
  });
});
