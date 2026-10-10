// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { useGrouping } from './useGrouping';
import { useRoomVolume } from './useRoomVolume';
import * as api from '../lib/api';
import type { Room } from './useRooms';
import type { SelectedRoom } from './useSelectedRoom';

vi.mock('../lib/api', () => ({
  followSpeaker: vi.fn(),
  inviteSpeaker: vi.fn(),
  setVolume: vi.fn(),
}));
// The hooks are called as plain functions here; their refs need no React, and no effect runs.
vi.mock('react', async (orig) => ({
  ...(await orig<typeof import('react')>()),
  useRef: (v: unknown) => ({ current: v }),
  useEffect: () => {},
}));

const room = (id: string, volume: number, linkedTo: string | null = null): Room => ({
  id,
  name: id,
  model: 'ST10',
  ip: `10.0.0.${id.charCodeAt(0)}`,
  online: true,
  self: false,
  now: null,
  volume,
  sources: [],
  linkedTo,
});

function selected(station: { tuneInId?: string; name: string } | null) {
  const lead = room('a', 100);
  const next = room('b', 20, 'a');
  return {
    rooms: [lead, next],
    lead,
    members: [next],
    speaker: 'a',
    live: { player: { station }, changeVolume: vi.fn() },
    roomsState: { leaders: { b: 'a' }, regroup: vi.fn(), refresh: vi.fn(), patch: vi.fn() },
    select: vi.fn(),
  } as unknown as SelectedRoom;
}

beforeEach(() => {
  vi.mocked(api.followSpeaker).mockReset().mockResolvedValue(undefined);
  vi.mocked(api.inviteSpeaker).mockReset().mockResolvedValue(undefined);
});

describe('useGrouping.linkMany', () => {
  it('regroups every speaker in one step and invites each', async () => {
    const sel = selected(null);
    const c = room('c', 40);
    const d = room('d', 40);
    await useGrouping(
      sel,
      vi.fn(() => vi.fn())
    ).linkMany([c, d]);
    expect(sel.roomsState.regroup).toHaveBeenCalledTimes(1);
    expect(sel.roomsState.regroup).toHaveBeenCalledWith({ b: 'a', c: 'a', d: 'a' });
    expect(api.inviteSpeaker).toHaveBeenCalledWith(c.ip, 'a');
    expect(api.inviteSpeaker).toHaveBeenCalledWith(d.ip, 'a');
  });

  it('does not invite a room ungrouped while its followers were let go', async () => {
    const sel = selected(null);
    const c = room('c', 40);
    const e = room('e', 40, 'c');
    (sel as { rooms: Room[] }).rooms = [...sel.rooms, c, e];
    let released: () => void = () => {};
    vi.mocked(api.followSpeaker).mockReturnValueOnce(
      new Promise((resolve) => (released = () => resolve(undefined)))
    );
    const grouping = useGrouping(
      sel,
      vi.fn(() => vi.fn())
    );
    const linking = grouping.linkMany([c]);
    await grouping.unlinkMany([c]);
    released();
    await linking;
    expect(api.inviteSpeaker).not.toHaveBeenCalled();
  });

  it('invites a room only after its earlier unlink landed', async () => {
    const sel = selected(null);
    const c = room('c', 40, 'a');
    let released: () => void = () => {};
    vi.mocked(api.followSpeaker).mockReturnValueOnce(
      new Promise((resolve) => (released = () => resolve(undefined)))
    );
    const grouping = useGrouping(
      sel,
      vi.fn(() => vi.fn())
    );
    const unlinking = grouping.unlinkMany([c]);
    const linking = grouping.linkMany([c]);
    await Promise.resolve();
    expect(api.inviteSpeaker).not.toHaveBeenCalled();
    released();
    await Promise.all([unlinking, linking]);
    expect(api.inviteSpeaker).toHaveBeenCalledWith(c.ip, 'a');
  });

  it('lets the followers of a speaker go before it joins', async () => {
    const sel = selected(null);
    const c = room('c', 40);
    const e = room('e', 40, 'c');
    (sel as { rooms: Room[] }).rooms = [...sel.rooms, c, e];
    await useGrouping(
      sel,
      vi.fn(() => vi.fn())
    ).linkMany([c]);
    expect(api.followSpeaker).toHaveBeenCalledWith('', 'e');
    expect(sel.roomsState.regroup).toHaveBeenCalledWith({ b: 'a', c: 'a' });
  });

  it('does nothing without speakers', async () => {
    const sel = selected(null);
    await useGrouping(
      sel,
      vi.fn(() => vi.fn())
    ).linkMany([]);
    expect(sel.roomsState.regroup).not.toHaveBeenCalled();
  });
});

describe('useGrouping failures', () => {
  it('hands the speaker error to the reporter, for an invite and an unlink', async () => {
    const refused = Object.assign(new Error('conflict'), { status: 409 });
    vi.mocked(api.inviteSpeaker).mockRejectedValue(refused);
    vi.mocked(api.followSpeaker).mockRejectedValue(refused);
    const report = vi.fn();
    const grouping = useGrouping(
      selected(null),
      vi.fn(() => report)
    );
    await grouping.link(room('c', 40));
    await grouping.unlink(room('b', 20, 'a'));
    expect(report).toHaveBeenCalledTimes(2);
    expect(report).toHaveBeenNthCalledWith(1, refused);
    expect(report).toHaveBeenNthCalledWith(2, refused);
  });
});

describe('useRoomVolume group sync', () => {
  it('sends a saturated leader the full step, as the followers are shown', () => {
    const sel = selected(null);
    // Leader 100, follower 20: mean 60. The slider to 70 is a step of +10.
    useRoomVolume(sel, true).setGroupVolume(70);
    expect(sel.live.changeVolume).toHaveBeenCalledWith(110, { group: true });
    expect(sel.roomsState.patch).toHaveBeenCalledWith('b', { volume: 30 });
  });

  it('keeps the balance when a drag passes an end and comes back', async () => {
    const sel = selected(null);
    vi.mocked(api.setVolume).mockResolvedValue(undefined);
    const volume = useRoomVolume(sel, false);
    // Leader 100, follower 20: mean 60. Down to 0 clamps the follower, then back to 60.
    volume.setGroupVolume(0);
    sel.lead!.volume = 40;
    sel.members[0].volume = 0;
    volume.setGroupVolume(60);
    await Promise.resolve();
    await Promise.resolve();
    expect(sel.live.changeVolume).toHaveBeenLastCalledWith(100);
    expect(api.setVolume).toHaveBeenLastCalledWith(20, 'b');
  });
});
