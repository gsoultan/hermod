import { test, expect } from '@playwright/test';
import { Client } from 'pg';
import { execSync } from 'child_process';

/**
 * PostgreSQL CDC Table Tracking E2E Test
 * 
 * This test verifies the ability to add and remove tables from an active 
 * PostgreSQL CDC source using the UI configuration form.
 * 
 * Scenarios:
 * 1. Initial setup: track 'users' table -> verify data flow.
 * 2. Add table: add 'audit_log' to tracking -> verify data flow for both.
 * 3. Remove table: remove 'users' from tracking -> verify data flow only for 'audit_log'.
 */
test.describe('PostgreSQL CDC Table Tracking E2E', () => {
  const sourceDB = 'hermod_test_source';
  const sinkDB = 'hermod_test_sink';
  const dbConfig = {
    host: '127.0.0.1',
    port: 5432,
    user: 'postgres',
    password: 'postgres',
  };

  test.beforeEach(async ({ page }) => {
    // Clean up slots/pubs to ensure a fresh environment
    try {
      execSync('rtk go run ./scripts/drop_all_slots', { cwd: process.cwd() });
    } catch (e) {
      console.log('Pre-test cleanup (slots):', e.message);
    }

    page.on('console', msg => console.log('BROWSER:', msg.text()));
    page.on('pageerror', err => console.log('BROWSER ERROR:', err.message));

    // Login
    await page.goto('/login');
    await page.getByPlaceholder('Your username').fill('admin');
    await page.getByPlaceholder('Your password').fill('admin123');
    await page.getByRole('button', { name: 'Sign In' }).click();
    await expect(page).toHaveURL('/', { timeout: 30000 });
  });

  test('should handle adding and removing tables from CDC tracking via UI form', async ({ page }) => {
    test.setTimeout(300000);
    const timestamp = Date.now();
    const vhostName = `track_vhost_${timestamp}`;
    const workflowName = `Track Workflow ${timestamp}`;

    // 1. Setup Infrastructure via API (Setup stage)
    const setupData = await page.evaluate(async ({ vhostName, timestamp, sourceDB, sinkDB, workflowName }) => {
      const token = localStorage.getItem('hermod_token');
      const headers = { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` };

      await fetch('/api/vhosts', { method: 'POST', headers, body: JSON.stringify({ name: vhostName }) });

      const cdcSrc = await (await fetch('/api/sources', {
        method: 'POST', headers,
        body: JSON.stringify({
          name: `CDC Track ${timestamp}`, vhost: vhostName, type: 'postgres',
          config: { 
            host: '127.0.0.1', port: '5432', user: 'postgres', password: 'postgres', dbname: sourceDB, 
            tables: 'users', use_cdc: 'true', slot_name: `track_sl_${timestamp}`, publication_name: `track_pb_${timestamp}`, slot_reclaim: 'true' 
          }
        })
      })).json();

      const sinkUsers = await (await fetch('/api/sinks', {
        method: 'POST', headers,
        body: JSON.stringify({
          name: `Sink Users ${timestamp}`, vhost: vhostName, type: 'postgres',
          config: { 
            host: '127.0.0.1', port: '5432', user: 'postgres', password: 'postgres', dbname: sinkDB, table: 'users_sink',
            column_mappings: JSON.stringify([
              {source_field: '$.id', target_column: 'id', is_primary_key: true, data_type: 'integer'},
              {source_field: '$.name', target_column: 'full_name', data_type: 'text'},
              {source_field: '$.email', target_column: 'email', data_type: 'text'}
            ])
          }
        })
      })).json();

      const sinkAudit = await (await fetch('/api/sinks', {
        method: 'POST', headers,
        body: JSON.stringify({
          name: `Sink Audit ${timestamp}`, vhost: vhostName, type: 'postgres',
          config: { 
            host: '127.0.0.1', port: '5432', user: 'postgres', password: 'postgres', dbname: sinkDB, table: 'comp_audit_sink',
            column_mappings: JSON.stringify([
              {source_field: '$.id', target_column: 'id', is_primary_key: true, data_type: 'integer'},
              {source_field: '$.status', target_column: 'status', data_type: 'text'}
            ])
          }
        })
      })).json();

      const workflow = await (await fetch('/api/workflows', {
        method: 'POST', headers,
        body: JSON.stringify({
          name: workflowName,
          vhost: vhostName,
          nodes: [
            { id: 'src', type: 'source', ref_id: cdcSrc.id, x: 50, y: 150 },
            { id: 'f_users', type: 'transformation', config: { transType: 'filter_data', field: 'table', operator: 'contains', value: 'users' }, x: 300, y: 50 },
            { id: 'f_audit', type: 'transformation', config: { transType: 'filter_data', field: 'table', operator: 'contains', value: 'audit_log' }, x: 300, y: 250 },
            { id: 'snk_users', type: 'sink', ref_id: sinkUsers.id, x: 550, y: 50 },
            { id: 'snk_audit', type: 'sink', ref_id: sinkAudit.id, x: 550, y: 250 },
          ],
          edges: [
            { id: 'e1', source_id: 'src', target_id: 'f_users' },
            { id: 'e2', source_id: 'src', target_id: 'f_audit' },
            { id: 'e3', source_id: 'f_users', target_id: 'snk_users' },
            { id: 'e4', source_id: 'f_audit', target_id: 'snk_audit' },
          ]
        })
      })).json();

      return { cdcSrcId: cdcSrc.id, workflowId: workflow.id };
    }, { vhostName, timestamp, sourceDB, sinkDB, workflowName });

    const workflowId = setupData.workflowId;

    // 2. Scenario 1: Only 'users' table is tracked
    console.log('Scenario 1: Starting workflow...');
    await page.goto(`/workflows/${workflowId}`);
    
    // Attempt to start
    const startBtn = page.getByRole('button', { name: 'Start' });
    await startBtn.click();
    
    // Wait for button to change to Stop, or look for errors
    try {
        await expect(page.getByRole('button', { name: 'Stop' })).toBeVisible({ timeout: 60000 });
    } catch (e) {
        console.log('Workflow failed to start or Stop button not visible. Checking for errors...');
        const errorToast = page.locator('.mantine-Notification-root', { hasText: /error|failed/i });
        if (await errorToast.isVisible()) {
            console.log('Error Toast detected:', await errorToast.innerText());
        }
        throw e;
    }
    
    // Wait for CDC initialization (Postgres needs time to start streaming)
    console.log('Waiting for CDC initialization (25s)...');
    await new Promise(r => setTimeout(r, 25000));

    const sourceClient = new Client({ ...dbConfig, database: sourceDB });
    await sourceClient.connect();
    const sinkClient = new Client({ ...dbConfig, database: sinkDB });
    await sinkClient.connect();
    
    // Clear sinks
    await sinkClient.query("DELETE FROM users_sink");
    await sinkClient.query("DELETE FROM comp_audit_sink");

    const email1 = `user1_${timestamp}@test.com`;
    await sourceClient.query("INSERT INTO users (name, email) VALUES ($1, $2)", ['User One', email1]);
    await sourceClient.query("INSERT INTO audit_log (status) VALUES ($1)", ['Status One - Should be ignored']);

    // Verify users flowed, audit did not
    let data1 = null;
    for (let i = 0; i < 10; i++) {
        const res = await sinkClient.query("SELECT * FROM users_sink WHERE email = $1", [email1]);
        if (res.rows.length > 0) { data1 = res.rows[0]; break; }
        await new Promise(r => setTimeout(r, 2000));
    }
    expect(data1).not.toBeNull();
    const auditRes1 = await sinkClient.query("SELECT * FROM comp_audit_sink WHERE status LIKE 'Status One%'");
    expect(auditRes1.rows.length).toBe(0);
    console.log('Scenario 1 verified.');

    // 3. Scenario 2: Add 'audit_log' to tracking via UI form
    console.log('Scenario 2: Adding audit_log to tracking...');
    await page.goto(`/workflows/${workflowId}`);
    await page.getByRole('button', { name: 'Stop' }).click();
    await expect(page.getByRole('button', { name: 'Start' })).toBeVisible({ timeout: 60000 });

    await page.goto(`/sources/${setupData.cdcSrcId}/edit`);
    await page.getByRole('button', { name: 'Next Step' }).click(); // To Connection step
    
    // Re-enter password because UI might not pre-fill it for security
    await page.getByPlaceholder('password').fill('postgres');
    
    // Verify 'users' tag is there
    await expect(page.locator('.mantine-TagsInput-pill', { hasText: 'users' })).toBeVisible();
    
    // Add 'audit_log'
    const tagsInput = page.getByPlaceholder('Type a table name and press Enter');
    await tagsInput.click();
    await tagsInput.pressSequentially('audit_log');
    await tagsInput.press('Enter');
    
    // Verify both tags are there
    await expect(page.locator('.mantine-TagsInput-pill', { hasText: 'users' })).toBeVisible();
    await expect(page.locator('.mantine-TagsInput-pill', { hasText: 'audit_log' })).toBeVisible();

    await page.getByRole('button', { name: 'Next Step' }).click(); // To Reliability
    await page.getByRole('button', { name: 'Next Step' }).click(); // To Completed
    await page.getByRole('button', { name: 'Update Source' }).click();
    await expect(page).toHaveURL('/sources');

    await page.goto(`/workflows/${workflowId}`);
    
    // Attempt to start Scenario 2
    await page.getByRole('button', { name: 'Start' }).click();
    try {
        await expect(page.getByRole('button', { name: 'Stop' })).toBeVisible({ timeout: 60000 });
    } catch (e) {
        console.log('Scenario 2: Workflow failed to start. Checking for errors...');
        const errorToast = page.locator('.mantine-Notification-root', { hasText: /error|failed/i });
        if (await errorToast.isVisible()) console.log('Error Toast detected:', await errorToast.innerText());
        throw e;
    }
    console.log('Scenario 2: Waiting for CDC initialization (25s)...');
    await new Promise(r => setTimeout(r, 25000));

    const email2 = `user2_${timestamp}@test.com`;
    await sourceClient.query("INSERT INTO users (name, email) VALUES ($1, $2)", ['User Two', email2]);
    await sourceClient.query("INSERT INTO audit_log (status) VALUES ($1)", ['Status Two - Should be tracked']);

    // Verify both flowed
    let userData2 = null;
    let auditData2 = null;
    for (let i = 0; i < 15; i++) {
        if (!userData2) {
            const res = await sinkClient.query("SELECT * FROM users_sink WHERE email = $1", [email2]);
            if (res.rows.length > 0) userData2 = res.rows[0];
        }
        if (!auditData2) {
            const res = await sinkClient.query("SELECT * FROM comp_audit_sink WHERE status = 'Status Two - Should be tracked'");
            if (res.rows.length > 0) auditData2 = res.rows[0];
        }
        if (userData2 && auditData2) break;
        await new Promise(r => setTimeout(r, 2000));
    }
    expect(userData2).not.toBeNull();
    expect(auditData2).not.toBeNull();
    console.log('Scenario 2 verified.');

    // 4. Scenario 3: Remove 'users' from tracking via UI form
    console.log('Scenario 3: Removing users from tracking...');
    await page.goto(`/workflows/${workflowId}`);
    await page.getByRole('button', { name: 'Stop' }).click();
    await expect(page.getByRole('button', { name: 'Start' })).toBeVisible({ timeout: 60000 });

    await page.goto(`/sources/${setupData.cdcSrcId}/edit`);
    await page.getByRole('button', { name: 'Next Step' }).click();
    
    // Re-enter password
    await page.getByPlaceholder('password').fill('postgres');
    
    // Click 'X' on the 'users' tag pill
    await page.locator('.mantine-TagsInput-pill', { hasText: 'users' }).locator('button').first().click();
    await expect(page.locator('.mantine-TagsInput-pill', { hasText: 'users' })).not.toBeVisible();
    await page.getByRole('button', { name: 'Next Step' }).click(); // To Reliability
    await page.getByRole('button', { name: 'Next Step' }).click(); // To Completed
    await page.getByRole('button', { name: 'Update Source' }).click();
    await expect(page).toHaveURL('/sources');

    await page.goto(`/workflows/${workflowId}`);
    
    // Attempt to start Scenario 3
    await page.getByRole('button', { name: 'Start' }).click();
    try {
        await expect(page.getByRole('button', { name: 'Stop' })).toBeVisible({ timeout: 60000 });
    } catch (e) {
        console.log('Scenario 3: Workflow failed to start. Checking for errors...');
        const errorToast = page.locator('.mantine-Notification-root', { hasText: /error|failed/i });
        if (await errorToast.isVisible()) console.log('Error Toast detected:', await errorToast.innerText());
        throw e;
    }
    console.log('Scenario 3: Waiting for CDC initialization (25s)...');
    await new Promise(r => setTimeout(r, 25000));

    const email3 = `user3_${timestamp}@test.com`;
    await sourceClient.query("INSERT INTO users (name, email) VALUES ($1, $2)", ['User Three', email3]);
    await sourceClient.query("INSERT INTO audit_log (status) VALUES ($1)", ['Status Three - Should be tracked']);

    // Verify only audit flowed
    let auditData3 = null;
    for (let i = 0; i < 10; i++) {
        const res = await sinkClient.query("SELECT * FROM comp_audit_sink WHERE status = 'Status Three - Should be tracked'");
        if (res.rows.length > 0) { auditData3 = res.rows[0]; break; }
        await new Promise(r => setTimeout(r, 2000));
    }
    expect(auditData3).not.toBeNull();
    const usersRes3 = await sinkClient.query("SELECT * FROM users_sink WHERE email = $1", [email3]);
    expect(usersRes3.rows.length).toBe(0);
    console.log('Scenario 3 verified.');

    await sourceClient.end();
    await sinkClient.end();
  });
});
