// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { usePlayback } from './usePlayback';
import type { SelectedRoom } from './useSelectedRoom';
import * as api from '../lib/api';

vi.mock('../lib/api', () => ({ setPaused: vi.fn() }));

const now = { source: 'SPOTIFY', stationName: 'Mix', playStatus: 'PLAY_STATE' };

function selected(patch: () => void): SelectedRoom {
  return {
    speaker: 'kitchen',
    lead: { id: 'kitchen', name: 'Kitchen', now },
    live: { presets: [], player: { status: 'playing' }, refreshNowPlaying: vi.fn() },
    roomsState: { patch, loadState: vi.fn() },
  } as unknown as SelectedRoom;
}

describe('usePlayback Spotify pause', () => {
  beforeEach(() => vi.clearAllMocks());

  it('shows paused at once, ignores a second tap, and undoes it on failure', async () => {
    let fail: (e: Error) => void = () => {};
    vi.mocked(api.setPaused).mockImplementationOnce(() => new Promise((_, r) => (fail = r)));
    const patch = vi.fn();
    const { result } = renderHook(() => usePlayback(selected(patch), () => () => {}));

    act(() => void result.current.transport());
    act(() => void result.current.transport());
    expect(api.setPaused).toHaveBeenCalledTimes(1);
    expect(patch).toHaveBeenCalledWith('kitchen', {
      now: { ...now, playStatus: 'PAUSE_STATE' },
    });

    await act(async () => fail(new Error('down')));
    expect(patch).toHaveBeenLastCalledWith('kitchen', { now });
  });
});
