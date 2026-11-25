// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useFavorites } from './useFavorites';
import * as api from '../lib/api';
import { recentEntry, recentItem } from './testFixtures';
import type { RecentEntry } from '../lib/types';

vi.mock('../lib/api', () => ({
  getFavorites: vi.fn(),
  addFavorite: vi.fn(),
  removeFavorite: vi.fn(),
}));
vi.mock('../lib/i18n', () => ({ useI18n: () => ({ t: (k: string) => k }) }));
vi.mock('../lib/recent', () => ({ recentItems: (list: unknown[]) => list }));

describe('useFavorites', () => {
  it('reads again after a failed first read', async () => {
    vi.useFakeTimers();
    vi.mocked(api.getFavorites)
      .mockResolvedValueOnce(null)
      .mockResolvedValue([recentEntry({ name: 'Jazz' })]);
    const { result } = renderHook(() => useFavorites('Kitchen', vi.fn()));
    await act(async () => vi.advanceTimersByTimeAsync(0));
    expect(result.current.items).toHaveLength(0);
    await act(async () => vi.advanceTimersByTimeAsync(15000));
    expect(result.current.items).toHaveLength(1);
    vi.useRealTimers();
  });

  const held = () => {
    let answer: (list: RecentEntry[] | null) => void = vi.fn();
    const read = new Promise<RecentEntry[] | null>((resolve) => {
      answer = resolve;
    });
    return { read, answer: (list: RecentEntry[] | null) => answer(list) };
  };

  it('keeps a late read of the old speaker off the new one', async () => {
    const late = held();
    vi.mocked(api.removeFavorite).mockResolvedValue(undefined);
    vi.mocked(api.getFavorites).mockReset();
    vi.mocked(api.getFavorites).mockResolvedValueOnce([recentEntry({ name: 'A' })]);
    const { result, rerender } = renderHook(({ speaker }) => useFavorites(speaker, vi.fn()), {
      initialProps: { speaker: 'Kitchen' },
    });
    await act(async () => Promise.resolve());
    vi.mocked(api.getFavorites).mockReturnValueOnce(late.read);
    let removal: Promise<void> = Promise.resolve();
    await act(async () => {
      removal = result.current.remove(recentItem({ source: 'S', locations: ['a'] }));
    });
    vi.mocked(api.getFavorites).mockResolvedValueOnce([recentEntry({ name: 'L' })]);
    rerender({ speaker: 'Living' });
    await act(async () => Promise.resolve());
    expect(result.current.items).toHaveLength(1);
    await act(async () => {
      late.answer([]);
      await removal;
    });
    expect(result.current.items).toEqual([recentEntry({ name: 'L' })]);
  });

  it('keeps a late read from bringing a removed favorite back', async () => {
    const late = held();
    vi.mocked(api.removeFavorite).mockResolvedValue(undefined);
    vi.mocked(api.getFavorites).mockReset();
    vi.mocked(api.getFavorites).mockResolvedValueOnce([
      recentEntry({ name: 'A' }),
      recentEntry({ name: 'B' }),
    ]);
    const { result } = renderHook(() => useFavorites('Kitchen', vi.fn()));
    await act(async () => Promise.resolve());
    vi.mocked(api.getFavorites).mockReturnValueOnce(late.read).mockResolvedValueOnce([]);
    const item = recentItem({ source: 'S', locations: ['a'] });
    let first: Promise<void> = Promise.resolve();
    await act(async () => {
      first = result.current.remove(item);
    });
    await act(async () => {
      await result.current.remove(item);
    });
    expect(result.current.items).toHaveLength(0);
    await act(async () => {
      late.answer([recentEntry({ name: 'B' })]);
      await first;
    });
    expect(result.current.items).toHaveLength(0);
  });
});
