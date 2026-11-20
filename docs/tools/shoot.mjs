// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Screenshots the demo UI (demo-server.mjs on :8080) at phone size. Run inside the
// Playwright image, see in-container.sh. Writes PNGs to the directory in argv[2].
import { chromium } from 'playwright';

const out = process.argv[2] ?? '.';
const browser = await chromium.launch();

const tap = (p, name) =>
  p.getByRole('button', { name }).or(p.getByRole('menuitem', { name })).or(p.getByRole('link', { name })).first().click();
const menu = async (p, name) => {
  await tap(p, 'Menu');
  await p.waitForTimeout(500);
  await tap(p, name);
};

async function shoot(name, scheme, steps) {
  const ctx = await browser.newContext({
    viewport: { width: 390, height: 844 },
    deviceScaleFactor: 3,
    colorScheme: scheme,
    locale: 'en-US',
  });
  const page = await ctx.newPage();
  await page.addInitScript((s) => localStorage.setItem('soundtap-theme', s), scheme);
  await page.goto('http://localhost:8080/');
  await page.getByRole('button', { name: 'Menu' }).waitFor();
  await page.waitForTimeout(1200);
  await steps(page);
  await page.mouse.move(0, 0); // no hover highlight on the row that was tapped
  await page.waitForTimeout(1200);
  await page.screenshot({ path: `${out}/${name}-${scheme}.png` });
  await ctx.close();
}

for (const scheme of ['light', 'dark']) {
  await shoot('home', scheme, async () => {});
  await shoot('presets', scheme, (p) => tap(p, 'Presets'));
  await shoot('speaker', scheme, (p) => tap(p, /^Speaker: /));
  await shoot('now', scheme, (p) => p.getByText('I Am Stretched On Your Grave').first().click());
  await shoot('spotify', scheme, (p) => menu(p, /Spotify/));
  await shoot('playlist', scheme, async (p) => {
    await menu(p, /Spotify/);
    await p.waitForTimeout(800);
    await p.getByText('Sunday Morning').first().click();
  });
  await shoot('tunein', scheme, (p) => menu(p, /TuneIn/));
  await shoot('settings', scheme, (p) => menu(p, /Settings/));
}
await browser.close();
