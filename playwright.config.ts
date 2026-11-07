import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './tests/e2e',
  workers: 1,
  timeout: 90000,
  expect: { timeout: 15000 },
  outputDir: './output/playwright',
  reporter: [['list']],
  use: { channel: 'chrome', headless: true, trace: 'off', screenshot: 'off', video: 'off' },
})
