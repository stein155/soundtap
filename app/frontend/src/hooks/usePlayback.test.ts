// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { usePlayback } from './usePlayback';
import type { SelectedRoom } from './useSelectedRoom';
import * as api from '../lib/api';

vi.mock('../lib/api', () => ({ playPreset: vi.fn() }));

const jazz = { tuneInId: 's1', name: 'Jazz' };

function room(speaker: string): SelectedRoom {
  return {
    speaker,
    lead: { name: speaker },
    live: {
      presets: [jazz],
      player: { status: 'standby' },
      playOptimistic: vi.fn(),
      nudge: vi.fn(),
      refreshNowPlaying: vi.fn(),
      cancelPending: vi.fn(),
    },
  } as unknown as SelectedRoom;
}

describe('usePlayback duplicate-tap guard', () => {
  beforeEach(() => vi.clearAllMocks());

  it('drops a repeat tap on the same speaker but not the same preset on another speaker', async () => {
    let release: () => void = () => {};
    vi.mocked(api.playPreset)
      .mockImplementationOnce(() => new Promise<void>((r) => (release = r)))
      .mockResolvedValue(undefined);

    const { result, rerender } = renderHook(({ sel }) => usePlayback(sel, () => () => {}), {
      initialProps: { sel: room('kitchen') },
    });

    act(() => void result.current.playSlot(1));
    act(() => void result.current.playSlot(1)); // double tap while pending: ignored
    expect(api.playPreset).toHaveBeenCalledTimes(1);

    rerender({ sel: room('bedroom') });
    await act(() => result.current.playSlot(1)); // same name, other speaker: sent
    expect(api.playPreset).toHaveBeenCalledTimes(2);
    expect(vi.mocked(api.playPreset).mock.calls[1][3]).toBe('bedroom');

    await act(async () => release());
  });
});
