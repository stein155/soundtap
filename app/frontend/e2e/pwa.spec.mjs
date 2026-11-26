// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// The home-screen app on a phone: every main screen and sheet keeps its controls, headings
// and text out of the status bar or notch above and the home indicator or navigation bar
// below, the docked speaker bar pads only max(0, inset - 16px) under its content, a sheet's
// last row clears the bottom inset once scrolled to its end, and nothing reaches past the
// sides. The insets are put into the CSS (safe-area.mjs); the projects in
// playwright.config.mjs give each phone its own. An emulator approves no hardware.
/* global document */
import { expect, openHome, test } from './demo.mjs';
import {
  fakeInsets,
  inBand,
  installAudit,
  leftoverInsets,
  pastSides,
  speakerBars,
} from './safe-area.mjs';

const button = (page, name) => page.getByRole('button', { name }).filter({ visible: true }).first();

async function settings(page) {
  await openHome(page);
  await button(page, 'Menu').click();
  await button(page, 'Settings').click();
}

async function speakerPage(page) {
  await settings(page);
  await button(page, /^Woonkamer /).click();
  await expect(page.getByRole('heading', { name: 'Connection and maintenance' })).toBeVisible();
}

async function wifiSearch(page) {
  await speakerPage(page);
  await button(page, /^Wi-Fi/).click();
  await button(page, 'Find a network').click();
  await expect(page.getByText('HomeNet').first()).toBeVisible();
}

async function nowPlaying(page) {
  await openHome(page);
  await button(page, /^Open player/).click();
}

// A long press on a recently played station opens what can be done with it.
async function recentActions(page) {
  await openHome(page);
  const box = await button(page, 'NPO Radio 2 TuneIn').boundingBox();
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  await page.waitForTimeout(800);
  await page.mouse.up();
  await expect(page.getByRole('dialog')).toBeVisible();
}

async function setup(page) {
  await page.goto('/?setup=wifi');
  await expect(page.getByRole('heading', { name: 'Welcome to SoundTap' })).toBeVisible();
}

const SCREENS = {
  home: async (page) => {
    await openHome(page);
    await expect(button(page, 'NPO Radio 2 TuneIn')).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Music services' })).toBeVisible();
  },
  menu: async (page) => {
    await openHome(page);
    await button(page, 'Menu').click();
  },
  'now playing': nowPlaying,
  'group sheet': async (page) => {
    await nowPlaying(page);
    await button(page, 'Group').click();
  },
  'speakers sheet': async (page) => {
    await openHome(page);
    await button(page, 'Speaker: Woonkamer').click();
  },
  presets: async (page) => {
    await openHome(page);
    await button(page, 'Presets').click();
  },
  search: async (page) => {
    await openHome(page);
    await page.getByRole('option', { name: /^TuneIn/ }).click();
    await button(page, 'Search TuneIn').click();
    await page.keyboard.type('jazz');
    await expect(page.getByText('Jazz24').first()).toBeVisible();
  },
  'recently played': async (page) => {
    await openHome(page);
    await button(page, 'Recently played').click();
  },
  'recent actions sheet': recentActions,
  settings,
  'speaker page': speakerPage,
  'Wi-Fi search': wifiSearch,
  'Wi-Fi password sheet': async (page) => {
    await wifiSearch(page);
    await page.getByText('HomeNet').first().click();
    await expect(page.getByLabel(/Password/).first()).toBeVisible();
  },
  updates: async (page) => {
    await speakerPage(page);
    await button(page, /^Updates/).click();
  },
  'speaker gone': async (page, request) => {
    await openHome(page);
    await request.post('/api/demo/away');
    await expect(page.getByRole('heading', { name: 'The speaker does not answer' })).toBeVisible({
      timeout: 30000,
    });
  },
  setup,
  'setup, Wi-Fi': async (page) => {
    await setup(page);
    await button(page, 'Next').click();
    await expect(page.getByRole('radio', { name: /HomeNet/ })).toBeChecked();
  },
};

for (const [name, open] of Object.entries(SCREENS)) {
  test(`${name} keeps clear of the phone's insets`, async ({ page, request }, info) => {
    test.setTimeout(60000);
    const ins = info.project.metadata.insets;
    await fakeInsets(page, ins);
    await installAudit(page);
    await open(page, request);
    // Sheets slide in: once one has started, every animation that ends has ended (a
    // spinner never does).
    await page.waitForTimeout(400);
    await page.waitForFunction(() =>
      document
        .getAnimations()
        .every((a) => a.playState !== 'running' || a.effect?.getTiming().iterations === Infinity)
    );
    // A slow runner can still be settling a layer that slides in: give it a moment.
    await expect.poll(() => leftoverInsets(page)).toEqual([]);
    await expect.poll(() => pastSides(page)).toEqual([]);
    await expect.poll(() => inBand(page, 'top')).toEqual([]);
    for (const bar of await speakerBars(page))
      expect(bar).toEqual({ pad: Math.max(0, ins.bottom - 16), gap: 0, content: 64 });
    await expect.poll(() => inBand(page, 'bottom')).toEqual([]);
  });
}
