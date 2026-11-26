// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { usePlayback } from './usePlayback';
import type { SelectedRoom } from './useSelectedRoom';
import * as api from '../lib/api';
import { nowPlaying, room, selectedRoom } from './testFixtures';

vi.mock('../lib/api', () => ({ stopPlayback: vi.fn() }));

function selected(patch: () => void): SelectedRoom {
  return selectedRoom({
    speaker: 'kitchen',
    lead: room({ id: 'kitchen', name: 'Kitchen' }),
    roomsState: { patch },
  });
}

describe('usePlayback stop failure', () => {
  beforeEach(() => vi.clearAllMocks());

  it('puts a room back as it was when stopping it failed', async () => {
    vi.mocked(api.stopPlayback).mockRejectedValue(new Error('down'));
    const patch = vi.fn();
    const now = nowPlaying({ source: 'TUNEIN', stationName: 'Jazz', playStatus: 'PLAY_STATE' });
    const other = room({ id: 'office', name: 'Office', now });
    const { result } = renderHook(() => usePlayback(selected(patch), () => vi.fn()));
    await act(async () => {
      await result.current.roomTransport(other);
    });
    expect(patch).toHaveBeenLastCalledWith('office', { now });
  });

  it('ignores a repeat tap on a room card until the first has settled', async () => {
    let finish: () => void = vi.fn();
    vi.mocked(api.stopPlayback).mockImplementation(
      async () =>
        new Promise<void>((resolve) => {
          finish = resolve;
        })
    );
    const now = nowPlaying({ source: 'TUNEIN', stationName: 'Jazz', playStatus: 'PLAY_STATE' });
    const other = room({ id: 'office', name: 'Office', now });
    const { result } = renderHook(() => usePlayback(selected(vi.fn()), () => vi.fn()));
    let first: Promise<unknown> = Promise.resolve();
    await act(async () => {
      first = result.current.roomTransport(other);
      await result.current.roomTransport(other);
    });
    expect(api.stopPlayback).toHaveBeenCalledTimes(1);
    await act(async () => {
      finish();
      await first;
    });
    await act(async () => {
      void result.current.roomTransport(other);
      await Promise.resolve();
    });
    expect(api.stopPlayback).toHaveBeenCalledTimes(2);
  });
});
