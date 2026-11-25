// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, expect, it, vi } from 'vitest';
import { renderHook } from '@testing-library/react';
import { useRoomVolume } from './useRoomVolume';
import type { SelectedRoom } from './useSelectedRoom';
import { room, selectedRoom } from './testFixtures';
import { must } from '../test/must';

vi.mock('../lib/api', () => ({ setVolume: vi.fn(async () => Promise.resolve()) }));

const selection = (volume: number): SelectedRoom => {
  const lead = room({ id: 'a', volume });
  return selectedRoom({ lead, rooms: [lead] });
};

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

  const group = (
    ids: string[],
    volume: number,
    changeVolume = vi.fn(),
    patch = vi.fn()
  ): SelectedRoom => {
    const rooms = ids.map((id) => room({ id, name: id, volume }));
    return selectedRoom({
      lead: must(rooms[0], 'a leader'),
      members: rooms.slice(1),
      live: { changeVolume },
      roomsState: { patch },
      rooms,
    });
  };

  for (const sync of [true, false]) {
    it(`starts a new drag for another group within the drag time (syncVolume ${String(sync)})`, () => {
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
