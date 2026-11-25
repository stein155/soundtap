// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// First run on the speaker's own setup access point (?setup=wifi), in two steps: name the
// speaker, then pick the home network from its survey and join it.
import { demoState, expect, test } from './demo.mjs';

test('names the speaker and joins it to the home Wi-Fi', async ({ page, request }) => {
  await page.goto('/?setup=wifi');
  await expect(page.getByRole('heading', { name: 'Welcome to SoundTap' })).toBeVisible();
  // Setup is the whole page: none of the radio's controls.
  await expect(page.getByRole('button', { name: 'Menu' })).toHaveCount(0);

  const name = page.getByRole('textbox', { name: 'Name' });
  await expect(name).toHaveValue('Woonkamer');
  await name.fill('Study');
  await page.getByRole('button', { name: 'Next', exact: true }).click();

  // The survey's strongest network comes first, and it needs a password.
  await expect(page.getByRole('radio', { name: /HomeNet/ })).toBeChecked();
  const connect = page.getByRole('button', { name: 'Connect', exact: true });
  await expect(connect).toBeDisabled();
  await page.getByLabel('Password for HomeNet').fill('short');
  await expect(connect).toBeDisabled();
  await page.getByLabel('Password for HomeNet').fill('correct horse battery');
  await connect.click();

  await expect(page.getByText('Network saved. The speaker is switching over.')).toBeVisible();
  await expect(page.getByText(/open http:\/\/woonkamer\.local/)).toBeVisible();
  const state = await demoState(request);
  expect(state.names.woonkamer).toBe('Study');
  expect(state.joined).toEqual({ ssid: 'HomeNet', security: 'wpa_or_wpa2' });
});

test('joins an open network without a password', async ({ page, request }) => {
  await page.goto('/?setup=wifi');
  await page.getByRole('button', { name: 'Next', exact: true }).click();
  await page.getByRole('radio', { name: 'Café Guest' }).click();
  await expect(page.getByLabel(/^Password for/)).toHaveCount(0);
  await page.getByRole('button', { name: 'Connect', exact: true }).click();
  await expect(page.getByText('Network saved. The speaker is switching over.')).toBeVisible();
  const state = await demoState(request);
  expect(state.joined).toEqual({ ssid: 'Café Guest', security: 'none' });
  // An unchanged name is not sent again.
  expect(state.names.woonkamer).toBe('Woonkamer');
});
