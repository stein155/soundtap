// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';
import { useTuneInBrowser } from './useTuneInBrowser';
import type { BrowseResult, Station } from '../lib/types';

const station = (name: string): Station => ({
  tuneInId: name,
  name,
  tagline: '',
  genre: '',
  country: '',
  logo: '',
});

const search = vi.hoisted(() => vi.fn());
const browse = vi.hoisted(() =>
  vi.fn(async (): Promise<BrowseResult | null> => Promise.resolve({ categories: [], stations: [] }))
);
vi.mock('../lib/api', () => ({
  searchTuneIn: search,
  browseTuneIn: browse,
}));

describe('useTuneInBrowser', () => {
  it('keeps the last results on screen while a changed query is on its way', async () => {
    let answer: (stations: Station[]) => void = vi.fn();
    search.mockResolvedValueOnce([station('Jazz FM')]);
    search.mockImplementationOnce(
      async () =>
        new Promise<Station[]>((resolve) => {
          answer = resolve;
        })
    );
    const { result } = renderHook(() => useTuneInBrowser(true, 'en'));

    act(() => result.current.setQuery('jaz'));
    // Before the first answer there is nothing to keep: a spinner.
    expect(result.current.searching).toBe(true);
    await waitFor(() => expect(result.current.results.map((s) => s.name)).toEqual(['Jazz FM']));

    act(() => result.current.setQuery('jazz'));
    expect(result.current.searching).toBe(false);
    expect(result.current.results.map((s) => s.name)).toEqual(['Jazz FM']);

    await waitFor(() => expect(search).toHaveBeenCalledTimes(2));
    act(() => answer([station('Jazz Radio')]));
    await waitFor(() => expect(result.current.results.map((s) => s.name)).toEqual(['Jazz Radio']));
  });

  it('never shows the directory of the previous language while the new one loads', async () => {
    const english = { categories: [{ title: 'Music', path: 'c=music' }], stations: [] };
    browse.mockResolvedValueOnce(english);
    browse.mockImplementationOnce(async () => new Promise(vi.fn()));
    const { result, rerender } = renderHook(({ lang }) => useTuneInBrowser(true, lang), {
      initialProps: { lang: 'en' },
    });
    await waitFor(() => expect(result.current.level).toEqual(english));

    rerender({ lang: 'nl' });
    expect(result.current.browsing).toBe(true);
    expect(result.current.level).toEqual({ categories: [], stations: [] });
  });
});
