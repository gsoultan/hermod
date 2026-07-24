import { test, expect } from '@playwright/test';
import { Client } from 'pg';

test.describe('Performance and Stress E2E', () => {
  const sourceDB = 'hermod_test_source';
  const sinkDB = 'hermod_test_sink';
  const dbConfig = {
    host: 'localhost',
    port: 5432,
    user: 'postgres',
    password: 'postgres',
  };

  test('should run complex workflow with multiple transformations', async ({ page }) => {
    test.setTimeout(600000);
    const timestamp = Date.now();
    const vhostName = `vhost_perf_${timestamp}`;

    // 1. Login
    await page.goto('http://localhost:5173/login');
    await page.getByLabel('Username').first().fill('admin');
    await page.locator('input[type="password"]').fill('admin123');
    await page.click('button[type="submit"]');
    await expect(page).toHaveURL('http://localhost:5173/', { timeout: 10000 });

    // 2. Setup via API
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
          config: { host: 'localhost', port: '5432', user: 'postgres', password: 'postgres', dbname: sourceDB, tables: 'users', use_cdc: true, slot_name: `sl_perf_${timestamp}`, publication_name: `pb_perf_${timestamp}` }
        })
      })).json();

      const sink = await (await fetch('/api/sinks', {
        method: 'POST', headers,
        body: JSON.stringify({
          name: `Sink ${timestamp}`, vhost: vhostName, type: 'postgres',
          config: { connection_string: `postgres://postgres:postgres@localhost:5432/${sinkDB}`, table: 'users_sink_perf', use_existing_table: false }
        })
      })).json();

      return { lookupSrcId: lookupSrc.id, cdcSrcId: cdcSrc.id, sinkId: sink.id, token };
    }, { vhostName, timestamp, sourceDB, sinkDB });

    // 3. Create Multi-Transformation Workflow
    const workflow = {
      name: `Perf Stress ${timestamp}`,
      vhost: vhostName,
      active: true,
      nodes: [
        { id: 'src', type: 'source', ref_id: setupData.cdcSrcId, x: 50, y: 150 },
        { id: 't1_char', type: 'transformation', config: { transType: 'char_map', field: 'name', operations: ['uppercase', 'trim'], targetField: 'name_upper' }, x: 200, y: 150 },
        { id: 't2_conv', type: 'transformation', config: { transType: 'data_conversion', field: 'id', targetType: 'string', targetField: 'id_str' }, x: 350, y: 150 },
        { id: 't3_lookup', type: 'transformation', config: { transType: 'db_lookup', sourceId: setupData.lookupSrcId, table: 'lookup_table', keyColumn: 'user_name', keyField: '$.name', valueColumn: 'city', targetField: '$.city' }, x: 500, y: 150 },
        { id: 't4_api', type: 'transformation', config: { transType: 'api_lookup', method: 'GET', url: `http://localhost:5173/api/vhosts`, headers: JSON.stringify({ 'Authorization': `Bearer ${setupData.token}` }), responsePath: '$.[0].name', targetField: '$.vhost_info' }, x: 650, y: 150 },
        { id: 't5_filter', type: 'transformation', config: { transType: 'filter_data', field: 'name', operator: 'contains', value: 'Doe' }, x: 800, y: 150 },
        { id: 't6_map', type: 'transformation', config: { transType: 'mapping', mapping: JSON.stringify({ id: '$.id', name: '$.name_upper', email: '$.email', city: '$.city', vhost_info: '$.vhost_info' }) }, x: 950, y: 150 },
        { id: 'snk', type: 'sink', ref_id: setupData.sinkId, x: 1100, y: 150 }
      ],
      edges: [
        { id: 'e1', source_id: 'src', target_id: 't1_char' },
        { id: 'e2', source_id: 't1_char', target_id: 't2_conv' },
        { id: 'e3', source_id: 't2_conv', target_id: 't3_lookup' },
        { id: 'e4', source_id: 't3_lookup', target_id: 't4_api' },
        { id: 'e5', source_id: 't4_api', target_id: 't5_filter' },
        { id: 'e6', source_id: 't5_filter', target_id: 't6_map' },
        { id: 'e7', source_id: 't6_map', target_id: 'snk' }
      ]
    };

    await page.evaluate(async (wf) => {
      const token = localStorage.getItem('hermod_token');
      await fetch('/api/workflows', { method: 'POST', headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` }, body: JSON.stringify(wf) });
    }, workflow);

    console.log('Stress workflow created. Injecting 100 rows...');
    const sourceClient = new Client({ ...dbConfig, database: sourceDB });
    await sourceClient.connect();
    for (let i = 0; i < 100; i++) {
      await sourceClient.query("INSERT INTO users (name, email) VALUES ($1, $2)", [`John Doe ${i}`, `doe-${i}@example.com`]);
    }
    await sourceClient.end();

    console.log('Verifying data in sink...');
    const sinkClient = new Client({ ...dbConfig, database: sinkDB });
    await sinkClient.connect();
    let count = 0;
    for (let i = 0; i < 60; i++) {
        try {
            const res = await sinkClient.query("SELECT count(*) FROM users_sink_perf");
            count = parseInt(res.rows[0].count);
            console.log(`Current sink count: ${count}`);
            if (count >= 100) break;
        } catch (e) {
            console.log(`Sink table not ready yet: ${e.message}`);
        }
        await new Promise(r => setTimeout(r, 2000));
    }
    expect(count).toBe(100);
    await sinkClient.end();
  });
});
