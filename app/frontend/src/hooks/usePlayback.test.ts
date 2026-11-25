// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { usePlayback } from './usePlayback';
import type { SelectedRoom } from './useSelectedRoom';
import * as api from '../lib/api';
import { must } from '../test/must';
import { preset, room as roomFixture, selectedRoom } from './testFixtures';

vi.mock('../lib/api', () => ({ playPreset: vi.fn() }));

const jazz = preset({ tuneInId: 's1', name: 'Jazz' });

function room(speaker: string): SelectedRoom {
  return selectedRoom({
    speaker,
    lead: roomFixture({ name: speaker }),
    live: { presets: [jazz] },
  });
}

describe('usePlayback duplicate-tap guard', () => {
  beforeEach(() => vi.clearAllMocks());

  it('drops a repeat tap on the same speaker but not the same preset on another speaker', async () => {
    let release: () => void = vi.fn();
    vi.mocked(api.playPreset)
      .mockImplementationOnce(async () => new Promise<void>((r) => (release = r)))
      .mockResolvedValue(undefined);

    const { result, rerender } = renderHook(({ sel }) => usePlayback(sel, () => vi.fn()), {
      initialProps: { sel: room('kitchen') },
    });

    act(() => void result.current.playSlot(1));
    act(() => void result.current.playSlot(1)); // double tap while pending: ignored
    expect(api.playPreset).toHaveBeenCalledTimes(1);

    rerender({ sel: room('bedroom') });
    await act(async () => result.current.playSlot(1)); // same name, other speaker: sent
    expect(api.playPreset).toHaveBeenCalledTimes(2);
    expect(must(vi.mocked(api.playPreset).mock.calls[1], 'the second play')[3]).toBe('bedroom');

    await act(async () => release());
  });
});
