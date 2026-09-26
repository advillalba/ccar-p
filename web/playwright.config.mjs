import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './tests',
  timeout: 30000,
  fullyParallel: true,
  reporter: 'line',
  use: {
    baseURL: 'http://127.0.0.1:4322',
    viewport: { width: 375, height: 812 },
    trace: 'retain-on-failure'
  },
  webServer: [
    {
      command: 'node tests/mock-backend.mjs',
      url: 'http://127.0.0.1:19091/api/v1/notes',
      reuseExistingServer: false,
      timeout: 30000
    },
    {
      command: 'HOST=127.0.0.1 PORT=4322 BACKEND_URL=http://127.0.0.1:19091 npm run start',
      url: 'http://127.0.0.1:4322/',
      reuseExistingServer: false,
      timeout: 30000
    }
  ]
});
