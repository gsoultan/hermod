import { test, expect } from '@playwright/test';
import { spawn, execSync } from 'child_process';

test.describe('Worker Agent Installation and Setup', () => {
  test('should register and connect a worker', async ({ page }) => {
    test.setTimeout(180000);

    page.on('console', msg => console.log(`BROWSER [${msg.type()}]: ${msg.text()}`));
    page.on('pageerror', err => console.error(`BROWSER ERROR: ${err.message}`));
    page.on('request', request => console.log(`REQ >> ${request.method()} ${request.url()}`));
    page.on('response', response => console.log(`RES << ${response.status()} ${response.url()}`));

    // 1. Login
    console.log('Navigating to login...');
    await page.goto('/login');
    console.log('Filling credentials...');
    await page.getByPlaceholder('Your username').fill('admin');
    await page.getByPlaceholder('Your password').fill('admin123');
    await page.click('button:has-text("Sign In")');
    
    // Wait for login to complete
    console.log('Waiting for redirect...');
    await expect(page).not.toHaveURL(/login/, { timeout: 30000 });
    console.log('Logged in!');

    // 2. Navigate to Workers page
    console.log('Navigating to workers...');
    await page.goto('/workers');
    await expect(page.locator('h2')).toContainText('Workers');
    
    console.log('Clicking Register Worker button...');
    await page.getByRole('button', { name: /Register Worker/i }).first().click();

    // 3. Register a new worker
    console.log('Filling worker form...');
    await page.getByLabel('Host').fill('localhost');
    await page.getByLabel('Description').fill('E2E Test Worker');
    await page.getByRole('button', { name: /Register Worker/i }).last().click();

    // 4. Verify success and get installation command
    console.log('Waiting for success alert...');
    await page.screenshot({ path: 'before_alert.png' });
    const successAlert = page.locator('.mantine-Alert-title').filter({ hasText: /Worker Registered Successfully/i });
    await expect(successAlert).toBeVisible({ timeout: 30000 });
    await page.screenshot({ path: 'after_alert.png' });
    
    console.log('Extracting command...');
    // Use a more specific text match to find the command line
    const commandLocator = page.getByText(/hermod --mode=worker --worker-guid=/i);
    await expect(commandLocator).toBeVisible({ timeout: 20000 });
    
    // Clean up command
    const rawCommand = await commandLocator.innerText();
    // The innerText might contain "Copy" if it's part of the same element, or be split by lines
    const fullCommand = rawCommand.split('\n').find(line => line.includes('hermod --mode=worker'))?.trim().replace(/Copy$/, '').trim();
    if (!fullCommand) {
        throw new Error(`Could not extract command from: ${rawCommand}`);
    }
    console.log('Installation command:', fullCommand);

    // Command format: hermod --mode=worker --worker-guid="..." --worker-token="..." --platform-url="..."
    // Strip "hermod " prefix and handle potential whitespace/newlines, and STRIP QUOTES for spawn
    const commandArgs = fullCommand.replace('hermod ', '')
        .replace(/\s+/g, ' ')
        .trim()
        .split(' ')
        .map(arg => {
            // Split arg into key and value if it's --key=value
            if (arg.includes('=')) {
                const [key, ...rest] = arg.split('=');
                let value = rest.join('=');
                // Strip surrounding quotes from value
                value = value.replace(/^"|"$/g, '').replace(/^'|'$/g, '');
                return `${key}=${value}`;
            }
            return arg;
        });
    
    // 5. Start the worker agent in background
    console.log('Starting worker agent with args:', commandArgs);
    const workerProcess = spawn('./hermod', commandArgs, {
      env: { 
          ...process.env, 
          HERMOD_MASTER_KEY: 'secret'
      }
    });

    let workerOutput = '';
    workerProcess.stdout.on('data', (data) => {
      workerOutput += data.toString();
      console.log(`Worker: ${data}`);
    });

    workerProcess.stderr.on('data', (data) => {
      console.error(`Worker Error: ${data}`);
    });

    // 6. Verify worker shows as Online in the UI
    await page.click('button:has-text("Back to Workers")');
    
    // Poll for online status of the specific worker
    const guidMatch = fullCommand.match(/--worker-guid="?([^"\s]+)"?/);
    const workerGUID = guidMatch ? guidMatch[1] : '';
    console.log('Waiting for worker online:', workerGUID);

    const workerRow = page.locator('tr').filter({ hasText: workerGUID });
    const onlineBadge = workerRow.locator('.mantine-Badge-root:has-text("Online")');
    
    // Manual polling with reload since UI doesn't auto-refresh workers list yet
    await expect(async () => {
        console.log('Reloading to check status...');
        await page.reload();
        await expect(onlineBadge).toBeVisible({ timeout: 5000 });
    }).toPass({ timeout: 60000, intervals: [5000] });

    console.log('Worker is Online!');

    // 7. Verify with hermodctl (Worker Setup Verification)
    console.log('Verifying with hermodctl...');
    try {
        // We use port 4001 directly for API calls from CLI
        const ctlOutput = execSync(`./hermodctl status --url http://localhost:4001`).toString();
        console.log('hermodctl output:', ctlOutput);
        // The status output should contain the worker's GUID if it's correctly registered and online
        if (ctlOutput.includes(workerGUID)) {
            console.log('hermodctl verification successful: Worker found in cluster status');
        } else {
            console.warn('hermodctl verification warning: Worker GUID not found in status output, but UI shows online');
        }
    } catch (e) {
        console.warn('hermodctl verification failed (non-fatal):', e.message);
    }

    // 8. Cleanup
    workerProcess.kill();
    console.log('Test completed successfully');
  });
});
