// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { usePlayback } from './usePlayback';
import type { SelectedRoom } from './useSelectedRoom';
import * as api from '../lib/api';

vi.mock('../lib/api', () => ({ playPreset: vi.fn(), storePreset: vi.fn(), stopPlayback: vi.fn() }));
const i18n = vi.hoisted(() => ({ t: (key: string) => key }));
vi.mock('../lib/i18n', () => ({ useI18n: () => ({ t: i18n.t }) }));

const refused = Object.assign(new Error('forbidden'), { status: 403 });

function selected(): SelectedRoom {
  return {
    speaker: 'kitchen',
    lead: { id: 'kitchen', name: 'Kitchen' },
    live: {
      presets: [{ tuneInId: 's1', name: 'Jazz' }],
      player: { status: 'idle' },
      playOptimistic: vi.fn(),
      nudge: vi.fn(),
      refreshNowPlaying: vi.fn(),
      cancelPending: vi.fn(),
    },
  } as unknown as SelectedRoom;
}

describe('usePlayback play failure', () => {
  beforeEach(() => vi.clearAllMocks());

  it('hands the speaker error to the reporter when a play request fails', async () => {
    vi.mocked(api.playPreset).mockRejectedValue(refused);
    const report = vi.fn();
    const { result } = renderHook(() => usePlayback(selected(), () => report));
    await act(() => result.current.playSlot(1));
    expect(report).toHaveBeenCalledWith(refused);
  });

  it('reports a stream the speaker gave up on once, also across a language change', () => {
    const room = selected();
    (room.live as { gaveUp: unknown }).gaveUp = { name: 'Jazz' };
    const block = vi.fn();
    const { rerender } = renderHook(() => usePlayback(room, () => vi.fn(), undefined, block));
    expect(block).toHaveBeenCalledTimes(1);
    i18n.t = (key: string) => `nl:${key}`;
    rerender();
    expect(block).toHaveBeenCalledTimes(1);
  });
});

describe('usePlayback storing a custom stream', () => {
  beforeEach(() => vi.clearAllMocks());

  it('names the address as the problem when the speaker refuses it', async () => {
    vi.mocked(api.storePreset).mockRejectedValue(Object.assign(new Error('bad'), { status: 400 }));
    const report = vi.fn();
    const notify = vi.fn();
    const room = selected();
    (room.live as { refreshPresets?: unknown }).refreshPresets = vi.fn();
    const { result } = renderHook(() => usePlayback(room, () => report, notify));
    await act(() => result.current.pick({ tuneInId: 'http://x.invalid/s', name: 'X' } as never, 2));
    expect(notify).toHaveBeenCalledWith(expect.stringContaining('customHelpInvalid'));
    expect(report).not.toHaveBeenCalled();
    await act(() => result.current.pick({ tuneInId: 's9', name: 'Y' } as never, 2));
    expect(report).toHaveBeenCalledTimes(1);
  });
});

describe('usePlayback stop failure', () => {
  beforeEach(() => vi.clearAllMocks());

  it('releases the optimistic stop and re-reads the speaker when Stop fails', async () => {
    vi.mocked(api.stopPlayback).mockRejectedValue(refused);
    const report = vi.fn();
    const room = selected();
    const live = room.live as unknown as Record<string, ReturnType<typeof vi.fn>>;
    live.stopOptimistic = vi.fn();
    live.releaseStopHold = vi.fn();
    const { result } = renderHook(() => usePlayback(room, () => report));
    await act(() => result.current.transport());
    expect(report).toHaveBeenCalledWith(refused);
    expect(live.releaseStopHold).toHaveBeenCalledTimes(1);
    expect(live.refreshNowPlaying).toHaveBeenCalledTimes(1);
  });
});
