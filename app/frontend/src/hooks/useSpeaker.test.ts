// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';
import { useSpeaker } from './useSpeaker';
import * as api from '../lib/api';

// Only the volume-coalescing path is exercised here; the rest of the hook is
// covered end-to-end by the app integration test.
vi.mock('../lib/api', () => ({
  getNowPlaying: vi.fn().mockResolvedValue({ standby: true }),
  getVolume: vi.fn().mockResolvedValue(20),
  getPresets: vi.fn().mockResolvedValue(Array(6).fill(null)),
  setVolume: vi.fn(),
  normalizeNowPlaying: (x: unknown) => x,
}));
vi.mock('../lib/events', () => ({ subscribeState: () => vi.fn() }));

describe('useSpeaker changeVolume', () => {
  beforeEach(() => vi.clearAllMocks());

  it('serialises writes and skips intermediate values while one is in flight', async () => {
    // First write hangs so 20 and 30 pile up behind it; only the latest survives.
    let release: () => void = vi.fn();
    vi.mocked(api.setVolume)
      .mockImplementationOnce(
        async () =>
          new Promise<void>((r) => {
            release = r;
          })
      )
      .mockResolvedValue(undefined);

    const { result } = renderHook(() => useSpeaker());

    act(() => {
      result.current.changeVolume(10);
    }); // sent immediately
    act(() => {
      result.current.changeVolume(20);
    }); // coalesced away
    act(() => {
      result.current.changeVolume(30);
    }); // latest — sent next

    expect(api.setVolume).toHaveBeenCalledTimes(1);
    expect(api.setVolume).toHaveBeenNthCalledWith(1, 10, '', { group: false });

    await act(async () => {
      release();
    });

    await waitFor(() => expect(api.setVolume).toHaveBeenCalledTimes(2));
    expect(api.setVolume).toHaveBeenNthCalledWith(2, 30, '', { group: false }); // 20 was dropped
  });
});

describe('useSpeaker optimistic playback', () => {
  beforeEach(() => vi.clearAllMocks());

  it('shows a picked station as "starting" the instant it is tapped', async () => {
    const { result } = renderHook(() => useSpeaker());
    await waitFor(() => expect(result.current.loading).toBe(false));

    act(() => {
      result.current.playOptimistic({ name: 'Jazz FM', tuneInId: 's1', logo: 'l' });
    });

    expect(result.current.player.status).toBe('starting');
    expect(result.current.player.station?.name).toBe('Jazz FM');
    expect(result.current.player.station?.tuneInId).toBe('s1');
  });

  it('collapses to idle immediately on stop', async () => {
    const { result } = renderHook(() => useSpeaker());
    await waitFor(() => expect(result.current.loading).toBe(false));

    act(() => {
      result.current.playOptimistic({ name: 'Jazz FM', tuneInId: 's1' });
    });
    expect(result.current.player.status).toBe('starting');

    act(() => {
      result.current.stopOptimistic();
    });
    expect(result.current.player.status).toBe('idle');
    expect(result.current.player.station).toBeNull();
  });

  it('drops the pending target when cancelled (failed play)', async () => {
    const { result } = renderHook(() => useSpeaker());
    await waitFor(() => expect(result.current.loading).toBe(false));

    act(() => {
      result.current.playOptimistic({ name: 'Jazz FM', tuneInId: 's1' });
    });
    act(() => {
      result.current.cancelPending();
    });
    expect(result.current.player.status).toBe('idle');
  });

  it('keeps a newer tap when an older play request fails', async () => {
    const { result } = renderHook(() => useSpeaker());
    await waitFor(() => expect(result.current.loading).toBe(false));

    let jazz: ReturnType<typeof result.current.playOptimistic> = null;
    act(() => {
      jazz = result.current.playOptimistic({ name: 'Jazz FM', tuneInId: 's1' });
    });
    act(() => {
      result.current.playOptimistic({ name: 'News', tuneInId: 's2' });
    });
    act(() => {
      result.current.cancelPending(jazz);
    });
    expect(result.current.player.status).toBe('starting');
    expect(result.current.player.station?.name).toBe('News');
  });

  it('gives up after the hard cap even when no state read succeeds', async () => {
    vi.mocked(api.getNowPlaying).mockResolvedValue(null);
    const { result } = renderHook(() => useSpeaker());
    await waitFor(() => expect(result.current.loading).toBe(false));

    vi.useFakeTimers();
    try {
      act(() => {
        result.current.playOptimistic({ name: 'Jazz FM', tuneInId: 's1' });
      });
      act(() => {
        vi.advanceTimersByTime(14000);
      });
      expect(result.current.player.status).toBe('starting');
      act(() => {
        vi.advanceTimersByTime(2000);
      });
      expect(result.current.player.status).toBe('idle');
    } finally {
      vi.useRealTimers();
    }
  });
});

