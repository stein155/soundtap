// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// index.html's launch screen: the mark and wordmark show before any script has run, in the
// system's colour scheme, and leave once the app has drawn its first frame.
/* global getComputedStyle, document */
import { expect, openHome, test } from './demo.mjs';

test('the launch screen shows before the scripts load', async ({ page }) => {
  await page.route('**/assets/*.js', (route) => route.abort());
  await page.goto('/');
  const launch = page.locator('#launch');
  await expect(launch).toBeVisible();
  expect(await launch.evaluate((el) => getComputedStyle(el).backgroundColor)).toBe(
    'rgb(249, 249, 249)'
  );
});

test('the launch screen follows a dark system', async ({ page }) => {
  await page.emulateMedia({ colorScheme: 'dark' });
  await page.route('**/assets/*.js', (route) => route.abort());
  await page.goto('/');
  expect(await page.locator('#launch').evaluate((el) => getComputedStyle(el).backgroundColor)).toBe(
    'rgb(18, 18, 18)'
  );
});

test('the launch screen leaves once the app is drawn', async ({ page }) => {
  await openHome(page);
  await expect(page.locator('#launch')).toHaveCount(0);
  expect(
    await page.evaluate(() => document.getElementById('root')?.childElementCount)
  ).toBeGreaterThan(0);
});
