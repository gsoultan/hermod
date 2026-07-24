import { test, expect, Page } from '@playwright/test';
import { spawn, ChildProcessWithoutNullStreams } from 'child_process';
import { Client } from 'pg';

test.describe('Heavy Load Multi-Worker E2E', () => {
  const sourceDB = 'hermod_test_source';
  const sinkDB = 'hermod_test_sink';
  const dbConfig = {
    host: '127.0.0.1',
    port: 5432,
    user: 'postgres',
    password: 'postgres',
  };

  let workers: ChildProcessWithoutNullStreams[] = [];

  const startWorker = async (page: Page, id: number): Promise<ChildProcessWithoutNullStreams> => {
    console.log(`Registering worker ${id}...`);
    await page.goto('/workers');
    await page.getByRole('button', { name: /Register Worker/i }).first().click();
    await page.getByLabel('Host').fill(`127.0.0.1`);
    await page.getByLabel('Description').fill(`Heavy Load Worker ${id}`);
    await page.getByRole('button', { name: /Register Worker/i }).last().click();

    const commandLocator = page.getByText(/hermod --mode=worker --worker-guid=/i);
    await expect(commandLocator).toBeVisible({ timeout: 30000 });
    const rawCommand = await commandLocator.innerText();
    console.log(`Worker ${id} raw command: ${rawCommand}`);

    const fullCommand = rawCommand.split('\n').find(line => line.includes('hermod --mode=worker'))?.trim().replace(/Copy$/, '').trim();
    
    if (!fullCommand) {
        await page.screenshot({ path: `worker_reg_fail_${id}.png` });
        throw new Error(`Failed to extract command for worker ${id}`);
    }
    
    const commandArgs = fullCommand.replace('hermod ', '')
        .replace(/\s+/g, ' ')
        .trim()
        .split(' ')
        .map(arg => {
            if (arg.startsWith('--platform-url=')) {
                // Use 127.0.0.1 for platform URL to avoid Unix socket issues on some systems
                return '--platform-url=http://127.0.0.1:4001';
            }
            if (arg.startsWith('--worker-token=')) {
                // Use the master key to avoid authentication flakiness in tests
                return '--worker-token=secret';
            }
            if (arg.includes('=')) {
                const [key, ...rest] = arg.split('=');
                let value = rest.join('=');
                value = value.replace(/^"|"$/g, '').replace(/^'|'$/g, '');
                return `${key}=${value}`;
            }
            return arg;
        });

    console.log(`Starting worker process ${id} with args:`, commandArgs);
    const proc = spawn('./hermod', commandArgs, {
      env: { ...process.env, HERMOD_MASTER_KEY: 'secret' }
    });
    
    proc.stdout.on('data', (data) => {
        const out = data.toString();
        if (out.includes('level":"error"')) console.error(`Worker ${id} LOG: ${out.trim()}`);
        else console.log(`Worker ${id}: ${out.trim()}`);
    });
    proc.stderr.on('data', (data) => console.error(`Worker ${id} STDERR: ${data}`));
    
    // Give it a moment to connect and register heartbeats
    await new Promise(r => setTimeout(r, 3000));
    return proc;
  };

  test('should handle heavy traffic with 3 workers and multiple workflows', async ({ page }) => {
    test.setTimeout(600000); // 10 minutes
    const timestamp = Date.now();
    const vhostName = `vhost_heavy_${timestamp}`;

    // 1. Login
    await page.goto('/login');
    await page.getByPlaceholder('Your username').fill('admin');
    await page.getByPlaceholder('Your password').fill('admin123');
    await page.click('button:has-text("Sign In")');
    await expect(page).not.toHaveURL(/login/);

    // 2. Start 3 Workers
    for (let i = 1; i <= 3; i++) {
        workers.push(await startWorker(page, i));
    }

    // 3. Setup Sources and Sinks via API
    console.log('Setting up environment via API...');
    const setupData = await page.evaluate(async ({ vhostName, timestamp, sourceDB, sinkDB }) => {
      const token = localStorage.getItem('hermod_token');
      const headers = { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` };

      await fetch('/api/vhosts', { method: 'POST', headers, body: JSON.stringify({ name: vhostName }) });

      const srcUsers = await (await fetch('/api/sources', {
        method: 'POST', headers,
        body: JSON.stringify({
          name: `Users CDC ${timestamp}`, vhost: vhostName, type: 'postgres',
          config: { host: '127.0.0.1', port: '5432', user: 'postgres', password: 'postgres', dbname: sourceDB, tables: 'users', use_cdc: true, slot_name: `sl_u_${timestamp}`, publication_name: `pb_u_${timestamp}` }
        })
      })).json();

      const srcOrders = await (await fetch('/api/sources', {
        method: 'POST', headers,
        body: JSON.stringify({
          name: `Orders CDC (Simulated RMQ) ${timestamp}`, vhost: vhostName, type: 'postgres',
          config: { host: '127.0.0.1', port: '5432', user: 'postgres', password: 'postgres', dbname: sourceDB, tables: 'orders', use_cdc: true, slot_name: `sl_o_${timestamp}`, publication_name: `pb_o_${timestamp}` }
        })
      })).json();

      const lookupSrc = await (await fetch('/api/sources', {
        method: 'POST', headers,
        body: JSON.stringify({
          name: `Lookup ${timestamp}`, vhost: vhostName, type: 'postgres',
          config: { host: '127.0.0.1', port: '5432', user: 'postgres', password: 'postgres', dbname: sourceDB, tables: 'lookup_table', use_cdc: false }
        })
      })).json();

      const sink1 = await (await fetch('/api/sinks', {
        method: 'POST', headers,
        body: JSON.stringify({
          name: `Users Sink ${timestamp}`, vhost: vhostName, type: 'postgres',
          config: { connection_string: `postgres://postgres:postgres@127.0.0.1:5432/${sinkDB}`, table: 'users_heavy', use_existing_table: false }
        })
      })).json();

      const sink2 = await (await fetch('/api/sinks', {
        method: 'POST', headers,
        body: JSON.stringify({
          name: `Orders Sink ${timestamp}`, vhost: vhostName, type: 'postgres',
          config: { connection_string: `postgres://postgres:postgres@127.0.0.1:5432/${sinkDB}`, table: 'orders_heavy', use_existing_table: false }
        })
      })).json();

      return { srcUsersId: srcUsers.id, srcOrdersId: srcOrders.id, lookupSrcId: lookupSrc.id, sink1Id: sink1.id, sink2Id: sink2.id, token };
    }, { vhostName, timestamp, sourceDB, sinkDB });

    // 4. Create 3 Separate Workflows to ensure distribution
    const workflows = [
        {
            name: `Heavy-Users-${timestamp}`,
            vhost: vhostName,
            active: true,
            nodes: [
                { id: 'src', type: 'source', ref_id: setupData.srcUsersId, x: 50, y: 100 },
                { id: 'p1', type: 'transformation', config: { transType: 'parallel_pipeline', pipelines: [
                    [{ id: 't1', type: 'transformation', config: { transType: 'char_map', field: 'name', operations: ['uppercase'], targetField: 'name_upper' } }],
                    [{ id: 't2', type: 'transformation', config: { transType: 'db_lookup', sourceId: setupData.lookupSrcId, table: 'lookup_table', keyColumn: 'user_name', keyField: '$.name', valueColumn: 'city', targetField: '$.city' } }]
                ] }, x: 250, y: 100 },
                { id: 'snk', type: 'sink', ref_id: setupData.sink1Id, x: 500, y: 100 }
            ],
            edges: [{ id: 'e1', source_id: 'src', target_id: 'p1' }, { id: 'e2', source_id: 'p1', target_id: 'snk' }]
        },
        {
            name: `Heavy-Orders-${timestamp}`,
            vhost: vhostName,
            active: true,
            nodes: [
                { id: 'src', type: 'source', ref_id: setupData.srcOrdersId, x: 50, y: 100 },
                { id: 't_api', type: 'transformation', config: { transType: 'api_lookup', method: 'GET', url: `http://127.0.0.1:4001/api/vhosts`, headers: { 'Authorization': `Bearer ${setupData.token}` }, responsePath: '$.[0].name', targetField: '$.vhost_info' }, x: 250, y: 100 },
                { id: 'snk', type: 'sink', ref_id: setupData.sink2Id, x: 500, y: 100 }
            ],
            edges: [{ id: 'e1', source_id: 'src', target_id: 't_api' }, { id: 'e2', source_id: 't_api', target_id: 'snk' }]
        },
        {
            name: `Heavy-Misc-${timestamp}`,
            vhost: vhostName,
            active: true,
            nodes: [
                { id: 'src', type: 'source', ref_id: setupData.srcUsersId, x: 50, y: 100 },
                { id: 't_filt', type: 'transformation', config: { transType: 'filter_data', field: 'name', operator: 'contains', value: 'User' }, x: 250, y: 100 },
                { id: 'snk', type: 'sink', ref_id: setupData.sink2Id, x: 500, y: 100 }
            ],
            edges: [{ id: 'e1', source_id: 'src', target_id: 't_filt' }, { id: 'e2', source_id: 't_filt', target_id: 'snk' }]
        }
    ];

    const workflowIDs = [];
    console.log('Creating workflows...');
    for (const wf of workflows) {
        const wfRes = await page.evaluate(async (wf) => {
            const token = localStorage.getItem('hermod_token');
            const res = await fetch('/api/workflows', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` },
                body: JSON.stringify(wf)
            });
            return res.json();
        }, wf);
        workflowIDs.push(wfRes.id);
    }

    // 5. Inject Heavy Traffic
    const client = new Client({ ...dbConfig, database: sourceDB });
    await client.connect();

    console.log('Injecting first batch (50 rows each)...');
    for (let i = 0; i < 50; i++) {
        await client.query("INSERT INTO users (name, email) VALUES ($1, $2)", [`User ${i}`, `user${i}@test.com`]);
        await client.query("INSERT INTO orders (user_id, amount, status) VALUES ($1, $2, $3)", [i, 10.5 * i, 'pending']);
    }

    // 6. Test Lifecycle: Stop/Start during traffic
    console.log('Stopping first workflow during traffic...');
    await page.goto(`/workflows/${workflowIDs[0]}`);
    await page.getByRole('button', { name: 'Stop' }).click();
    await expect(page.getByRole('button', { name: 'Start' })).toBeVisible({ timeout: 20000 });

    console.log('Injecting while stopped (20 rows)...');
    for (let i = 50; i < 70; i++) {
        await client.query("INSERT INTO users (name, email) VALUES ($1, $2)", [`User ${i}`, `user${i}@test.com`]);
        await client.query("INSERT INTO orders (user_id, amount, status) VALUES ($1, $2, $3)", [i, 10.5 * i, 'pending']);
    }

    console.log('Restarting workflow...');
    await page.getByRole('button', { name: 'Start' }).click();
    await expect(page.getByRole('button', { name: 'Stop' })).toBeVisible({ timeout: 20000 });

    console.log('Injecting final batch (30 rows)...');
    for (let i = 70; i < 100; i++) {
        await client.query("INSERT INTO users (name, email) VALUES ($1, $2)", [`User ${i}`, `user${i}@test.com`]);
        await client.query("INSERT INTO orders (user_id, amount, status) VALUES ($1, $2, $3)", [i, 10.5 * i, 'pending']);
    }
    await client.end();

    // 7. Verify Data and Stability
    console.log('Verifying sinks (target 100 rows minimum across paths)...');
    const sinkClient = new Client({ ...dbConfig, database: sinkDB });
    await sinkClient.connect();
    
    await expect(async () => {
        const uRes = await sinkClient.query("SELECT count(*) FROM users_heavy");
        const oRes = await sinkClient.query("SELECT count(*) FROM orders_heavy");
        const uCount = parseInt(uRes.rows[0].count);
        const oCount = parseInt(oRes.rows[0].count);
        console.log(`Current counts - Users Sink: ${uCount}, Orders Sink: ${oCount}`);
        expect(uCount).toBeGreaterThanOrEqual(100);
        expect(oCount).toBeGreaterThanOrEqual(200);
    }).toPass({ timeout: 180000, intervals: [5000] });

    await sinkClient.end();

    // 8. Verify UI Diagnostics
    console.log('Verifying Live Logs and Trace...');
    await page.goto(`/workflows/${workflowIDs[0]}`);
    const logItems = page.locator('text=processed').or(page.locator('text=written to sink')).first();
    await expect(logItems).toBeVisible({ timeout: 60000 });
    console.log('Live logs visible');

    await logItems.click();
    await expect(page.locator('text=Message Trace').or(page.locator('text=Payload'))).toBeVisible();
    console.log('Message trace visible');

    // 9. Cleanup
    console.log('Cleaning up workers...');
    for (const proc of workers) {
        proc.kill();
    }
  });

  test.afterAll(async () => {
    for (const proc of workers) {
        proc.kill();
    }
  });
});
