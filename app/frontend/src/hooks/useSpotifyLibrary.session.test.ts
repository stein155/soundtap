// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';
import { useSpotifyLibrary } from './useSpotifyLibrary';
import { setTargetDevice } from '../lib/base';
import * as spotify from '../lib/spotify';

vi.mock('../lib/api', () => ({ storeSpotifyPreset: vi.fn() }));
vi.mock('../lib/spotify', async (orig) => ({
  ...(await orig<typeof import('../lib/spotify')>()),
  spotifyStatus: vi.fn(),
  spotifyMyPlaylists: vi.fn().mockResolvedValue({ items: [], total: 0 }),
  spotifyPlay: vi.fn().mockResolvedValue({ status: 'ok' }),
  spotifyDisconnect: vi.fn().mockResolvedValue({ status: 'ok' }),
}));

const item = { uri: 'spotify:playlist:x', name: 'X' };

describe('useSpotifyLibrary session', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setTargetDevice('');
  });

  // Stores on the speaker that was picked when the store began, not one picked meanwhile;
  // storing no longer also plays.
  it('reads the session again after a disconnect instead of loading forever', async () => {
    vi.mocked(spotify.spotifyStatus)
      .mockResolvedValueOnce({ available: true, connected: true } as spotify.SpotifyStatus)
      .mockResolvedValue({ available: true } as spotify.SpotifyStatus);
    const { result } = renderHook(() => useSpotifyLibrary(true, undefined, () => {}));
    await waitFor(() => expect(result.current.view).toBe('ready'));

    await act(() => result.current.disconnect());
    await waitFor(() => expect(result.current.statusLoaded).toBe(true));
    expect(result.current.view).toBe('pair');
  });

  it('polls the session again after a play finds it lost, until it is back', async () => {
    const S = (s: object) => s as spotify.SpotifyStatus;
    vi.mocked(spotify.spotifyStatus)
      .mockResolvedValueOnce(S({ available: true, connected: true }))
      .mockResolvedValueOnce(S({ available: true, authenticated: true }))
      .mockResolvedValue(S({ available: true, authenticated: true, connected: true }));
    vi.mocked(spotify.spotifyPlay).mockRejectedValueOnce(
      new spotify.SpotifyError('spotify_not_connected')
    );
    const { result } = renderHook(() => useSpotifyLibrary(true, undefined, () => {}));
    await waitFor(() => expect(result.current.view).toBe('ready'));
    await act(() => result.current.play(item.uri, undefined, item));
    await waitFor(() => expect(result.current.view).toBe('connecting'));
    await waitFor(() => expect(result.current.view).toBe('ready'), { timeout: 6000 });
  }, 10000);

  it('ignores a play that finishes after the sheet was closed and reopened', async () => {
    let release: () => void = () => {};
    vi.mocked(spotify.spotifyPlay).mockImplementationOnce(
      () => new Promise((r) => (release = () => r({ status: 'ok' })))
    );
    const onPlayed = vi.fn();
    const { result, rerender } = renderHook(({ o }) => useSpotifyLibrary(o, undefined, onPlayed), {
      initialProps: { o: true },
    });
    let done: Promise<void> = Promise.resolve();
    act(() => {
      done = result.current.play(item.uri, undefined, item);
    });
    rerender({ o: false });
    rerender({ o: true });
    await act(async () => {
      release();
      await done;
    });
    expect(onPlayed).not.toHaveBeenCalled();
  });
});
