// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Screenshots the demo UI (demo-server.mjs on :8080) at phone size, light and dark. Run inside
// the Playwright image, see in-container.sh. Writes <screen>-<scheme>.png to the directory in
// argv[2], for compose.mjs.
import { chromium } from "playwright";

const out = process.argv[2] ?? ".";
const browser = await chromium.launch();

const tap = (p, name) =>
  p
    .getByRole("button", { name })
    .or(p.getByRole("menuitem", { name }))
    .or(p.getByRole("link", { name }))
    .first()
    .click();
const menu = async (p, name) => {
  await tap(p, "Menu");
  await p.waitForTimeout(500);
  // In the menu itself: a card on the page under it may carry the same name.
  await tap(p.getByRole("dialog").last(), name);
};
const settle = (p) => p.waitForTimeout(800);

async function shoot(name, scheme, steps) {
  const ctx = await browser.newContext({
    viewport: { width: 390, height: 844 },
    deviceScaleFactor: 3,
    colorScheme: scheme,
    locale: "en-US",
  });
  const page = await ctx.newPage();
  // Every shot starts from the speakers as the demo server starts: no group left by the last.
  await page.request.post("http://127.0.0.1:8080/api/demo/reset");
  await page.goto("http://127.0.0.1:8080/");
  await page.getByRole("button", { name: "Menu" }).waitFor();
  await page.waitForTimeout(1200);
  await steps(page);
  await page.mouse.move(0, 0); // no hover highlight on the row that was tapped
  await page.waitForTimeout(1200);
  await page.screenshot({ path: `${out}/${name}-${scheme}.png` });
  await ctx.close();
}

const nowPlaying = (p) =>
  p.getByText("Up Till Dawn (On The Move)").first().click();

for (const scheme of ["light", "dark"]) {
  await shoot("home", scheme, async () => {});
  await shoot("now", scheme, nowPlaying);
  // The group sheet from now playing, with the kitchen and the TV playing along.
  await shoot("group", scheme, async (p) => {
    await nowPlaying(p);
    await settle(p);
    await tap(p, "Group");
    await settle(p);
    for (const room of ["Keuken", "TV"]) {
      await p.getByRole("switch", { name: room }).click();
      await settle(p);
    }
  });
  await shoot("integrations", scheme, async (p) => {
    await menu(p, /Settings/);
    await settle(p);
    await tap(p, /Integrations/);
  });
  // The SoundTouch 300's Sound page: tone, audio mode, the TV and its extra speakers.
  await shoot("sound", scheme, async (p) => {
    await menu(p, /Settings/);
    await settle(p);
    await tap(p, /^TV/);
    await settle(p);
    await tap(p, /^Sound/);
  });
}
await browser.close();
