// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, expect, it, vi } from 'vitest';
import { renderHook } from '@testing-library/react';
import { useRoomVolume } from './useRoomVolume';
import type { SelectedRoom } from './useSelectedRoom';

vi.mock('../lib/api', () => ({ setVolume: vi.fn(() => Promise.resolve()) }));

const selection = (volume: number) =>
  ({
    lead: { id: 'a' },
    members: [],
    live: { changeVolume: vi.fn() },
    roomsState: { patch: vi.fn() },
    rooms: [{ id: 'a', volume }],
  }) as unknown as SelectedRoom;

describe('useRoomVolume', () => {
  it('counts a volume change from elsewhere, so a late unmute level is dropped', () => {
    const { result, rerender } = renderHook(({ s }) => useRoomVolume(s, false), {
      initialProps: { s: selection(0) },
    });
    const stamp = result.current.volumeStamp('a');
    rerender({ s: selection(0) });
    expect(result.current.volumeStamp('a')).toBe(stamp);
    rerender({ s: selection(50) });
    expect(result.current.volumeStamp('a')).not.toBe(stamp);
  });

  const group = (ids: string[], volume: number, changeVolume = vi.fn(), patch = vi.fn()) =>
    ({
      lead: { id: ids[0], name: ids[0], volume },
      members: ids.slice(1).map((id) => ({ id, name: id, volume })),
      live: { changeVolume },
      roomsState: { patch },
      rooms: ids.map((id) => ({ id, name: id, volume })),
    }) as unknown as SelectedRoom;

  for (const sync of [true, false]) {
    it(`starts a new drag for another group within the drag time (syncVolume ${sync})`, () => {
      const changeVolume = vi.fn();
      const patch = vi.fn();
      const { result, rerender } = renderHook(({ s }) => useRoomVolume(s, sync), {
        initialProps: { s: group(['a1', 'a2'], 20, changeVolume, patch) },
      });
      result.current.setGroupVolume(21);
      rerender({ s: group(['b1', 'b2'], 70, changeVolume, patch) });
      changeVolume.mockClear();
      patch.mockClear();
      result.current.setGroupVolume(71);
      if (sync) expect(changeVolume).toHaveBeenCalledWith(71, { group: true });
      else expect(changeVolume).toHaveBeenCalledWith(71);
      expect(patch).toHaveBeenCalledWith('b2', { volume: 71 });
    });
  }
});
