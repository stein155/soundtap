// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';
import { useSleepTimer } from './useSleepTimer';
import * as api from '../lib/api';
import type { SleepState } from '../lib/api/playback';

vi.mock('../lib/api', () => ({ getSleep: vi.fn(), setSleep: vi.fn() }));

// A timer set on Kitchen that answers after switching to Bedroom must not show on
// Bedroom.
describe('useSleepTimer room switch', () => {
  it('drops a timer write that completes for the previous speaker', async () => {
    let finish: (v: SleepState | null) => void = vi.fn();
    vi.mocked(api.setSleep).mockImplementation(
      async () =>
        new Promise<SleepState | null>((r) => {
          finish = r;
        })
    );
    vi.mocked(api.getSleep).mockResolvedValue({ minutes: 0, endsAt: '' });
    const { result, rerender } = renderHook(({ s }) => useSleepTimer(s, true), {
      initialProps: { s: 'kitchen' },
    });
    let pending: Promise<void> = Promise.resolve();
    act(() => {
      pending = result.current.start(30);
    });
    rerender({ s: 'bedroom' });
    await waitFor(() => expect(api.getSleep).toHaveBeenCalledWith('bedroom'));
    await act(async () => {
      finish({ minutes: 30, endsAt: new Date(Date.now() + 1800000).toISOString() });
      await pending;
    });
    expect(result.current.timer).toBeNull();
  });
});

describe('useSleepTimer past its end', () => {
  it('reads again after a failed first read', async () => {
    vi.useFakeTimers();
    const endsAt = new Date(Date.now() + 1800000).toISOString();
    vi.mocked(api.getSleep)
      .mockReset()
      .mockResolvedValueOnce(null)
      .mockResolvedValue({ minutes: 30, endsAt });
    const { result } = renderHook(() => useSleepTimer('kitchen', true));
    await act(async () => vi.advanceTimersByTimeAsync(0));
    await act(async () => vi.advanceTimersByTimeAsync(6000));
    expect(result.current.timer?.minutes).toBe(30);
    vi.useRealTimers();
  });

  it('spaces out the reads while they fail', async () => {
    vi.useFakeTimers();
    const endsAt = new Date(Date.now() + 500).toISOString();
    vi.mocked(api.getSleep)
      .mockReset()
      .mockResolvedValueOnce({ minutes: 1, endsAt })
      .mockResolvedValue(null);
    renderHook(() => useSleepTimer('kitchen', true));
    await act(async () => vi.advanceTimersByTimeAsync(1600));
    vi.mocked(api.getSleep).mockClear();
    // In steps: React applies a miss, and arms the next read, when each act ends.
    for (let step = 0; step < 20; step += 1)
      // eslint-disable-next-line no-await-in-loop -- each step lands before the next is taken
      await act(async () => vi.advanceTimersByTimeAsync(500));
    expect(vi.mocked(api.getSleep).mock.calls.length).toBeLessThanOrEqual(2);
    vi.useRealTimers();
  });
});
