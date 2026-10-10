// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { usePlayback } from './usePlayback';
import type { SelectedRoom } from './useSelectedRoom';
import * as spotify from '../lib/spotify';

vi.mock('../lib/api', () => ({}));
vi.mock('../lib/spotify', () => ({ spotifyPlay: vi.fn() }));

const item = { name: 'Mix', art: 'a.jpg', spotifyUri: 'spotify:playlist:1' } as never;

describe('usePlayback Spotify recent item', () => {
  beforeEach(() => vi.clearAllMocks());

  it('shows it starting at once and ignores a repeat until the request settles', async () => {
    let release: () => void = () => {};
    vi.mocked(spotify.spotifyPlay).mockImplementationOnce(
      () => new Promise<{ status: string }>((r) => (release = () => r({ status: 'ok' })))
    );
    const live = {
      player: { status: 'standby' },
      playOptimistic: vi.fn(),
      nudge: vi.fn(),
      refreshNowPlaying: vi.fn(),
      cancelPending: vi.fn(),
    };
    const sel = { speaker: 'kitchen', lead: { name: 'Kitchen' }, live } as unknown as SelectedRoom;
    const { result } = renderHook(() => usePlayback(sel, () => () => {}));

    act(() => void result.current.playRecent(item));
    act(() => void result.current.playRecent(item));
    expect(spotify.spotifyPlay).toHaveBeenCalledTimes(1);
    expect(live.playOptimistic).toHaveBeenCalledWith({ name: 'Mix', logo: 'a.jpg' });

    await act(async () => release());
    expect(live.refreshNowPlaying).toHaveBeenCalledTimes(1);
  });
});
