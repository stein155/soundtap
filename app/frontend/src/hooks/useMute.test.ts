// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useMute } from './useMute';
import { getMutedLevel, setMuted } from '../lib/api/playback';
import type { Room } from './useRooms';

vi.mock('../lib/api/playback', () => ({
  getMutedLevel: vi.fn(async () => null),
  setMuted: vi.fn(async () => ({})),
}));

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

const mute = (stamp: () => number = () => 0, failed = vi.fn(() => vi.fn())) => {
  const showRoomVolume = vi.fn();
  const { result } = renderHook(() => useMute({ showRoomVolume, volumeStamp: stamp }, failed));
  return { toggle: result.current, showRoomVolume, failed };
};

describe('useMute', () => {
  beforeEach(() => {
    vi.mocked(getMutedLevel).mockReset().mockResolvedValue(null);
    vi.mocked(setMuted).mockReset().mockResolvedValue({});
  });

  // The speaker's own mute, never volume 0: the speaker keeps the level, so an unmute
  // after a reload returns to it rather than to a default.
  it('mutes through the speaker and shows the room silent', async () => {
    const { toggle, showRoomVolume } = mute();
    await act(async () => toggle(room('a', 80)));
    expect(setMuted).toHaveBeenCalledWith(true, 'a');
    expect(showRoomVolume).toHaveBeenLastCalledWith(expect.objectContaining({ id: 'a' }), 0);
  });

  it('unmutes to the level the speaker kept', async () => {
    vi.mocked(getMutedLevel).mockResolvedValueOnce(6);
    const { toggle, showRoomVolume } = mute();
    await act(async () => toggle(room('bar', 0)));
    expect(setMuted).toHaveBeenCalledWith(false, 'bar');
    expect(showRoomVolume).toHaveBeenLastCalledWith(expect.objectContaining({ id: 'bar' }), 6);
  });

  // A room set to 0 by its slider is not muted: there is no level to make up.
  it('leaves a room at 0 that is not muted as it is', async () => {
    const { toggle, showRoomVolume } = mute();
    await act(async () => toggle(room('b', 0)));
    expect(setMuted).not.toHaveBeenCalled();
    expect(showRoomVolume).not.toHaveBeenCalled();
  });

  it('puts the level back and reports a mute the speaker refused', async () => {
    vi.mocked(setMuted).mockRejectedValueOnce(new Error('offline'));
    const report = vi.fn();
    const failed = vi.fn(() => report);
    const { toggle, showRoomVolume } = mute(() => 0, failed);
    await act(async () => toggle(room('a', 40)));
    expect(showRoomVolume).toHaveBeenLastCalledWith(expect.objectContaining({ id: 'a' }), 40);
    expect(failed).toHaveBeenCalledWith('a');
    expect(report).toHaveBeenCalled();
  });

  it('drops a late unmute level after a newer slider move', async () => {
    let resolve: (v: number) => void = vi.fn();
    vi.mocked(getMutedLevel).mockReturnValueOnce(
      new Promise((r) => {
        resolve = r;
      })
    );
    let stamp = 0;
    const { toggle, showRoomVolume } = mute(() => stamp);
    let pending: Promise<void> | undefined;
    act(() => {
      pending = toggle(room('a', 0));
    });
    stamp += 1; // the slider moved to 50 while the level was being read
    await act(async () => {
      resolve(20);
      await pending;
    });
    expect(showRoomVolume).not.toHaveBeenCalled();
    expect(setMuted).not.toHaveBeenCalled();
  });
});
