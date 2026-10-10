// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// What the end-to-end tests share: a demo speaker put back as it started before every test,
// and its remote control (/api/demo/*, see docs/tools/demo-server.mjs).
import { test as base, expect } from '@playwright/test';

export { expect };

export const test = base.extend({
  page: async ({ page, request }, use) => {
    await request.post('/api/demo/reset');
    await use(page);
  },
});

// What the page has sent the demo speaker: volumes, names, the Wi-Fi it joined.
export async function demoState(request) {
  return (await request.get('/api/demo/state')).json();
}

// The home page, once it has found the speaker serving it.
export async function openHome(page) {
  await page.goto('/');
  await expect(page.getByRole('button', { name: 'Speaker: Woonkamer' })).toBeVisible();
}
