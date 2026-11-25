// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { RecentList } from './RecentList';
import type { RecentItem } from '../../../lib/recent';

const item: RecentItem = {
  key: 'tunein:s1',
  name: 'Qmusic',
  art: '',
  tuneInId: 's1',
  spotifyUri: null,
  source: 'TUNEIN',
  stoppedAt: '2026-10-04T09:00:00Z',
  locations: ['s1'],
};

const open = async (onHold?: (item: RecentItem) => void) => {
  const p = { play: vi.fn(), remove: vi.fn() };
  render(
    <RecentList
      recent={[item]}
      speaker=""
      onPlayRecent={p.play}
      onRemoveRecent={p.remove}
      onHoldRecent={onHold}
    />
  );
  await userEvent.click(screen.getByRole('button', { name: 'More for Qmusic' }));
  return p;
};

// No I18nProvider here, so the labels are English.
describe('RecentActions', () => {
  it('opens a sheet for the entry that removes it from the list', async () => {
    const p = await open();
    expect(screen.getByRole('dialog', { name: 'Qmusic' })).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Remove from Recently played' }));
    expect(p.remove).toHaveBeenCalledWith(item);
  });

  it('plays the entry from the sheet', async () => {
    const p = await open();
    await userEvent.click(screen.getByRole('button', { name: 'Play' }));
    expect(p.play).toHaveBeenCalledWith(item);
  });

  it('offers a preset only when there is room to store it on', async () => {
    const hold = vi.fn();
    await open(hold);
    await userEvent.click(screen.getByRole('button', { name: 'Put on a preset' }));
    expect(hold).toHaveBeenCalledWith(item);
  });

  it('leaves the preset out without a room', async () => {
    await open();
    expect(screen.queryByRole('button', { name: 'Put on a preset' })).not.toBeInTheDocument();
  });
});
