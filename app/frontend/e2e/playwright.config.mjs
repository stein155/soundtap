// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// The end-to-end tests: the built web UI in Chromium against the demo speaker
// (docs/tools/demo-server.mjs on :8080). `make test-e2e` runs them in the Makefile's pinned
// Playwright image through e2e/in-container.sh, which starts the server.
import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: '.',
  testMatch: '*.spec.mjs',
  // One demo speaker, whose state the tests change: one test at a time.
  workers: 1,
  fullyParallel: false,
  forbidOnly: true,
  timeout: 45000,
  expect: { timeout: 10000 },
  reporter: [['list']],
  use: {
    baseURL: 'http://localhost:8080',
    browserName: 'chromium',
    viewport: { width: 390, height: 844 },
    locale: 'en-US',
    colorScheme: 'light',
  },
});
