// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';
import { keepRooms, lastRooms, useRooms, type Room } from './useRooms';
import { PREF } from '../lib/storage';
import * as api from '../lib/api';
import * as events from '../lib/events';

vi.mock('../lib/events', () => ({ subscribeHousehold: vi.fn(() => vi.fn()) }));
import { multiroomClient, nowPlaying } from './testFixtures';
import { must } from '../test/must';

vi.mock('../lib/api', () => ({
  getDevices: vi
    .fn()
    .mockResolvedValue([{ id: 'a', name: 'A', ip: '', model: 'ST10', online: true, self: true }]),
  getMultiroom: vi.fn().mockResolvedValue(null),
  getNowPlaying: vi.fn().mockResolvedValue(null),
  getSettings: vi.fn(),
  getUserSources: vi.fn().mockResolvedValue([]),
  getVolume: vi.fn(),
}));

describe('useRooms state polls', () => {
  it('lets a late answer from an older poll not overwrite a newer one', async () => {
    let releaseOld: (v: number) => void = vi.fn();
    vi.mocked(api.getVolume)
      .mockImplementationOnce(
        async () =>
          new Promise<number>((r) => {
            releaseOld = r;
          })
      )
      .mockResolvedValue(22);

    const { result } = renderHook(() => useRooms(null));
    await waitFor(() => expect(api.getVolume).toHaveBeenCalledTimes(1));

    await act(async () => {
      await result.current.reconnect('a'); // a second, newer poll
    });
    await waitFor(() => expect(result.current.rooms[0]?.volume).toBe(22));

    await act(async () => {
      releaseOld(11);
    });
    expect(result.current.rooms[0]?.volume).toBe(22);
  });

  it('keeps the last known volume when a read fails', async () => {
    vi.mocked(api.getVolume).mockReset().mockResolvedValueOnce(35).mockResolvedValue(null);
    const { result } = renderHook(() => useRooms(null));
    await waitFor(() => expect(result.current.rooms[0]?.volume).toBe(35));
    await act(async () => {
      await result.current.reconnect('a');
    });
    expect(result.current.rooms[0]?.volume).toBe(35);
  });

  it('keeps what a room plays when a read fails', async () => {
    const jazz = nowPlaying({ stationName: 'Jazz' });
    vi.mocked(api.getVolume).mockReset().mockResolvedValue(40);
    vi.mocked(api.getNowPlaying).mockReset().mockResolvedValueOnce(jazz).mockResolvedValue(null);
    const { result } = renderHook(() => useRooms(null));
    await waitFor(() => expect(result.current.rooms[0]?.now).toBe(jazz));
    await act(async () => {
      await result.current.reconnect('a');
    });
    expect(result.current.rooms[0]?.now).toBe(jazz);
  });

  it('keeps the other rooms when the device list times out', async () => {
    const two = [
      { id: 'a', name: 'A', ip: '', model: 'ST10', online: true, self: true },
      { id: 'b', name: 'B', ip: '10.0.0.2', model: 'ST20', online: true, self: false },
    ];
    vi.mocked(api.getDevices)
      .mockReset()
      .mockResolvedValueOnce(two)
      .mockRejectedValue(new Error('timeout'));
    const { result } = renderHook(() => useRooms(null));
    await waitFor(() => expect(result.current.rooms).toHaveLength(2));
    await act(async () => {
      await result.current.reconnect('a');
    });
    expect(result.current.rooms.map((r) => r.id)).toEqual(['a', 'b']);
    expect(api.getSettings).not.toHaveBeenCalled();
  });

  it('takes room state from the household stream and stops polling while it is open', async () => {
    vi.mocked(api.getDevices)
      .mockReset()
      .mockResolvedValue([
        { id: 'a', name: 'A', ip: '', model: 'ST10', online: true, self: true },
        { id: 'b', name: 'B', ip: '10.0.0.2', model: 'ST20', online: true, self: false },
      ]);
    vi.mocked(api.getVolume).mockReset().mockResolvedValue(5);
    const { result } = renderHook(() => useRooms(null));
    await waitFor(() => expect(result.current.rooms).toHaveLength(2));
    const h = must(
      vi.mocked(events.subscribeHousehold).mock.calls.at(-1),
      'a household subscription'
    )[0];
    act(() => {
      h.onOpen?.();
      h.onState({ self: true }, { volume: 17 });
      h.onState({ id: 'b', self: false }, { volume: 9 });
    });
    expect(result.current.rooms.map((r) => r.volume)).toEqual([17, 9]);
    vi.mocked(api.getVolume).mockClear();
    await act(async () => {
      await result.current.loadState();
    });
    expect(api.getVolume).not.toHaveBeenCalled();
  });

  it('asks nobody for groups or the device list while the household stream reports them', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      vi.mocked(api.getDevices)
        .mockReset()
        .mockResolvedValue([
          { id: 'a', name: 'A', ip: '', model: 'ST10', online: true, self: true },
          { id: 'b', name: 'B', ip: '10.0.0.2', model: 'ST20', online: true, self: false },
        ]);
      vi.mocked(api.getMultiroom).mockReset().mockResolvedValue([]);
      const { result } = renderHook(() => useRooms(null));
      await waitFor(() => expect(result.current.rooms).toHaveLength(2));
      const h = must(
        vi.mocked(events.subscribeHousehold).mock.calls.at(-1),
        'a household subscription'
      )[0];
      await act(async () => {
        h.onOpen?.();
        h.onState({ self: true }, { followers: ['b'], devices: 'r1' });
        h.onState({ id: 'b', self: false }, { followers: [] });
      });
      await waitFor(() => expect(result.current.leaders).toEqual({ b: 'a' }));

      vi.mocked(api.getDevices).mockClear();
      vi.mocked(api.getMultiroom).mockClear();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(30000);
      });
      expect(api.getMultiroom).not.toHaveBeenCalled();
      expect(api.getDevices).not.toHaveBeenCalled();

      // A changed device list is read once; a group change needs no read at all.
      await act(async () => {
        h.onState({ self: true }, { devices: 'r2' });
        h.onState({ self: true }, { followers: [] });
      });
      expect(api.getDevices).toHaveBeenCalledTimes(1);
      await waitFor(() => expect(result.current.leaders).toEqual({}));
      expect(api.getMultiroom).not.toHaveBeenCalled();

      // The stream drops: both polls take over again.
      act(() => h.onError?.());
      await act(async () => {
        await vi.advanceTimersByTimeAsync(10000);
      });
      expect(api.getMultiroom).toHaveBeenCalled();
      expect(api.getDevices).toHaveBeenCalledTimes(2);
    } finally {
      vi.useRealTimers();
    }
  });

  it('retries the device list when the read for a new revision fails', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const a = { id: 'a', name: 'A', ip: '', model: 'ST10', online: true, self: true };
      const b = { id: 'b', name: 'B', ip: '10.0.0.2', model: 'ST20', online: true, self: false };
      vi.mocked(api.getDevices).mockReset().mockResolvedValueOnce([a]).mockResolvedValue([a, b]);
      const { result } = renderHook(() => useRooms(null));
      await waitFor(() => expect(result.current.rooms).toHaveLength(1));
      const h = must(
        vi.mocked(events.subscribeHousehold).mock.calls.at(-1),
        'a household subscription'
      )[0];
      act(() => h.onOpen?.());
      vi.mocked(api.getDevices).mockRejectedValueOnce(new Error('down'));
      await act(async () => {
        h.onState({ self: true }, { devices: 'new-revision' });
      });
      await act(async () => {
        await vi.advanceTimersByTimeAsync(60000);
      });
      expect(result.current.rooms.map((r) => r.id)).toContain('b');
    } finally {
      vi.useRealTimers();
    }
  });

  it('keeps polling a room the household stream says nothing about', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      vi.mocked(api.getDevices)
        .mockReset()
        .mockResolvedValue([
          { id: 'a', name: 'A', ip: '', model: 'ST10', online: true, self: true },
          { id: 'b', name: 'B', ip: '10.0.0.2', model: 'ST20', online: true, self: false },
        ]);
      vi.mocked(api.getVolume).mockReset().mockResolvedValue(10);
      const { result } = renderHook(() => useRooms('a'));
      await waitFor(() => expect(result.current.rooms.map((r) => r.volume)).toEqual([0, 10]));
      const h = must(
        vi.mocked(events.subscribeHousehold).mock.calls.at(-1),
        'a household subscription'
      )[0];
      act(() => {
        h.onOpen?.();
        h.onState({ self: true }, { volume: 10 });
      });
      vi.mocked(api.getVolume).mockResolvedValue(55);
      await act(async () => {
        await vi.advanceTimersByTimeAsync(60000);
      });
      expect(result.current.rooms.find((r) => r.id === 'b')?.volume).toBe(55);
    } finally {
      vi.useRealTimers();
    }
  });

  it('closes the household stream while the page is hidden and reopens it on return', async () => {
    const close = vi.fn();
    vi.mocked(events.subscribeHousehold).mockClear().mockReturnValue(close);
    const { result } = renderHook(() => useRooms(null));
    await waitFor(() => expect(result.current.loaded).toBe(true));
    const setVisibility = (v: 'hidden' | 'visible') => {
      Object.defineProperty(document, 'visibilityState', { value: v, configurable: true });
      act(() => {
        document.dispatchEvent(new Event('visibilitychange'));
      });
    };
    try {
      setVisibility('hidden');
      expect(close).toHaveBeenCalled();
      const opened = vi.mocked(events.subscribeHousehold).mock.calls.length;
      setVisibility('visible');
      expect(vi.mocked(events.subscribeHousehold).mock.calls.length).toBe(opened + 1);
    } finally {
      Object.defineProperty(document, 'visibilityState', { value: 'visible', configurable: true });
    }
  });

  it('keeps a confirmed group when the leader cannot be read', async () => {
    vi.mocked(api.getDevices)
      .mockReset()
      .mockResolvedValue([
        { id: 'a', name: 'A', ip: '', model: 'ST10', online: true, self: true },
        { id: 'b', name: 'B', ip: '10.0.0.2', model: 'ST20', online: true, self: false },
      ]);
    vi.mocked(api.getMultiroom)
      .mockReset()
      .mockImplementation(async (speaker) =>
        speaker === '' ? [multiroomClient({ deviceId: 'b', connected: true })] : []
      );
    const { result } = renderHook(() => useRooms(null));
    await waitFor(() => expect(result.current.leaders).toEqual({ b: 'a' }));
    vi.mocked(api.getMultiroom).mockImplementation(async (speaker) => (speaker === '' ? null : []));
    await act(async () => {
      await result.current.refresh();
    });
    expect(result.current.leaders).toEqual({ b: 'a' });
    vi.mocked(api.getMultiroom).mockResolvedValue([]);
    await act(async () => {
      await result.current.refresh();
    });
    expect(result.current.leaders).toEqual({});
  });

  it('keeps a group change shown while the follower is still connecting', async () => {
    vi.mocked(api.getDevices)
      .mockReset()
      .mockResolvedValue([
        { id: 'a', name: 'A', ip: '', model: 'ST10', online: true, self: true },
        { id: 'b', name: 'B', ip: '10.0.0.2', model: 'ST20', online: true, self: false },
      ]);
    vi.mocked(api.getMultiroom).mockReset().mockResolvedValue([]);
    const { result } = renderHook(() => useRooms(null));
    await waitFor(() => expect(result.current.loaded).toBe(true));
    act(() => result.current.regroup({ b: 'a' }));
    const asked = vi.mocked(api.getMultiroom).mock.calls.length;
    // The first check reads the snapserver before b's snapclient is in.
    await waitFor(
      () => expect(vi.mocked(api.getMultiroom).mock.calls.length).toBeGreaterThan(asked),
      {
        timeout: 3000,
      }
    );
    await act(async () => Promise.resolve());
    expect(result.current.leaders).toEqual({ b: 'a' });
  });

  it('keeps a group change after a read confirmed it and a later one has the old group', async () => {
    vi.mocked(api.getDevices)
      .mockReset()
      .mockResolvedValue([
        { id: 'a', name: 'A', ip: '', model: 'ST10', online: true, self: true },
        { id: 'b', name: 'B', ip: '10.0.0.2', model: 'ST20', online: true, self: false },
      ]);
    const member = [multiroomClient({ deviceId: 'b', connected: true })];
    vi.mocked(api.getMultiroom).mockReset().mockResolvedValue([]);
    const { result } = renderHook(() => useRooms(null));
    await waitFor(() => expect(result.current.loaded).toBe(true));
    vi.mocked(api.getMultiroom).mockResolvedValue(member);
    act(() => result.current.regroup({ b: 'a' }));
    const asked = vi.mocked(api.getMultiroom).mock.calls.length;
    await waitFor(
      () => expect(vi.mocked(api.getMultiroom).mock.calls.length).toBeGreaterThan(asked),
      { timeout: 3000 }
    );
    await act(async () => Promise.resolve());
    vi.mocked(api.getMultiroom).mockResolvedValue([]);
    const confirmed = vi.mocked(api.getMultiroom).mock.calls.length;
    await waitFor(
      () => expect(vi.mocked(api.getMultiroom).mock.calls.length).toBeGreaterThan(confirmed),
      { timeout: 4000 }
    );
    await act(async () => Promise.resolve());
    expect(result.current.leaders).toEqual({ b: 'a' });
  }, 9000);
});

describe('useRooms last seen household', () => {
  const seen: Room = {
    id: 'b',
    name: 'Keuken',
    model: 'ST20',
    ip: '10.0.0.2',
    online: true,
    self: false,
    now: nowPlaying({ stationName: 'Jazz' }),
    volume: 12,
    sources: ['AUX'],
    linkedTo: 'a',
    connecting: true,
  };

  it('shows the rooms last seen before discovery answers', () => {
    keepRooms([seen]);
    vi.mocked(api.getDevices).mockReturnValueOnce(new Promise(vi.fn()));
    const { result } = renderHook(() => useRooms(null));
    expect(result.current.loaded).toBe(false);
    expect(result.current.rooms).toEqual([{ ...seen, connecting: undefined }]);
  });

  it('starts empty from nothing stored or something unreadable', () => {
    expect(lastRooms()).toEqual([]);
    localStorage.setItem(PREF.rooms, '{not json');
    expect(lastRooms()).toEqual([]);
  });
});
