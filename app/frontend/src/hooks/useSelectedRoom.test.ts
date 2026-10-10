// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { renderHook } from '@testing-library/react';
import { useSelectedRoom } from './useSelectedRoom';
import { apiURL } from '../lib/base';
import type { Room } from './useRooms';

let rooms: Partial<Room>[] = [];
vi.mock('./useRooms', async (orig) => ({
  ...(await orig<typeof import('./useRooms')>()),
  useRooms: () => ({ rooms, loaded: true, patch: () => {} }),
}));
vi.mock('./useSpeaker', () => ({
  useSpeaker: () => ({ nowPlaying: null, player: { status: 'idle' }, volume: 0 }),
}));

describe('useSelectedRoom', () => {
  it('routes the page to the leader when the room is grouped elsewhere, and back', () => {
    const a = { id: 'A', self: true, online: true, linkedTo: undefined as string | undefined };
    const b = { id: 'B', self: false, online: true };
    rooms = [a, b];
    const { result, rerender } = renderHook(() => useSelectedRoom());
    expect(apiURL('/api/spotify/play')).toBe('/api/spotify/play');

    rooms = [{ ...a, linkedTo: 'B' }, b];
    rerender();
    expect(result.current.speaker).toBe('B');
    expect(apiURL('/api/spotify/play')).toBe('/api/devices/B/api/spotify/play');

    rooms = [a, b];
    rerender();
    expect(apiURL('/api/spotify/play')).toBe('/api/spotify/play');
  });
});
