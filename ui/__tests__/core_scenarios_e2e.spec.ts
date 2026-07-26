import { test, expect } from '@playwright/test';

test.describe('Hermod Core Scenarios E2E', () => {
  test.beforeEach(async ({ page }) => {
    // Login
    await page.goto('/login');
    await page.getByPlaceholder('Your username').fill('admin');
    await page.getByPlaceholder('Your password').fill('admin123');
    await page.click('button:has-text("Sign In")');
    await expect(page).not.toHaveURL(/login/, { timeout: 30000 });
    // Wait for Shell to load
    await expect(page.getByText('Hermod', { exact: true })).toBeVisible({ timeout: 15000 });
  });

  test('Dashboard should display stats', async ({ page }) => {
    // Overview is the default page after login
    await expect(page.getByText(/Active Pipelines/i).first()).toBeVisible({ timeout: 15000 });
    await expect(page.getByText(/Node Cluster/i).first()).toBeVisible();
    await expect(page.getByText(/Throughput/i).first()).toBeVisible();
  });

  test('User Management: CRUD operations', async ({ page }) => {
    const username = `u_${Date.now()}`;
    const fullName = 'Test User E2E';
    
    // 1. Navigate to Users
    await page.getByText('Users', { exact: true }).click();
    await page.waitForURL(/\/users/);
    
    // 2. Add User
    await page.getByRole('button', { name: /Add User/i }).click();
    
    await page.getByLabel(/Username/i).first().fill(username);
    await page.getByLabel(/Full Name/i).fill(fullName);
    await page.getByLabel(/Email/i).fill(`${username}@example.com`);
    await page.getByLabel(/Password/i).first().fill('password123');
    await page.getByRole('button', { name: /Create User/i }).click();
    
    await page.waitForURL(/\/users/);
    await expect(page.getByRole('cell', { name: username, exact: true })).toBeVisible();

    // 3. Search User
    await page.getByPlaceholder(/Search users/i).fill(username);
    await expect(page.locator('table')).toContainText(username);

    // 4. Edit User
    await page.locator('tr').filter({ has: page.getByRole('cell', { name: username, exact: true }) }).getByLabel(/Edit user/i).click();
    await page.getByLabel(/Full Name/i).fill(fullName + ' Edited');
    await page.getByRole('button', { name: /Save Changes/i }).click();
    await page.waitForURL(/\/users/);
    await expect(page.getByText(fullName + ' Edited', { exact: true })).toBeVisible();

    // 5. Delete User
    page.on('dialog', dialog => dialog.accept());
    await page.locator('tr').filter({ has: page.getByRole('cell', { name: username, exact: true }) }).getByLabel(/Delete user/i).click();
    await expect(page.getByRole('cell', { name: username, exact: true })).not.toBeVisible({ timeout: 10000 });
  });

  test('VHost Management: CRUD operations', async ({ page }) => {
    const vhostName = `v_${Date.now()}`;
    const description = 'Test VHost Description';

    // 1. Navigate to VHosts
    await page.getByText('Virtual Hosts', { exact: true }).click();
    await page.waitForURL(/\/vhosts/);

    // 2. Add VHost
    await page.getByRole('button', { name: /Add VHost/i }).click();
    await page.getByLabel(/Name/i).fill(vhostName);
    await page.getByLabel(/Description/i).fill(description);
    await page.getByRole('button', { name: /Create VHost/i }).click();
    
    await page.waitForURL(/\/vhosts/);
    await expect(page.getByRole('cell', { name: vhostName, exact: true })).toBeVisible();

    // 3. Edit VHost
    await page.locator('tr').filter({ has: page.getByRole('cell', { name: vhostName, exact: true }) }).getByLabel(/Edit vhost/i).click();
    await page.getByLabel(/Description/i).fill(description + ' Edited');
    await page.getByRole('button', { name: /Save Changes/i }).click();
    await page.waitForURL(/\/vhosts/);
    await expect(page.getByText(description + ' Edited', { exact: true })).toBeVisible();

    // 4. Delete VHost
    page.on('dialog', dialog => dialog.accept());
    await page.locator('tr').filter({ has: page.getByRole('cell', { name: vhostName, exact: true }) }).getByLabel(/Delete vhost/i).click();
    await expect(page.getByRole('cell', { name: vhostName, exact: true })).not.toBeVisible({ timeout: 10000 });
  });

  test('Settings: Update SMTP settings', async ({ page }) => {
    // Navigate via Account Menu
    await page.getByTitle('Account').click();
    await page.getByRole('menuitem', { name: /System Settings/i }).click();
    await page.waitForURL(/\/settings/);

    // Click Connectivity tab (the first one)
    await page.locator('button:has-text("Connectivity")').first().click();
    
    const smtpHost = `smtp.${Date.now()}.com`;
    await page.getByLabel(/SMTP Host/i).fill(smtpHost);
    await page.getByRole('button', { name: /Save Connectivity Settings/i }).click();
    
    await expect(page.getByText(/Settings saved successfully/i).or(page.getByText(/updated/i))).toBeVisible();
    await page.reload();
    await page.locator('button:has-text("Connectivity")').first().click();
    await expect(page.getByLabel(/SMTP Host/i)).toHaveValue(smtpHost);
  });

  test('Audit Logs: Display and Filter', async ({ page }) => {
    // Perform an action to generate a log
    const vhostName = `a_${Date.now()}`;
    await page.getByText('Virtual Hosts', { exact: true }).click();
    await page.waitForURL(/\/vhosts/);
    await page.getByRole('button', { name: /Add VHost/i }).click();
    await page.getByLabel(/Name/i).fill(vhostName);
    await page.getByRole('button', { name: /Create VHost/i }).click();
    await page.waitForURL(/\/vhosts/);
    await expect(page.getByRole('cell', { name: vhostName, exact: true })).toBeVisible();

    // Check Audit Logs
    await page.getByText('Audit Logs', { exact: true }).click();
    await page.waitForURL(/\/audit-logs/);
    
    // Case-insensitive check for create/CREATE
    await expect(page.locator('table')).toContainText(/create/i, { timeout: 30000 });
    await expect(page.locator('table')).toContainText(/vhost/i, { timeout: 30000 });
    
    // Filter by entity type
    await page.getByLabel(/Entity Type/i).fill('vhost');
    // Wait for the table to refresh and still contain vhost logs
    await expect(page.locator('table')).toContainText(/vhost/i, { timeout: 10000 });
  });
});
