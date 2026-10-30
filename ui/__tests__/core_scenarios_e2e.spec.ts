import { test, expect } from '@playwright/test';
import { E2E_USER, E2E_PASS } from './support/auth';
import { acceptConfirm } from './support/confirm';

test.describe('Hermod Core Scenarios E2E', () => {
  test.beforeEach(async ({ page }) => {
    // Login
    await page.goto('/login');
    await page.getByPlaceholder('Your username').fill(E2E_USER);
    await page.getByPlaceholder('Your password').fill(E2E_PASS);
    await page.click('button:has-text("Sign In")');
    await expect(page).not.toHaveURL(/login/, { timeout: 30000 });
    // Wait for Shell to load
    await expect(page.getByText('Hermod', { exact: true }).first()).toBeVisible({ timeout: 15000 });
  });

  test('Dashboard should display stats', async ({ page }) => {
    // Overview is the default page after login
    await expect(page.getByText(/Active Pipelines/i).first()).toBeVisible({ timeout: 15000 });
    await expect(page.getByText(/Node Cluster/i).first()).toBeVisible();
    await expect(page.getByText(/Throughput/i).first()).toBeVisible();
  });

  test('User Management: CRUD operations', async ({ page }) => {
    const username = `u_${Date.now()}`;
    const fullName = `Test User ${username}`;
    
    // 1. Navigate to Users: the sidebar has five places, People sits under Settings.
    await page.getByRole('navigation', { name: 'Main' }).getByText('Settings', { exact: true }).click();
    await page.waitForURL(/\/settings/);
    await page.getByText('People', { exact: true }).click();
    await page.waitForURL(/\/users/);
    
    // 2. Add User
    await page.getByRole('button', { name: 'Add person' }).click();
    
    await page.getByLabel(/Username/i).first().fill(username);
    await page.getByLabel(/Full Name/i).fill(fullName);
    await page.getByLabel(/Email/i).fill(`${username}@example.com`);
    await page.getByLabel(/Password/i).first().fill('password123');
    await page.getByRole('button', { name: /Create User/i }).click();
    
    await page.waitForURL(/\/users/);

    // 3. Search, then assert. The list accumulates and paginates, so a freshly
    // created user is not necessarily rendered on the first page — asserting on
    // the unfiltered list made this fail for a row that existed.
    const people = page.getByRole('list', { name: 'People' });
    const personRow = () => people.getByRole('listitem').filter({ hasText: username });
    await page.getByPlaceholder(/Search people/i).fill(username);
    await expect(personRow()).toBeVisible();

    // 4. Edit User (the search filter from step 3 is still applied here).
    await page.getByRole('button', { name: `More for ${username}` }).click();
    await page.getByRole('menuitem', { name: 'Edit' }).click();
    await page.getByLabel(/Full Name/i).fill(fullName + ' Edited');
    await page.getByRole('button', { name: /Save Changes/i }).click();
    // A bare /users/ pattern also matches /users/<id>/edit, so navigate to the
    // list explicitly. The fresh load also sidesteps a stale cached list, which
    // makes this assert persistence rather than cache-invalidation timing.
    await page.goto('/users');
    await page.getByPlaceholder(/Search people/i).fill(username);
    await expect(personRow()).toContainText(fullName + ' Edited');

    // 5. Delete User. Saving navigated back to the unfiltered list, so re-apply
    // the search or the row is not rendered to click.
    await page.getByPlaceholder(/Search people/i).fill(username);
    await expect(personRow()).toBeVisible();
    // Confirmation is the in-app ConfirmProvider modal, not window.confirm, so
    // it has to be accepted through the DOM.
    await page.getByRole('button', { name: `More for ${username}` }).click();
    await page.getByRole('menuitem', { name: 'Delete' }).click();
    await acceptConfirm(page);
    await expect(personRow()).not.toBeVisible({ timeout: 10000 });
  });

  test('VHost Management: CRUD operations', async ({ page }) => {
    const vhostName = `v_${Date.now()}`;
    const description = 'Test VHost Description';

    // 1. Navigate to VHosts: Settings › Workspace opens the Virtual hosts tab.
    await page.getByRole('navigation', { name: 'Main' }).getByText('Settings', { exact: true }).click();
    await page.waitForURL(/\/settings/);
    await page.getByText('Workspace', { exact: true }).click();
    await page.waitForURL(/tab=vhosts/);

    // 2. Add VHost
    await page.getByRole('button', { name: 'Add virtual host' }).click();
    await page.getByLabel(/Name/i).fill(vhostName);
    await page.getByLabel(/Description/i).fill(description);
    await page.getByRole('button', { name: /Create VHost/i }).click();
    
    await page.waitForURL(/\/vhosts/);
    const vhostRow = page.getByRole('list', { name: 'Virtual hosts' }).getByRole('listitem').filter({ hasText: vhostName });
    await expect(vhostRow).toBeVisible();

    // 3. Edit VHost
    await page.getByRole('button', { name: `More for ${vhostName}` }).click();
    await page.getByRole('menuitem', { name: 'Edit' }).click();
    await page.getByLabel(/Description/i).fill(description + ' Edited');
    await page.getByRole('button', { name: /Save Changes/i }).click();
    await page.waitForURL(/\/vhosts/);
    await expect(page.getByText(description + ' Edited', { exact: true })).toBeVisible();

    // 4. Delete VHost (same in-app confirmation modal as above).
    await page.getByRole('button', { name: `More for ${vhostName}` }).click();
    await page.getByRole('menuitem', { name: 'Delete' }).click();
    await acceptConfirm(page);
    await expect(vhostRow).not.toBeVisible({ timeout: 10000 });
  });

  test('Settings: Update SMTP settings', async ({ page }) => {
    // Navigate via Account Menu, then into Workspace, which holds the platform tabs
    await page.getByTitle('Account').click();
    await page.getByRole('menuitem', { name: /^Settings$/ }).click();
    await page.waitForURL(/\/settings/);
    await page.getByText('Workspace', { exact: true }).click();
    await page.waitForURL(/tab=vhosts/);

    // Click Connectivity tab
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
    await page.getByRole('navigation', { name: 'Main' }).getByText('Settings', { exact: true }).click();
    await page.waitForURL(/\/settings/);
    await page.getByText('Workspace', { exact: true }).click();
    await page.waitForURL(/tab=vhosts/);
    await page.getByRole('button', { name: 'Add virtual host' }).click();
    await page.getByLabel(/Name/i).fill(vhostName);
    await page.getByRole('button', { name: /Create VHost/i }).click();
    await page.waitForURL(/\/vhosts/);
    await expect(page.getByRole('list', { name: 'Virtual hosts' }).getByRole('listitem').filter({ hasText: vhostName })).toBeVisible();

    // Check Audit Logs: a tab of Activity
    await page.getByRole('navigation', { name: 'Main' }).getByText('Activity', { exact: true }).click();
    await page.waitForURL(/\/activity/);
    await page.getByRole('tab', { name: 'Audit log' }).click();
    await page.waitForURL(/tab=audit/);
    
    // Case-insensitive check for create/CREATE
    await expect(page.locator('table')).toContainText(/create/i, { timeout: 30000 });
    await expect(page.locator('table')).toContainText(/vhost/i, { timeout: 30000 });
    
    // Filter by entity type
    await page.getByLabel(/Entity Type/i).fill('vhost');
    // Wait for the table to refresh and still contain vhost logs
    await expect(page.locator('table')).toContainText(/vhost/i, { timeout: 10000 });
  });
});
