// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi, afterEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useSleepTimer } from './useSleepTimer';
import * as api from '../lib/api';

vi.mock('../lib/api', () => ({ getSleep: vi.fn(), setSleep: vi.fn() }));

afterEach(() => vi.useRealTimers());

describe('useSleepTimer', () => {
  it('drops a timer the speaker no longer has once it ends', async () => {
    vi.useFakeTimers();
    const endsAt = new Date(Date.now() + 60000).toISOString();
    vi.mocked(api.getSleep)
      .mockResolvedValueOnce({ minutes: 1, endsAt })
      .mockResolvedValue({ minutes: 0, endsAt: '' });
    const { result } = renderHook(() => useSleepTimer('Kitchen', true));
    await act(async () => vi.advanceTimersByTimeAsync(0));
    expect(result.current.timer?.minutes).toBe(1);
    await act(async () => vi.advanceTimersByTimeAsync(10 * 60000));
    expect(result.current.timer).toBeNull();
  });

  it('keeps a timer started while the opening read was still out', async () => {
    let answer: (s: api.SleepState) => void = vi.fn();
    vi.mocked(api.getSleep)
      .mockReset()
      .mockReturnValueOnce(
        new Promise((r) => {
          answer = r;
        })
      );
    const endsAt = new Date(Date.now() + 30 * 60000).toISOString();
    vi.mocked(api.setSleep).mockResolvedValue({ minutes: 30, endsAt });
    const { result } = renderHook(() => useSleepTimer('Kitchen', true));
    await act(async () => result.current.start(30));
    await act(async () => answer({ minutes: 0, endsAt: '' }));
    expect(result.current.timer?.minutes).toBe(30);
  });
});

describe('useSleepTimer read failure', () => {
  it('keeps the shown timer through a failed read and reads again', async () => {
    vi.useFakeTimers();
    const endsAt = new Date(Date.now() + 30 * 60000).toISOString();
    vi.mocked(api.getSleep)
      .mockReset()
      .mockResolvedValueOnce({ minutes: 30, endsAt })
      .mockResolvedValueOnce(null)
      .mockResolvedValue({ minutes: 30, endsAt });
    const { result } = renderHook(() => useSleepTimer('Kitchen', true));
    await act(async () => vi.advanceTimersByTimeAsync(0));
    await act(async () => vi.advanceTimersByTimeAsync(30000)); // the read that fails
    expect(result.current.timer?.minutes).toBe(30);
    await act(async () => vi.advanceTimersByTimeAsync(30000));
    expect(api.getSleep).toHaveBeenCalledTimes(3);
    expect(result.current.timer?.minutes).toBe(30);
  });
});

describe('useSleepTimer room switch', () => {
  it('keeps the timer when a change and the read after it both fail', async () => {
    vi.useFakeTimers();
    const endsAt = new Date(Date.now() + 1800000).toISOString();
    vi.mocked(api.getSleep).mockReset().mockResolvedValueOnce({ minutes: 30, endsAt });
    vi.mocked(api.setSleep).mockReset().mockRejectedValue(new Error('refused'));
    const { result } = renderHook(() => useSleepTimer('kitchen', true));
    await act(async () => vi.advanceTimersByTimeAsync(0));
    vi.mocked(api.getSleep).mockResolvedValue(null);
    await act(async () => result.current.start(60));
    expect(result.current.timer?.minutes).toBe(30);
  });

  it('does not show the previous room timer while the new room is read', async () => {
    vi.useFakeTimers();
    const endsAt = new Date(Date.now() + 30 * 60000).toISOString();
    vi.mocked(api.getSleep)
      .mockReset()
      .mockResolvedValueOnce({ minutes: 30, endsAt })
      .mockReturnValue(new Promise(vi.fn()));
    const { result, rerender } = renderHook(({ s }) => useSleepTimer(s, true), {
      initialProps: { s: 'Kitchen' },
    });
    await act(async () => vi.advanceTimersByTimeAsync(0));
    expect(result.current.timer?.minutes).toBe(30);
    rerender({ s: 'Bedroom' });
    expect(result.current.timer).toBeNull();
  });
  it('finds a timer another phone started while the view stays open', async () => {
    vi.useFakeTimers();
    const endsAt = new Date(Date.now() + 30 * 60000).toISOString();
    vi.mocked(api.getSleep)
      .mockReset()
      .mockResolvedValueOnce({ minutes: 0, endsAt: '' })
      .mockResolvedValue({ minutes: 30, endsAt });
    const { result } = renderHook(() => useSleepTimer('Kitchen', true));
    await act(async () => vi.advanceTimersByTimeAsync(0));
    expect(result.current.timer).toBeNull();
    await act(async () => vi.advanceTimersByTimeAsync(120000));
    expect(result.current.timer?.minutes).toBe(30);
  });

  it('finds a new timer after a running one was cancelled elsewhere', async () => {
    vi.useFakeTimers();
    const first = new Date(Date.now() + 30 * 60000).toISOString();
    const second = new Date(Date.now() + 60 * 60000).toISOString();
    vi.mocked(api.getSleep)
      .mockReset()
      .mockResolvedValueOnce({ minutes: 30, endsAt: first })
      .mockResolvedValueOnce({ minutes: 0, endsAt: '' })
      .mockResolvedValue({ minutes: 60, endsAt: second });
    const { result } = renderHook(() => useSleepTimer('Kitchen', true));
    await act(async () => vi.advanceTimersByTimeAsync(0));
    expect(result.current.timer?.minutes).toBe(30);
    await act(async () => vi.advanceTimersByTimeAsync(30000));
    expect(result.current.timer).toBeNull();
    await act(async () => vi.advanceTimersByTimeAsync(30000));
    expect(result.current.timer?.minutes).toBe(60);
  });
});