describe('useSpeaker buffering feedback', () => {
  beforeEach(() => vi.clearAllMocks());

  // The whole point of the backend reporting BUFFERING_STATE: while a station is
  // being resolved and connected the user must see it, not "live" or nothing.
  it('reports a station the box is still buffering as buffering', async () => {
    vi.mocked(api.getNowPlaying).mockResolvedValue({
      standby: false,
      playStatus: 'BUFFERING_STATE',
      stationName: 'Jazz FM',
    });

    const { result } = renderHook(() => useSpeaker());
    await waitFor(() => expect(result.current.player.status).toBe('buffering'));
    expect(result.current.player.station?.name).toBe('Jazz FM');
  });

  it('reports a paused stream as paused, not connecting', async () => {
    vi.mocked(api.getNowPlaying).mockResolvedValue({
      standby: false,
      source: 'SPOTIFY',
      playStatus: 'PAUSE_STATE',
      stationName: 'Album',
    });

    const { result } = renderHook(() => useSpeaker());
    await waitFor(() => expect(result.current.player.status).toBe('paused'));
  });

  // A stream the box gave up on must collapse to idle rather than spin for the full
  // optimistic window, and say so; before 4 s STOP_STATE is only a retry.
  it('gives up on a stream the box still reports as stopped after 4 s', async () => {
    vi.mocked(api.getNowPlaying).mockResolvedValue({
      standby: false,
      playStatus: 'STOP_STATE',
      stationName: 'Jazz FM',
    });

    const { result } = renderHook(() => useSpeaker());
    await waitFor(() => expect(result.current.loading).toBe(false));
    act(() => {
      result.current.playOptimistic({ name: 'Jazz FM', tuneInId: 's1' });
    });
    await act(async () => {
      await result.current.refreshNowPlaying();
    });
    expect(result.current.player.status).toBe('starting');
    expect(result.current.gaveUp).toBeNull();

    const later = Date.now() + 4000;
    vi.spyOn(Date, 'now').mockReturnValue(later);
    await act(async () => {
      await result.current.refreshNowPlaying();
    });
    await waitFor(() => expect(result.current.player.status).toBe('idle'));
    expect(result.current.gaveUp?.name).toBe('Jazz FM');
    vi.restoreAllMocks();
  });

  // Stop used to be undone by the lagging PLAY_STATE of the station being stopped:
  // the first non-PLAY reading cleared the hold, leaving the late one unguarded.
  it('keeps the stopped station off screen while the box catches up', async () => {
    vi.mocked(api.getNowPlaying).mockResolvedValue({
      standby: false,
      playStatus: 'BUFFERING_STATE',
      stationName: 'Jazz FM',
    });

    const { result } = renderHook(() => useSpeaker());
    await waitFor(() => expect(result.current.player.status).toBe('buffering'));

    act(() => {
      result.current.stopOptimistic();
    });
    expect(result.current.player.status).toBe('idle');

    // A transient buffering reading, then the lagging PLAY_STATE of the old station.
    await act(async () => {
      await result.current.refreshNowPlaying();
    });
    vi.mocked(api.getNowPlaying).mockResolvedValue({
      standby: false,
      playStatus: 'PLAY_STATE',
      stationName: 'Jazz FM',
    });
    await act(async () => {
      await result.current.refreshNowPlaying();
    });

    expect(result.current.player.status).toBe('idle');
  });
});

describe('useSpeaker speaker switch', () => {
  beforeEach(() => vi.clearAllMocks());

  it('ignores a late volume failure from the room left behind', async () => {
    let fail: (_: Error) => void = vi.fn();
    vi.mocked(api.setVolume).mockImplementationOnce(
      async () =>
        new Promise((_, reject) => {
          fail = reject;
        })
    );
    const { result, rerender } = renderHook(({ id }) => useSpeaker(id), {
      initialProps: { id: 'kitchen' },
    });
    act(() => result.current.changeVolume(10));
    await waitFor(() => expect(api.setVolume).toHaveBeenCalled());
    rerender({ id: 'office' });
    await act(async () => fail(new Error('gone')));
    expect(result.current.volumeFailures).toBe(0);
  });

  it('sends volume to the speaker the hook currently points at', async () => {
    vi.mocked(api.setVolume).mockResolvedValue(undefined);
    const { result, rerender } = renderHook(({ id }) => useSpeaker(id), {
      initialProps: { id: '' },
    });
    act(() => result.current.changeVolume(10));
    await waitFor(() => expect(api.setVolume).toHaveBeenCalledWith(10, '', { group: false }));

    rerender({ id: 'kantoor' });
    act(() => result.current.changeVolume(40));
    await waitFor(() =>
      expect(api.setVolume).toHaveBeenLastCalledWith(40, 'kantoor', { group: false })
    );
  });
});
