// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { act, renderHook, waitFor } from '@testing-library/react';
import { useSpotifyLibrary } from './useSpotifyLibrary';
import * as spotify from '../lib/spotify';
import { DEFAULT_LANG, makeT } from '../lib/i18n';
import { spotifyPlaylist, spotifyTrack } from './testFixtures';

vi.mock('../lib/spotify', async (orig) => ({
  ...(await orig<typeof import('../lib/spotify')>()),
  spotifyStatus: vi.fn().mockResolvedValue({ available: true, connected: true }),
  spotifyMyPlaylists: vi.fn(),
  spotifyLiked: vi.fn(),
}));

const pl = spotifyPlaylist;

describe('useSpotifyLibrary rootlist', () => {
  it('loads every page of playlists, not only the first', async () => {
    vi.mocked(spotify.spotifyMyPlaylists).mockImplementation(async (offset = 0) =>
      offset === 0 ? { items: [pl('a')], total: 2, next: 50 } : { items: [pl('b')], total: 2 }
    );
    const { result } = renderHook(() => useSpotifyLibrary(true, undefined, vi.fn()));
    await waitFor(() => expect(result.current.playlists.map((p) => p.name)).toEqual(['a', 'b']));
    expect(spotify.spotifyMyPlaylists).toHaveBeenCalledTimes(2);
    expect(spotify.spotifyMyPlaylists).toHaveBeenLastCalledWith(50);
  });

  it('reads the rootlist again after its first page failed', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    vi.mocked(spotify.spotifyMyPlaylists)
      .mockReset()
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValue({ items: [pl('a')], total: 1 });
    const { result } = renderHook(() => useSpotifyLibrary(true, undefined, vi.fn()));
    await waitFor(() => expect(spotify.spotifyMyPlaylists).toHaveBeenCalledTimes(1));
    expect(result.current.playlists).toEqual([]);
    await act(async () => vi.advanceTimersByTimeAsync(15000));
    await waitFor(() => expect(result.current.playlists.map((p) => p.name)).toEqual(['a']));
    vi.useRealTimers();
  });

  it('resumes at a later page that failed, keeping the earlier ones', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    vi.mocked(spotify.spotifyMyPlaylists)
      .mockReset()
      .mockResolvedValueOnce({ items: [pl('a')], total: 2, next: 50 })
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValue({ items: [pl('b')], total: 2 });
    const { result } = renderHook(() => useSpotifyLibrary(true, undefined, vi.fn()));
    await waitFor(() => expect(spotify.spotifyMyPlaylists).toHaveBeenCalledTimes(2));
    expect(result.current.playlists.map((p) => p.name)).toEqual(['a']);
    await act(async () => vi.advanceTimersByTimeAsync(15000));
    await waitFor(() => expect(result.current.playlists.map((p) => p.name)).toEqual(['a', 'b']));
    expect(spotify.spotifyMyPlaylists).toHaveBeenLastCalledWith(50);
    vi.useRealTimers();
  });
});

describe('useSpotifyLibrary failed page', () => {
  it('retries the page that failed', async () => {
    vi.mocked(spotify.spotifyMyPlaylists).mockResolvedValue({ items: [], total: 0 });
    vi.mocked(spotify.spotifyLiked)
      .mockRejectedValueOnce(new Error('metadata request failed'))
      .mockResolvedValueOnce({ items: [], total: 0 });
    const { result } = renderHook(() => useSpotifyLibrary(true, undefined, vi.fn()));
    await waitFor(() => expect(result.current.view).toBe('ready'));
    act(() => result.current.open({ kind: 'liked', title: 'Liked' }));
    await waitFor(() => expect(result.current.error).not.toBe(''));
    // Nothing was played, so it must not read as a play failure.
    expect(result.current.error).toBe(makeT(DEFAULT_LANG)('spotifyLoadFailed'));
    expect(result.current.page).toBeNull();
    act(() => result.current.retry());
    await waitFor(() => expect(result.current.page?.title).toBe('Liked'));
    expect(result.current.error).toBe('');
  });
});

const track = spotifyTrack;

