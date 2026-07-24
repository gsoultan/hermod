import { test, expect } from '@playwright/test';
import { Client } from 'pg';

test.describe('SQL Transformation and Query Builder E2E', () => {
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
    console.log('Navigating to login...');
    await page.goto('http://localhost:5173/login');
    await page.getByLabel('Username').first().fill('admin');
    await page.locator('input[type="password"]').fill('admin123');
    console.log('Clicking Sign In...');
    await page.getByRole('button', { name: 'Sign In' }).click();
    console.log('Waiting for navigation...');
    await expect(page).toHaveURL('http://localhost:5173/', { timeout: 30000 });
    console.log('Login successful');
  });

  test('Exact SQL Transformation (execute_sql)', async ({ page }) => {
    test.setTimeout(180000);
    const timestamp = Date.now();
    const vhostName = `sql_vhost_${timestamp}`;

    // 1. Create VHost
    await page.evaluate(async (name) => {
        const token = localStorage.getItem('hermod_token');
        await fetch('/api/vhosts', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` },
          body: JSON.stringify({ name, description: 'SQL E2E VHost' })
        });
    }, vhostName);

    // 2. Setup audit_log table in source
    const client = new Client({ ...dbConfig, database: sourceDB });
    await client.connect();
    await client.query(`DROP TABLE IF EXISTS audit_log`);
    await client.query(`
        CREATE TABLE audit_log (
            id SERIAL PRIMARY KEY,
            status TEXT
        )
    `);
    await client.query("INSERT INTO audit_log (status) VALUES ('pending')");
    await client.end();

    // 3. Create Source via API
    const sourceRes = await page.evaluate(async ({vhost, timestamp, sourceDB}) => {
        const token = localStorage.getItem('hermod_token');
        const res = await fetch('/api/sources', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` },
          body: JSON.stringify({
            name: `SQL Source ${timestamp}`,
            vhost: vhost,
            type: 'postgres',
            config: {
                host: 'localhost',
                port: '5432',
                user: 'postgres',
                password: 'postgres',
                dbname: sourceDB,
                use_cdc: 'true',
                slot_name: `sql_slot_${timestamp}`,
                publication_name: `sql_pub_${timestamp}`,
                tables: 'users'
            }
          })
        });
        return res.json();
    }, {vhost: vhostName, timestamp, sourceDB});
    const sourceID = sourceRes.id;

    // 4. Create Sink via API
    const sinkRes = await page.evaluate(async ({vhost, timestamp, sinkDB}) => {
        const token = localStorage.getItem('hermod_token');
        const res = await fetch('/api/sinks', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` },
          body: JSON.stringify({
            name: `SQL Sink ${timestamp}`,
            vhost: vhost,
            type: 'postgres',
            config: {
                host: 'localhost',
                port: '5432',
                user: 'postgres',
                password: 'postgres',
                dbname: sinkDB,
                table: 'users_sink'
            }
          })
        });
        return res.json();
    }, {vhost: vhostName, timestamp, sinkDB});
    const sinkID = sinkRes.id;

    // 5. Create Workflow with execute_sql
    const workflowName = `SQL Workflow ${timestamp}`;
    const workflowRes = await page.evaluate(async ({name, vhost, sourceID, sinkID}) => {
        const token = localStorage.getItem('hermod_token');
        const res = await fetch('/api/workflows', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` },
            body: JSON.stringify({
                name,
                vhost,
                active: true,
                nodes: [
                    { id: 'source-1', type: 'source', x: 0, y: 0, ref_id: sourceID },
                    { 
                        id: 'trans-1', 
                        type: 'transformation', 
                        x: 300, y: 0, 
                        config: { 
                            transType: 'execute_sql',
                            sourceId: sourceID,
                            queryTemplate: "UPDATE audit_log SET status = 'success' WHERE status = 'pending'"
                        } 
                    },
                    { id: 'sink-1', type: 'sink', x: 600, y: 0, ref_id: sinkID }
                ],
                edges: [
                    { id: 'e1', source_id: 'source-1', target_id: 'trans-1' },
                    { id: 'e2', source_id: 'trans-1', target_id: 'sink-1' }
                ]
            })
        });
        if (!res.ok) {
            const err = await res.text();
            throw new Error(`Failed to create workflow: ${err}`);
        }
        return res.json();
    }, {name: workflowName, vhost: vhostName, sourceID, sinkID});
    const workflowID = workflowRes.id;

    // 5.1 Start Workflow via API
    await page.evaluate(async (id) => {
        const token = localStorage.getItem('hermod_token');
        await fetch(`/api/workflows/${id}/toggle`, {
            method: 'POST',
            headers: { 'Authorization': `Bearer ${token}` }
        });
    }, workflowID);
    console.log(`Workflow ${workflowID} started`);
    await new Promise(r => setTimeout(r, 10000)); // Wait for CDC to connect

    // 6. Trigger data change
    console.log('Inserting data into source...');
    const client2 = new Client({ ...dbConfig, database: sourceDB });
    await client2.connect();
    const insertRes = await client2.query("INSERT INTO users (name, email) VALUES ($1, $2) RETURNING id", [`User ${timestamp}`, `user${timestamp}@example.com`]);
    const insertedId = insertRes.rows[0].id;
    await client2.end();

    // 7. Verify execute_sql side effect
    console.log(`Waiting for CDC and SQL execution...`);
    let auditFound = false;
    for (let i = 0; i < 15; i++) {
        await new Promise(r => setTimeout(r, 2000));
        const client3 = new Client({ ...dbConfig, database: sourceDB });
        try {
            await client3.connect();
            const auditRes = await client3.query("SELECT * FROM audit_log WHERE status = 'success'");
            if (auditRes.rows.length > 0) {
                auditFound = true;
                console.log('Audit log entry updated to success!');
                break;
            }
        } finally {
            await client3.end();
        }
    }
    expect(auditFound).toBe(true);
  });

  test('SQL Query Builder API', async ({ page }) => {
    test.setTimeout(180000);
    const timestamp = Date.now();
    const vhostName = `apiqb_vhost_${timestamp}`;

    // 1. Create VHost and Source via API
    const setup = await page.evaluate(async ({vhost, timestamp, sourceDB}) => {
        const token = localStorage.getItem('hermod_token');
        await fetch('/api/vhosts', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` },
          body: JSON.stringify({ name: vhost })
        });
        const sRes = await fetch('/api/sources', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` },
          body: JSON.stringify({
            name: `QB Source ${timestamp}`,
            vhost: vhost,
            type: 'postgres',
            config: { host: 'localhost', port: '5432', user: 'postgres', password: 'postgres', dbname: sourceDB }
          })
        });
        const source = await sRes.json();
        return { source };
    }, {vhost: vhostName, timestamp, sourceDB});

    // 2. Test Run Query API
    console.log('Testing Run Query API...');
    const results = await page.evaluate(async ({source}) => {
        const token = localStorage.getItem('hermod_token');
        const res = await fetch('/api/sources/query', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` },
            body: JSON.stringify({
                config: { type: source.type, config: source.config },
                query: 'SELECT * FROM users LIMIT 5',
                sampleData: {}
            })
        });
        return res.json();
    }, {source: setup.source});

    expect(Array.isArray(results)).toBe(true);
    expect(results.length).toBeGreaterThan(0);
    expect(results[0]).toHaveProperty('name');
    expect(results[0]).toHaveProperty('email');
  });
});
