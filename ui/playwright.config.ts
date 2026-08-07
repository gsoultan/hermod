import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: './__tests__',
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: 1,
  reporter: 'list',
  use: {
    // Must match vite.config.ts's pinned `server.port` (5175). Pointing at 5173
    // meant every spec using a relative goto() hit a closed port, so the layout
    // and form audits CI runs on each push were failing against nothing.
    baseURL: process.env.AUDIT_BASE_URL || 'http://localhost:5175',
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
