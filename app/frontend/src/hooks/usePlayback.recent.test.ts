// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { usePlayback } from './usePlayback';
import * as spotify from '../lib/spotify';
import { recentItem, room, selectedRoom } from './testFixtures';

vi.mock('../lib/api', () => ({}));
vi.mock('../lib/spotify', () => ({ spotifyPlay: vi.fn() }));

const item = recentItem({
  name: 'Mix',
  art: 'a.jpg',
  tuneInId: null,
  spotifyUri: 'spotify:playlist:1',
  source: 'SPOTIFY',
});

describe('usePlayback Spotify recent item', () => {
  beforeEach(() => vi.clearAllMocks());

  it('shows it starting at once and ignores a repeat until the request settles', async () => {
    let release: () => void = vi.fn();
    vi.mocked(spotify.spotifyPlay).mockImplementationOnce(
      async () => new Promise<{ status: string }>((r) => (release = () => r({ status: 'ok' })))
    );
    const sel = selectedRoom({ speaker: 'kitchen', lead: room({ name: 'Kitchen' }) });
    const { live } = sel;
    const { result } = renderHook(() => usePlayback(sel, () => vi.fn()));

    act(() => void result.current.playRecent(item));
    act(() => void result.current.playRecent(item));
    expect(spotify.spotifyPlay).toHaveBeenCalledTimes(1);
    expect(live.playOptimistic).toHaveBeenCalledWith({ name: 'Mix', logo: 'a.jpg' });

    await act(async () => release());
    expect(live.refreshNowPlaying).toHaveBeenCalledTimes(1);
  });
});
