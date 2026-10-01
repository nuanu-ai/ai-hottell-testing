import { defineConfig, devices } from '@playwright/test';

// Runs against the stand `task e2e` starts from compose.yaml + compose.e2e.yaml.
export default defineConfig({
  testDir: 'e2e',
  // Every scenario shares one database, so they run one at a time.
  fullyParallel: false,
  workers: 1,
  forbidOnly: true,
  reporter: 'list',
  use: {
    baseURL: 'http://localhost:18080',
    trace: 'retain-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
});
