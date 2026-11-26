// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// The speaker serving the page goes away (a reboot, an update, the phone off its Wi-Fi) and
// comes back: the page says so, then follows the speaker's live stream again by itself.
import { expect, openHome, test } from './demo.mjs';

const isStream = (r) => new URL(r.url()).pathname === '/api/events';

test('says when the speaker is gone and follows it live again when it is back', async ({
  page,
  request,
}) => {
  await openHome(page);
  const banner = page.getByText('Can’t reach Woonkamer. Reconnecting…');
  await expect(banner).toHaveCount(0);

  await request.post('/api/demo/away');
  await expect(banner).toBeVisible({ timeout: 20000 });

  // The stream the page opens again once the speaker answers.
  const reopened = page.waitForResponse((r) => isStream(r) && r.status() === 200, {
    timeout: 20000,
  });
  await request.post('/api/demo/back');
  await reopened;
  await expect(banner).toBeHidden({ timeout: 20000 });

  // Live again: a change made elsewhere (another phone) is pushed at once. With the stream
  // up the page polls only every 30 s, so only the push can bring it within seconds.
  await page.getByRole('button', { name: 'Volume Woonkamer' }).click();
  const slider = page.getByRole('slider', { name: 'Volume' }).first();
  await expect(slider).toHaveValue('28');
  await request.post('/api/volume', { data: { volume: 60 } });
  await expect(slider).toHaveValue('60', { timeout: 3000 });
});
