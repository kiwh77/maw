import path from 'node:path';
import { defineConfig, devices } from '@playwright/test';

// 浏览器版本由 @playwright/test 的精确版本固定（见 package-lock.json）。
const ROOT = path.resolve(__dirname, '../..');
const PORT = 47121;
const BASE_URL = `http://127.0.0.1:${PORT}`;

export default defineConfig({
  testDir: ROOT,
  // 各视图与公共组件可在自身目录发布 *.browser.spec.*，基座统一发现。
  testMatch: [
    'tests/browser/**/*.browser.spec.ts',
    'internal/**/*.browser.spec.ts',
    'clients/**/*.browser.spec.ts',
  ],
  outputDir: path.join(__dirname, 'reports', 'artifacts'),
  reporter: [
    ['list'],
    ['json', { outputFile: path.join(__dirname, 'reports', 'results.json') }],
  ],
  use: {
    baseURL: BASE_URL,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'off',
  },
  webServer: {
    command: `go run ./tests/browser/cmd/harness -addr 127.0.0.1:${PORT}`,
    cwd: ROOT,
    url: `${BASE_URL}/`,
    reuseExistingServer: false,
    timeout: 120_000,
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'], browserName: 'chromium' },
    },
  ],
});
