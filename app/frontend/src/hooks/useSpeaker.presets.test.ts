// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';
import { useSpeaker } from './useSpeaker';
import * as api from '../lib/api';

type OnState = (s: { now?: unknown; volume?: number; presets?: string }) => void;
let push: OnState = () => {};

vi.mock('../lib/api', () => ({
  getNowPlaying: vi.fn().mockResolvedValue({ standby: true }),
  getVolume: vi.fn().mockResolvedValue(20),
  getPresets: vi.fn().mockResolvedValue(Array(6).fill(null)),
  setVolume: vi.fn(),
  normalizeNowPlaying: (x: unknown) => x,
}));
vi.mock('../lib/events', () => ({
  subscribeState: ({ onState }: { onState: OnState }) => {
    push = onState;
    return () => {};
  },
}));

// A preset stored elsewhere shows up without a reload: the stream's revision moves.
describe('useSpeaker presets revision', () => {
  it('re-reads presets when the revision changes, not when it repeats', async () => {
    renderHook(() => useSpeaker());
    await waitFor(() => expect(api.getPresets).toHaveBeenCalledTimes(1));
    act(() => push({ presets: 'a' })); // first seen: the initial load already has it
    act(() => push({ presets: 'a' }));
    expect(api.getPresets).toHaveBeenCalledTimes(1);
    act(() => push({ presets: 'b' }));
    await waitFor(() => expect(api.getPresets).toHaveBeenCalledTimes(2));
  });

  // A failed read keeps the slots and is retried by the next push of the same revision.
  it('keeps the shown presets and retries a failed refresh', async () => {
    const shown = [{ slot: 1, name: 'A' }, null, null, null, null, null];
    vi.mocked(api.getPresets).mockResolvedValueOnce(shown as never);
    const { result } = renderHook(() => useSpeaker());
    await waitFor(() => expect(result.current.presets[0]).toBeTruthy());
    act(() => push({ presets: 'a' }));
    vi.mocked(api.getPresets).mockResolvedValueOnce(null);
    act(() => push({ presets: 'b' }));
    await waitFor(() => expect(api.getPresets).toHaveBeenCalledTimes(2));
    expect(result.current.presets[0]).toBeTruthy();
    act(() => push({ presets: 'b' }));
    await waitFor(() => expect(api.getPresets).toHaveBeenCalledTimes(3));
  });
});
