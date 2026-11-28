// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// A speaker updating itself does not hold the whole app: the update screen can be left, the
// other speakers controlled meanwhile, and the screen found again while the update goes on.
import { demoState, expect, openHome, test } from './demo.mjs';

// Woonkamer's own settings, from the speakers sheet, its Updates page and the update it
// offers, started after the one question.
async function updateWoonkamer(page, request) {
  await page.getByRole('button', { name: 'Speaker: Woonkamer' }).click();
  await page
    .getByRole('dialog', { name: 'Speakers' })
    .getByRole('button', { name: 'Settings' })
    .click();
  await page.getByRole('button', { name: /^Updates/u }).click();
  const card = page.getByRole('region', { name: 'SoundTap app 0.2.0' });
  await card.getByRole('button', { name: 'Update' }).click();
  await page.getByRole('dialog', { name: 'Update' }).getByRole('button', { name: 'Start' }).click();
  await expect.poll(async () => (await demoState(request)).updating).toEqual({ woonkamer: true });
}

// Another speaker: its row says nothing of an update, Woonkamer's says how far it is; picked,
// it is controlled as ever.
async function turnUpKeuken(page, request) {
  await page.getByRole('button', { name: 'Speaker: Woonkamer' }).click();
  const speakers = page.getByRole('dialog', { name: 'Speakers' });
  await expect(speakers.getByRole('radio', { name: /Woonkamer/u })).toContainText(
    'Updating… 42% done'
  );
  await expect(speakers.getByRole('radio', { name: /Keuken/u })).not.toContainText('Updating');
  await speakers.getByRole('radio', { name: /Keuken/u }).click();
  await expect(page.getByRole('button', { name: 'Speaker: Keuken' })).toBeVisible();

  await page.getByRole('button', { name: 'Volume Keuken' }).click();
  const slider = page.getByRole('slider', { name: 'Volume' }).first();
  await expect(slider).toHaveValue('18');
  await slider.focus();
  await page.keyboard.press('ArrowRight');
  await expect.poll(async () => (await demoState(request)).volumes.keuken).toBe(20);
  await page.keyboard.press('Escape');
}

test('leaves the update screen, controls another speaker and comes back to it', async ({
  page,
  request,
}) => {
  await request.post('/api/demo/offer-update');
  await openHome(page);
  await updateWoonkamer(page, request);

  // The update screen, with how far the speaker says it is and the warning.
  const screen = page.getByRole('alertdialog', { name: 'The speaker is updating itself' });
  await expect(screen).toBeVisible();
  await expect(screen.getByText('42% done')).toBeVisible({ timeout: 20000 });
  await expect(screen.getByText('Keep it plugged in.')).toBeVisible();

  // Close: the app is there again, and Woonkamer's controls wait for it.
  await screen.getByRole('button', { name: 'Close' }).click();
  await expect(screen).toBeHidden();
  const notice = page.getByRole('button', { name: 'Update · Woonkamer · 42% done' });
  await expect(notice).toBeVisible();
  await expect(page.getByRole('button', { name: 'Volume Woonkamer' })).toBeDisabled();

  await turnUpKeuken(page, request);

  // The notice brings the update screen back, the update still under way.
  await notice.click();
  await expect(screen).toBeVisible();
  await expect(screen.getByText('Update · Woonkamer')).toBeVisible();
  await expect(screen.getByText('42% done')).toBeVisible();
});
