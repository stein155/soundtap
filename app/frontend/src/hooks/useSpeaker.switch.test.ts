// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { renderHook, waitFor, act } from '@testing-library/react';
import { useSpeaker } from './useSpeaker';

vi.mock('../lib/api', () => ({
  getNowPlaying: vi.fn(async (s: string) => (s === 'dead' ? null : { standby: true })),
  getVolume: vi.fn(async (s: string) => (s === 'kitchen' ? 70 : null)),
  getPresets: vi.fn().mockResolvedValue(Array(6).fill(null)),
  setVolume: vi.fn(),
  normalizeNowPlaying: (x: unknown) => x,
}));
vi.mock('../lib/events', () => ({ subscribeState: () => vi.fn() }));

describe('useSpeaker on a room switch', () => {
  it('drops the volume of the old room when the new room gives none', async () => {
    const { result, rerender } = renderHook(({ s }) => useSpeaker(s), {
      initialProps: { s: 'kitchen' },
    });
    await waitFor(() => expect(result.current.volume).toBe(70));
    rerender({ s: 'bedroom' });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.volume).not.toBe(70);
  });
  it('drops the unreachable state of the old room', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const { result, rerender } = renderHook(({ s }) => useSpeaker(s), {
      initialProps: { s: 'dead' },
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30000);
    });
    expect(result.current.unreachable).toBe(true);
    rerender({ s: 'bedroom' });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });
    expect(result.current.loading).toBe(false);
    expect(result.current.unreachable).toBe(false);
    vi.useRealTimers();
  });
});
