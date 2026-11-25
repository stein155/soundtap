// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useRecent } from './useRecent';
import * as api from '../lib/api';
import type { RecentEntry } from '../lib/types';
import { recentEntry, recentItem } from './testFixtures';

vi.mock('../lib/api', () => ({ getRecent: vi.fn(), removeRecent: vi.fn() }));
vi.mock('../lib/recent', () => ({ recentItems: (list: unknown[]) => list }));

// recentItems is the identity here: one value stands for the speaker's entry and the item.
const jazzEntry = () => ({
  ...recentEntry({ name: 'Jazz', location: 'l' }),
  ...recentItem({ key: 'k', name: 'Jazz', locations: ['l'] }),
});

describe('useRecent', () => {
  it('does not show the previous room history after a switch', async () => {
    vi.useFakeTimers();
    vi.mocked(api.getRecent).mockResolvedValueOnce([recentEntry({ name: 'Jazz' })]);
    vi.mocked(api.getRecent).mockReturnValue(new Promise(vi.fn()));
    const { result, rerender } = renderHook(({ s }) => useRecent(s, ''), {
      initialProps: { s: 'Kitchen' },
    });
    await act(async () => vi.advanceTimersByTimeAsync(1000));
    expect(result.current.items).toHaveLength(1);
    rerender({ s: 'Bedroom' });
    expect(result.current.items).toHaveLength(0);
    vi.useRealTimers();
  });

  it('keeps the shown history through a failed read and reads again', async () => {
    vi.useFakeTimers();
    vi.mocked(api.getRecent)
      .mockReset()
      .mockResolvedValueOnce([recentEntry({ name: 'Jazz' })])
      .mockResolvedValueOnce(null)
      .mockResolvedValue([recentEntry({ name: 'Jazz' }), recentEntry({ name: 'Rock' })]);
    const { result, rerender } = renderHook(({ p }) => useRecent('Kitchen', p), {
      initialProps: { p: 'a' },
    });
    await act(async () => vi.advanceTimersByTimeAsync(1000));
    expect(result.current.items).toHaveLength(1);
    rerender({ p: 'b' }); // the refresh that fails
    await act(async () => vi.advanceTimersByTimeAsync(1000));
    expect(result.current.items).toHaveLength(1);
    await act(async () => vi.advanceTimersByTimeAsync(20000));
    expect(result.current.items).toHaveLength(2);
    vi.useRealTimers();
  });

  it('keeps an entry being removed away, and sends one DELETE', async () => {
    vi.useFakeTimers();
    const jazz = jazzEntry();
    let late: (l: RecentEntry[] | null) => void = vi.fn();
    vi.mocked(api.getRecent)
      .mockReset()
      .mockResolvedValueOnce([jazz])
      .mockReturnValueOnce(
        new Promise<RecentEntry[] | null>((r) => {
          late = r;
        })
      )
      .mockResolvedValue([]);
    vi.mocked(api.removeRecent).mockReset().mockReturnValue(new Promise(vi.fn()));
    const { result, rerender } = renderHook(({ p }) => useRecent('Kitchen', p), {
      initialProps: { p: 'a' },
    });
    await act(async () => vi.advanceTimersByTimeAsync(1000));
    rerender({ p: 'b' });
    await act(async () => vi.advanceTimersByTimeAsync(1000)); // the read now in flight
    act(() => void result.current.remove(jazz));
    await act(async () => late([jazz])); // answered with the entry still in it
    expect(result.current.items).toHaveLength(0);
    act(() => void result.current.remove(jazz));
    expect(api.removeRecent).toHaveBeenCalledTimes(1);
    vi.useRealTimers();
  });

  it('keeps a station in another room while it is removed from this one', async () => {
    vi.useFakeTimers();
    const jazz = jazzEntry();
    vi.mocked(api.getRecent).mockReset().mockResolvedValue([jazz]);
    vi.mocked(api.removeRecent).mockReset().mockReturnValue(new Promise(vi.fn()));
    const { result, rerender } = renderHook(({ s }) => useRecent(s, ''), {
      initialProps: { s: 'Kitchen' },
    });
    await act(async () => vi.advanceTimersByTimeAsync(1000));
    act(() => void result.current.remove(jazz));
    rerender({ s: 'Bedroom' });
    await act(async () => vi.advanceTimersByTimeAsync(1000));
    expect(result.current.items).toHaveLength(1);
    vi.useRealTimers();
  });

  it('reports a removal the speaker refused, and shows the entry again', async () => {
    vi.useFakeTimers();
    const jazz = jazzEntry();
    vi.mocked(api.getRecent).mockReset().mockResolvedValue([jazz]);
    const refused = Object.assign(new Error('forbidden'), { status: 403 });
    vi.mocked(api.removeRecent).mockReset().mockRejectedValue(refused);
    const onFail = vi.fn();
    const { result } = renderHook(() => useRecent('Kitchen', '', onFail));
    await act(async () => vi.advanceTimersByTimeAsync(1000));
    await act(async () => result.current.remove(jazz));
    expect(onFail).toHaveBeenCalledWith(refused);
    await act(async () => vi.advanceTimersByTimeAsync(1000));
    expect(result.current.items).toHaveLength(1);
    vi.useRealTimers();
  });
});
