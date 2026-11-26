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

describe('RecentList', () => {
  it('lists recent entries with their service, and plays the tapped one', async () => {
    const play = vi.fn();
    render(<RecentList recent={[item]} speaker="" onPlayRecent={play} onRemoveRecent={vi.fn()} />);
    expect(screen.getByText(/· TuneIn$/u)).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: /^Qmusic/u }));
    expect(play).toHaveBeenCalledWith(item);
  });

  it('says what the page is for while nothing was played', () => {
    render(<RecentList recent={[]} speaker="" onPlayRecent={vi.fn()} onRemoveRecent={vi.fn()} />);
    expect(screen.getByText(/Speel iets af|Play something/u)).toBeInTheDocument();
  });
});
