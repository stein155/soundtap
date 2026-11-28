// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Now playing fits one phone screen: the artwork takes the height the controls leave, the
// track's progress is a line on it with the times under it, shuffle and repeat sit on the
// outside of the transport, and nothing scrolls. The demo speaker plays radio; for a
// Spotify track the page's answers about what plays are put in here.
/* global document, getComputedStyle, window */
import { expect, openHome, test } from './demo.mjs';

const SPOTIFY = {
  source: 'SPOTIFY',
  track: 'Sun Goes Down',
  artist: 'Robin Schulz',
  album: 'Prayer',
  playStatus: 'PLAY_STATE',
  art: '/api/demo/cover/Sun.svg',
};

// Every speaker plays a Spotify track, one minute into three and a half.
async function playSpotify(page) {
  const json = (body) => ({ contentType: 'application/json', body: JSON.stringify(body) });
  await page.route(/\/api\/now(?:\?|$)/u, (r) => r.fulfill(json(SPOTIFY)));
  await page.route(/\/api\/position(?:\?|$)/u, (r) =>
    r.fulfill(json({ positionMs: 60000, durationMs: 214000, seekable: true }))
  );
  await page.route(/\/api\/spotify\/player(?:\?|$)/u, (r) =>
    r.fulfill(json({ shuffle: false, repeat: 'context' }))
  );
  await page.route(/\/api\/spotify\/liked(?:\?|$)/u, (r) =>
    r.fulfill(json({ available: true, liked: false }))
  );
  await page.route(/\/api\/events(?:\?|$)/u, (r) =>
    r.fulfill({
      contentType: 'text/event-stream',
      body: `event: state\ndata: ${JSON.stringify({ now: SPOTIFY, volume: 28, presets: 'demo' })}\n\n`,
    })
  );
}

async function openPlayer(page) {
  await openHome(page);
  await page
    .getByRole('button', { name: /^Open player/u })
    .filter({ visible: true })
    .first()
    .click();
  const sheet = page.getByRole('dialog');
  await expect(sheet).toBeVisible();
  // The sheet slides in: measure once it stands still.
  await page.waitForFunction(() =>
    document
      .getAnimations()
      .every((a) => a.playState !== 'running' || a.effect?.getTiming().iterations === Infinity)
  );
  return sheet;
}

// Nothing in now playing scrolls, and every control is on the screen.
async function fitsOneScreen(page, sheet) {
  const body = sheet.locator('.np-body');
  expect(
    await body.evaluate((el) => ({
      over: el.scrollHeight - el.clientHeight,
      y: getComputedStyle(el).overflowY,
    }))
  ).toEqual({ over: 0, y: 'visible' });
  const height = await page.evaluate(() => window.innerHeight);
  const boxes = await Promise.all(
    [
      sheet.getByRole('slider', { name: 'Volume' }),
      sheet.getByRole('button', { name: 'Group' }),
      sheet.locator('.spkbar'),
    ].map((control) => control.boundingBox())
  );
  for (const box of boxes) expect(box.y + box.height).toBeLessThanOrEqual(height);
}

for (const [w, h, big] of [
  [390, 760, true],
  [375, 667, false],
]) {
  test(`a Spotify track fits ${w}×${h} without scrolling`, async ({ page }) => {
    await page.setViewportSize({ width: w, height: h });
    await playSpotify(page);
    const sheet = await openPlayer(page);

    // Progress: a line on the artwork, with the time played and left under it.
    const bar = sheet.getByRole('progressbar', { name: 'Position in track' });
    await expect(bar).toHaveAttribute('aria-valuemax', '214000');
    await expect(sheet.locator('.np-times')).toContainText('−');
    const frame = await sheet.locator('.np-frame').boundingBox();
    const line = await bar.boundingBox();
    expect(line.height).toBeLessThanOrEqual(4);
    expect(line.y + line.height).toBeCloseTo(frame.y + frame.height, 0);

    // The artwork: square, at most 310 px, smaller on a small phone.
    expect(frame.width).toBeCloseTo(frame.height, 0);
    expect(frame.width).toBeLessThanOrEqual(310);
    if (big) expect(frame.width).toBeGreaterThan(280);
    else expect(frame.width).toBeGreaterThan(120);

    // Shuffle and repeat on the outside of the transport; repeat is on, shuffle off.
    const row = sheet.locator('.np-ctl').getByRole('button');
    await expect(row).toHaveCount(5);
    const names = await row.evaluateAll((els) => els.map((el) => el.getAttribute('aria-label')));
    expect(names[0]).toBe('Shuffle');
    expect(names[1]).toBe('Previous track');
    expect(names[3]).toBe('Next track');
    expect(names[4]).toBe('Repeat');
    await expect(row.nth(0)).toHaveAttribute('aria-pressed', 'false');
    await expect(row.nth(4)).toHaveAttribute('aria-pressed', 'true');

    // Only one slider: the volume.
    await expect(sheet.getByRole('slider')).toHaveCount(1);
    await fitsOneScreen(page, sheet);
  });
}

test('radio keeps its own buttons, without shuffle, repeat or a progress line', async ({
  page,
}) => {
  await page.setViewportSize({ width: 375, height: 667 });
  const sheet = await openPlayer(page);
  await expect(sheet.getByText('Radio Paradise').first()).toBeVisible();
  await expect(sheet.getByRole('button', { name: 'Shuffle' })).toHaveCount(0);
  await expect(sheet.getByRole('button', { name: 'Repeat' })).toHaveCount(0);
  await expect(sheet.getByRole('progressbar')).toHaveCount(0);
  await fitsOneScreen(page, sheet);
});
