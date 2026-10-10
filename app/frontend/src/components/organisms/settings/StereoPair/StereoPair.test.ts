// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, expect, it } from 'vitest';
import { pairCandidates, pairPartner } from '.';
import type { Room } from '../../../../hooks/useRooms';

const room = (id: string, linkedTo: string | null = null): Room => ({
  id,
  name: id,
  model: 'st10',
  ip: '',
  online: true,
  self: false,
  now: null,
  volume: 0,
  sources: [],
  linkedTo,
});

describe('pairPartner', () => {
  it('finds the partner whichever speaker leads', () => {
    const rooms = [room('kitchen'), room('hall', 'kitchen'), room('attic')];
    expect(pairPartner(rooms[0], rooms)?.id).toBe('hall');
    expect(pairPartner(rooms[1], rooms)?.id).toBe('kitchen');
    expect(pairPartner(rooms[2], rooms)).toBeNull();
  });
});

describe('pairCandidates', () => {
  it('offers only online speakers that are free or already follow this one', () => {
    const offline = { ...room('cellar'), online: false };
    const rooms = [
      room('kitchen'),
      room('hall', 'kitchen'),
      room('attic'),
      room('study', 'attic'),
      room('garage'),
      offline,
    ];
    const ids = (r: Room) => pairCandidates(r, rooms).map((c) => c.id);
    expect(ids(rooms[0])).toEqual(['hall', 'garage']);
    expect(ids(rooms[4])).toEqual([]);
  });
});
