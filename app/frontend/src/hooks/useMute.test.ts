// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useMute } from './useMute';
import { getMutedLevel } from '../lib/api/playback';
import type { Room } from './useRooms';

vi.mock('../lib/api/playback', () => ({ getMutedLevel: vi.fn(async () => null) }));

const room = (id: string, volume: number): Room => ({
  id,
  name: id,
  model: 'ST10',
  ip: '',
  online: true,
  self: false,
  now: null,
  volume,
  sources: [],
  linkedTo: null,
});

describe('useMute', () => {
  it('does not unmute a room to another room’s level', async () => {
    const setVolume = vi.fn();
    const { result } = renderHook(() => useMute(setVolume));
    await act(async () => result.current(room('a', 80)));
    expect(setVolume).toHaveBeenLastCalledWith(expect.objectContaining({ id: 'a' }), 0);
    // Another room, muted from the start, has nothing to return to.
    await act(async () => result.current(room('b', 0)));
    expect(setVolume).toHaveBeenLastCalledWith(expect.objectContaining({ id: 'b' }), 25);
    // The first room returns to where it was.
    await act(async () => result.current(room('a', 0)));
    expect(setVolume).toHaveBeenLastCalledWith(expect.objectContaining({ id: 'a' }), 80);
  });

  it('unmutes a bar muted by its remote to the level it had', async () => {
    vi.mocked(getMutedLevel).mockResolvedValueOnce(6);
    const setVolume = vi.fn();
    const { result } = renderHook(() => useMute(setVolume));
    await act(async () => result.current(room('bar', 0)));
    expect(setVolume).toHaveBeenLastCalledWith(expect.objectContaining({ id: 'bar' }), 6);
  });

  it('drops a late unmute level after a newer slider move', async () => {
    let resolve: (v: number) => void = vi.fn();
    vi.mocked(getMutedLevel).mockReturnValueOnce(
      new Promise((r) => {
        resolve = r;
      })
    );
    let stamp = 0;
    const setVolume = vi.fn();
    const { result } = renderHook(() => useMute(setVolume, () => stamp));
    let pending: Promise<void> | undefined;
    act(() => {
      pending = result.current(room('a', 0));
    });
    stamp += 1; // the slider moved to 50 while the level was being read
    await act(async () => {
      resolve(20);
      await pending;
    });
    expect(setVolume).not.toHaveBeenCalled();
  });
});
