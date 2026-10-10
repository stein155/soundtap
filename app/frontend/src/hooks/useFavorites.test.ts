// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useFavorites } from './useFavorites';
import * as api from '../lib/api';

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
      .mockResolvedValue([{ name: 'Jazz' }] as never);
    const { result } = renderHook(() => useFavorites('Kitchen', vi.fn(), vi.fn()));
    await act(() => vi.advanceTimersByTimeAsync(0));
    expect(result.current.items).toHaveLength(0);
    await act(() => vi.advanceTimersByTimeAsync(15000));
    expect(result.current.items).toHaveLength(1);
    vi.useRealTimers();
  });

  const held = () => {
    let answer = (_: unknown) => {};
    const read = new Promise<never>((resolve) => {
      answer = resolve as (_: unknown) => void;
    });
    return { read, answer };
  };

  it('keeps a late read of the old speaker off the new one', async () => {
    const late = held();
    vi.mocked(api.removeFavorite).mockResolvedValue(undefined);
    vi.mocked(api.getFavorites).mockReset();
    vi.mocked(api.getFavorites).mockResolvedValueOnce([{ name: 'A' }] as never);
    const { result, rerender } = renderHook(
      ({ speaker }) => useFavorites(speaker, vi.fn(), vi.fn()),
      { initialProps: { speaker: 'Kitchen' } }
    );
    await act(async () => {});
    vi.mocked(api.getFavorites).mockReturnValueOnce(late.read);
    let removal: Promise<void> = Promise.resolve();
    await act(async () => {
      removal = result.current.remove({ source: 'S', locations: ['a'] } as never);
    });
    vi.mocked(api.getFavorites).mockResolvedValueOnce([{ name: 'L' }] as never);
    rerender({ speaker: 'Living' });
    await act(async () => {});
    expect(result.current.items).toHaveLength(1);
    await act(async () => {
      late.answer([]);
      await removal;
    });
    expect(result.current.items).toEqual([{ name: 'L' }]);
  });

  it('keeps a late read from bringing a removed favorite back', async () => {
    const late = held();
    vi.mocked(api.removeFavorite).mockResolvedValue(undefined);
    vi.mocked(api.getFavorites).mockReset();
    vi.mocked(api.getFavorites).mockResolvedValueOnce([{ name: 'A' }, { name: 'B' }] as never);
    const { result } = renderHook(() => useFavorites('Kitchen', vi.fn(), vi.fn()));
    await act(async () => {});
    vi.mocked(api.getFavorites)
      .mockReturnValueOnce(late.read)
      .mockResolvedValueOnce([] as never);
    const item = { source: 'S', locations: ['a'] } as never;
    let first: Promise<void> = Promise.resolve();
    await act(async () => {
      first = result.current.remove(item);
    });
    await act(async () => {
      await result.current.remove(item);
    });
    expect(result.current.items).toHaveLength(0);
    await act(async () => {
      late.answer([{ name: 'B' }]);
      await first;
    });
    expect(result.current.items).toHaveLength(0);
  });
});
