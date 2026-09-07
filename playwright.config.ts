import { defineConfig, devices } from '@playwright/test';
import { uiBaseURL } from './scripts/dev-ports';

export default defineConfig({
  testDir: './ui/__tests__',
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: 1,
  reporter: 'list',
  timeout: 60000,
  use: {
    // Follows the port scripts/dev.sh actually bound rather than assuming 5175,
    // which it only gets when 5175 was free. Still overridable so a run can be
    // pointed at an isolated stack (a throwaway database on another port)
    // instead of the dev instance you are working in.
    baseURL: process.env.PLAYWRIGHT_BASE_URL || uiBaseURL(),
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
    actionTimeout: 15000,
    navigationTimeout: 30000,
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
});
