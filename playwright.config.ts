import { defineConfig } from '@playwright/test';

const requestedPort = process.env.E2E_PORT || '13009';
const port = Number(requestedPort);
if (
  !/^[0-9]+$/.test(requestedPort) ||
  !Number.isInteger(port) ||
  port < 1 ||
  port > 65535
) {
  throw new Error('E2E_PORT must be a decimal integer between 1 and 65535.');
}
const baseURL = `http://127.0.0.1:${port}`;

export default defineConfig({
  testDir: './tests',
  testMatch: '**/*.spec.ts',
  fullyParallel: false,
  workers: 1,
  timeout: 45_000,
  expect: { timeout: 8_000 },
  forbidOnly: Boolean(process.env.CI),
  retries: 0,
  reporter: [['list'], ['html', { open: 'never' }]],
  use: {
    baseURL,
    viewport: { width: 1440, height: 900 },
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    acceptDownloads: true,
  },
  projects: [
    { name: 'chromium', use: { browserName: 'chromium' } },
    { name: 'firefox', use: { browserName: 'firefox' } },
    { name: 'webkit', use: { browserName: 'webkit' } },
    {
      name: 'touch',
      use: {
        browserName: 'chromium',
        viewport: { width: 390, height: 844 },
        deviceScaleFactor: 2,
        hasTouch: true,
        isMobile: true,
      },
    },
  ],
  webServer: {
    command: 'node scripts/e2e-server.mjs',
    // Only this child process can announce a successful bind; a URL may belong
    // to another server that starts while the production build is running.
    wait: {
      stderr: new RegExp(
        `msg="desktop listening" url=http://127\\.0\\.0\\.1:${port} data=`,
      ),
    },
    timeout: 120_000,
    reuseExistingServer: false,
    // Let the server stop Go and remove its temporary database before exiting.
    gracefulShutdown: { signal: 'SIGTERM', timeout: 10_000 },
  },
});