describe('useSpotifyLibrary page cache', () => {
  it('keeps tracks appended by Load More on a revisit', async () => {
    vi.mocked(spotify.spotifyMyPlaylists).mockResolvedValue({ items: [], total: 0 });
    vi.mocked(spotify.spotifyLiked).mockReset();
    vi.mocked(spotify.spotifyLiked).mockImplementation(async (offset = 0) =>
      offset === 0 ? { items: [track('a')], total: 2, next: 50 } : { items: [track('b')], total: 2 }
    );
    const { result } = renderHook(() => useSpotifyLibrary(true, undefined, vi.fn()));
    await waitFor(() => expect(result.current.view).toBe('ready'));
    act(() => result.current.open({ kind: 'liked', title: 'Liked' }));
    await waitFor(() => expect(result.current.page?.tracks).toHaveLength(1));
    await act(async () => result.current.loadMore());
    act(() => result.current.goUp());
    act(() => result.current.open({ kind: 'liked', title: 'Liked' }));
    expect(result.current.page?.tracks.map((t) => t.name)).toEqual(['a', 'b']);
    expect(result.current.page?.next).toBeUndefined();
  });

  it('leaves the next page able to load more while the last one is still loading', async () => {
    vi.mocked(spotify.spotifyMyPlaylists).mockResolvedValue({ items: [], total: 0 });
    vi.mocked(spotify.spotifyLiked).mockReset();
    let answer: () => void = vi.fn();
    vi.mocked(spotify.spotifyLiked).mockImplementation(async (offset = 0) =>
      offset === 0
        ? Promise.resolve({ items: [track('a')], total: 2, next: 50 })
        : new Promise((resolve) => {
            answer = () => resolve({ items: [track('b')], total: 2 });
          })
    );
    const { result } = renderHook(() => useSpotifyLibrary(true, undefined, vi.fn()));
    await waitFor(() => expect(result.current.view).toBe('ready'));
    act(() => result.current.open({ kind: 'liked', title: 'Liked' }));
    await waitFor(() => expect(result.current.page?.tracks).toHaveLength(1));
    let more: Promise<void> = Promise.resolve();
    act(() => {
      more = result.current.loadMore();
    });
    expect(result.current.loadingMore).toBe(true);
    act(() => result.current.goUp());
    act(() => result.current.open({ kind: 'liked', title: 'Liked' }));
    expect(result.current.loadingMore).toBe(false);
    await act(async () => {
      answer();
      await more;
    });
    expect(result.current.loadingMore).toBe(false);
  });

  it('fetches again after the library was closed and reopened', async () => {
    vi.mocked(spotify.spotifyMyPlaylists).mockResolvedValue({ items: [], total: 0 });
    vi.mocked(spotify.spotifyLiked).mockReset();
    vi.mocked(spotify.spotifyLiked).mockResolvedValueOnce({ items: [track('old')], total: 1 });
    vi.mocked(spotify.spotifyLiked).mockResolvedValueOnce({ items: [track('new')], total: 1 });
    const { result, rerender } = renderHook(
      ({ opened }) => useSpotifyLibrary(true, undefined, vi.fn(), opened),
      { initialProps: { opened: true } }
    );
    await waitFor(() => expect(result.current.view).toBe('ready'));
    act(() => result.current.open({ kind: 'liked', title: 'Liked' }));
    await waitFor(() => expect(result.current.page?.tracks[0]?.name).toBe('old'));
    rerender({ opened: false });
    rerender({ opened: true });
    act(() => result.current.open({ kind: 'liked', title: 'Liked' }));
    await waitFor(() => expect(result.current.page?.tracks[0]?.name).toBe('new'));
    expect(spotify.spotifyLiked).toHaveBeenCalledTimes(2);
  });

  it('does not cache a page answer that arrives after the library closed', async () => {
    vi.mocked(spotify.spotifyMyPlaylists).mockResolvedValue({ items: [], total: 0 });
    let finish: (v: spotify.SpotifyPage<spotify.SpotifyTrack>) => void = vi.fn();
    vi.mocked(spotify.spotifyLiked).mockReset();
    vi.mocked(spotify.spotifyLiked)
      .mockImplementationOnce(
        async () =>
          new Promise((r) => {
            finish = r;
          })
      )
      .mockResolvedValue({ items: [track('new')], total: 1 });
    const { result, rerender } = renderHook(
      ({ opened }) => useSpotifyLibrary(true, undefined, vi.fn(), opened),
      { initialProps: { opened: true } }
    );
    await waitFor(() => expect(result.current.view).toBe('ready'));
    act(() => result.current.open({ kind: 'liked', title: 'Liked' }));
    rerender({ opened: false });
    await act(async () => finish({ items: [track('old')], total: 1 }));
    rerender({ opened: true });
    act(() => result.current.open({ kind: 'liked', title: 'Liked' }));
    await waitFor(() => expect(result.current.page?.tracks[0]?.name).toBe('new'));
    expect(spotify.spotifyLiked).toHaveBeenCalledTimes(2);
  });
});
