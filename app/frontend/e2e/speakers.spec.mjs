// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Switching the page to another speaker in the household, and its volume slider.
import { demoState, expect, openHome, test } from './demo.mjs';

test('switches to another speaker and shows what it plays', async ({ page }) => {
  await openHome(page);
  await expect(page.getByText('Radio Paradise').first()).toBeVisible();

  // The speakers sheet: a tap on another speaker makes it the one this page controls.
  await page.getByRole('button', { name: 'Speaker: Woonkamer' }).click();
  await page.getByRole('radio', { name: /Keuken/u }).click();
  await page.getByRole('button', { name: 'Volume Keuken' }).click();
  await expect(page.getByRole('slider', { name: 'Volume' }).first()).toHaveValue('18');
  await page.keyboard.press('Escape');

  await expect(page.getByRole('button', { name: 'Speaker: Keuken' })).toBeVisible();
  await expect(page.getByText('Jazz24').first()).toBeVisible();
  await expect(page.getByText('Radio Paradise')).toHaveCount(0);

  // The pick holds across a reload of the page.
  await page.reload();
  await expect(page.getByRole('button', { name: 'Speaker: Keuken' })).toBeVisible();
});

test('a volume slider sets the speaker’s volume', async ({ page, request }) => {
  await openHome(page);
  await page.getByRole('button', { name: 'Volume Woonkamer' }).click();
  const slider = page.getByRole('slider', { name: 'Volume' }).first();
  await expect(slider).toHaveValue('28');

  // By keyboard, a step of 2 at a time.
  await slider.focus();
  await page.keyboard.press('ArrowRight');
  await page.keyboard.press('ArrowRight');
  await page.keyboard.press('ArrowRight');
  await expect(slider).toHaveValue('34');
  await expect.poll(async () => (await demoState(request)).volumes.woonkamer).toBe(34);

  // By pointer: a tap halfway along the track.
  const track = page
    .locator('.vol', { has: page.getByRole('slider', { name: 'Volume', exact: true }) })
    .filter({ visible: true })
    .first()
    .locator('.vtrack');
  const box = await track.boundingBox();
  await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
  await expect
    .poll(async () => (await demoState(request)).volumes.woonkamer)
    .toBeGreaterThanOrEqual(44);
  const set = (await demoState(request)).volumes.woonkamer;
  expect(set).toBeLessThanOrEqual(56);
  await expect(slider).toHaveValue(String(set));

  // Another room's volume is its own.
  expect((await demoState(request)).volumes.keuken).toBe(18);
});
