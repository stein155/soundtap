// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useRecent } from './useRecent';
import * as api from '../lib/api';

vi.mock('../lib/api', () => ({ getRecent: vi.fn(), removeRecent: vi.fn() }));
vi.mock('../lib/recent', () => ({ recentItems: (list: unknown[]) => list }));

describe('useRecent', () => {
  it('does not show the previous room history after a switch', async () => {
    vi.useFakeTimers();
    vi.mocked(api.getRecent).mockResolvedValueOnce([{ name: 'Jazz' }] as never);
    vi.mocked(api.getRecent).mockReturnValue(new Promise(() => {}));
    const { result, rerender } = renderHook(({ s }) => useRecent(s, ''), {
      initialProps: { s: 'Kitchen' },
    });
    await act(() => vi.advanceTimersByTimeAsync(1000));
    expect(result.current.items).toHaveLength(1);
    rerender({ s: 'Bedroom' });
    expect(result.current.items).toHaveLength(0);
    vi.useRealTimers();
  });

  it('keeps the shown history through a failed read and reads again', async () => {
    vi.useFakeTimers();
    vi.mocked(api.getRecent)
      .mockReset()
      .mockResolvedValueOnce([{ name: 'Jazz' }] as never)
      .mockResolvedValueOnce(null)
      .mockResolvedValue([{ name: 'Jazz' }, { name: 'Rock' }] as never);
    const { result, rerender } = renderHook(({ p }) => useRecent('Kitchen', p), {
      initialProps: { p: 'a' },
    });
    await act(() => vi.advanceTimersByTimeAsync(1000));
    expect(result.current.items).toHaveLength(1);
    rerender({ p: 'b' }); // the refresh that fails
    await act(() => vi.advanceTimersByTimeAsync(1000));
    expect(result.current.items).toHaveLength(1);
    await act(() => vi.advanceTimersByTimeAsync(20000));
    expect(result.current.items).toHaveLength(2);
    vi.useRealTimers();
  });

  it('keeps an entry being removed away, and sends one DELETE', async () => {
    vi.useFakeTimers();
    const jazz = { key: 'k', source: 'TUNEIN', locations: ['l'], name: 'Jazz' };
    let late: (l: unknown[]) => void = () => {};
    vi.mocked(api.getRecent)
      .mockReset()
      .mockResolvedValueOnce([jazz] as never)
      .mockReturnValueOnce(new Promise((r) => (late = r)) as never)
      .mockResolvedValue([] as never);
    vi.mocked(api.removeRecent)
      .mockReset()
      .mockReturnValue(new Promise(() => {}));
    const { result, rerender } = renderHook(({ p }) => useRecent('Kitchen', p), {
      initialProps: { p: 'a' },
    });
    await act(() => vi.advanceTimersByTimeAsync(1000));
    rerender({ p: 'b' });
    await act(() => vi.advanceTimersByTimeAsync(1000)); // the read now in flight
    act(() => void result.current.remove(jazz as never));
    await act(async () => late([jazz])); // answered with the entry still in it
    expect(result.current.items).toHaveLength(0);
    act(() => void result.current.remove(jazz as never));
    expect(api.removeRecent).toHaveBeenCalledTimes(1);
    vi.useRealTimers();
  });

  it('keeps a station in another room while it is removed from this one', async () => {
    vi.useFakeTimers();
    const jazz = { key: 'k', source: 'TUNEIN', locations: ['l'], name: 'Jazz' };
    vi.mocked(api.getRecent)
      .mockReset()
      .mockResolvedValue([jazz] as never);
    vi.mocked(api.removeRecent)
      .mockReset()
      .mockReturnValue(new Promise(() => {}));
    const { result, rerender } = renderHook(({ s }) => useRecent(s, ''), {
      initialProps: { s: 'Kitchen' },
    });
    await act(() => vi.advanceTimersByTimeAsync(1000));
    act(() => void result.current.remove(jazz as never));
    rerender({ s: 'Bedroom' });
    await act(() => vi.advanceTimersByTimeAsync(1000));
    expect(result.current.items).toHaveLength(1);
    vi.useRealTimers();
  });

  it('reports a removal the speaker refused, and shows the entry again', async () => {
    vi.useFakeTimers();
    const jazz = { key: 'k', source: 'TUNEIN', locations: ['l'], name: 'Jazz' };
    vi.mocked(api.getRecent)
      .mockReset()
      .mockResolvedValue([jazz] as never);
    const refused = Object.assign(new Error('forbidden'), { status: 403 });
    vi.mocked(api.removeRecent).mockReset().mockRejectedValue(refused);
    const onFail = vi.fn();
    const { result } = renderHook(() => useRecent('Kitchen', '', onFail));
    await act(() => vi.advanceTimersByTimeAsync(1000));
    await act(() => result.current.remove(jazz as never));
    expect(onFail).toHaveBeenCalledWith(refused);
    await act(() => vi.advanceTimersByTimeAsync(1000));
    expect(result.current.items).toHaveLength(1);
    vi.useRealTimers();
  });
});
