import { defineConfig, devices } from '@playwright/test';
import { uiBaseURL } from '../scripts/dev-ports';

export default defineConfig({
  testDir: './__tests__',
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: 1,
  reporter: 'list',
  use: {
    // Must match the port vite actually bound, which is 5175 only when 5175 was
    // free — scripts/dev.sh steps up when it is not. Pointing at a port nothing
    // is serving does not fail loudly: every spec using a relative goto() just
    // hits a closed port, and the layout and form audits CI runs on each push
    // silently pass against nothing.
    baseURL: process.env.AUDIT_BASE_URL || uiBaseURL(),
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
});
