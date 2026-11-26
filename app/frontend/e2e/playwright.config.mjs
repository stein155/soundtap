// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// The end-to-end tests: the built web UI in Chromium against the demo speaker
// (docs/tools/demo-server.mjs on :8080). `make test-e2e` runs them in the Makefile's pinned
// Playwright image through e2e/in-container.sh, which starts the server. pwa.spec.mjs runs
// as the home-screen app on phones instead: an iPhone in WebKit, an Android phone and the
// narrowest screen in Chromium, each the size of its whole screen with its own insets.
import { defineConfig, devices } from '@playwright/test';

// A device as a home-screen app: no browser bar, so the page has the whole screen.
const standalone = (name, browserName) => {
  const d = devices[name];
  return { ...d, browserName, viewport: d.screen ?? d.viewport };
};

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
    baseURL: 'http://127.0.0.1:8080',
    browserName: 'chromium',
    viewport: { width: 390, height: 844 },
    locale: 'en-US',
    colorScheme: 'light',
  },
  projects: [
    { name: 'chromium', testIgnore: 'pwa.spec.mjs' },
    {
      // Notch above, home indicator below.
      name: 'iphone',
      testMatch: 'pwa.spec.mjs',
      use: standalone('iPhone 15', 'webkit'),
      metadata: { insets: { top: 59, bottom: 34, left: 0, right: 0 } },
    },
    {
      // Status bar above, three-button navigation below.
      name: 'android',
      testMatch: 'pwa.spec.mjs',
      use: standalone('Pixel 7', 'chromium'),
      metadata: { insets: { top: 24, bottom: 48, left: 0, right: 0 } },
    },
    {
      // 320 px wide, the narrowest the layout holds; a status bar and no home indicator.
      name: 'narrow',
      testMatch: 'pwa.spec.mjs',
      use: standalone('iPhone SE', 'chromium'),
      metadata: { insets: { top: 20, bottom: 0, left: 0, right: 0 } },
    },
  ],
});
