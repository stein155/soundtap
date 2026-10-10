// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';
import { useLanguage } from './useLanguage';
import { loadLanguage } from '../lib/i18n';
import { PREF, readPref, writePref } from '../lib/storage';

vi.mock('../lib/i18n', async (actual) => ({
  ...(await actual<typeof import('../lib/i18n')>()),
  loadLanguage: vi.fn(),
}));

const actualI18n = await vi.importActual<typeof import('../lib/i18n')>('../lib/i18n');

describe('useLanguage', () => {
  beforeEach(() => localStorage.clear());

  it('shows the fallback as the language when the stored one did not load', () => {
    writePref(PREF.lang, 'de');
    const { result } = renderHook(() => useLanguage());
    expect(result.current.lang).toBe('en');
    expect(result.current.t('settings')).toBe('Settings');
    expect(document.documentElement.lang).toBe('en');
    expect(readPref(PREF.lang)).toBe('de'); // tried again on the next visit
  });

  it('moves to the stored language when its chunk arrives late', async () => {
    writePref(PREF.lang, 'de');
    vi.mocked(loadLanguage).mockImplementationOnce(async () => {
      await actualI18n.loadLanguage('de');
    });
    const { result } = renderHook(() => useLanguage());
    expect(result.current.lang).toBe('en');
    await waitFor(() => expect(result.current.lang).toBe('de'));
  });

  it('lets the last pick win over a slower earlier one', async () => {
    let german: () => void = () => {};
    vi.mocked(loadLanguage)
      .mockReturnValueOnce(new Promise((r) => (german = r)))
      .mockResolvedValueOnce();
    const { result } = renderHook(() => useLanguage());
    act(() => result.current.setLang('de'));
    await act(async () => result.current.setLang('en'));
    await act(async () => german());
    expect(result.current.lang).toBe('en');
    expect(readPref(PREF.lang)).toBe('en');
  });

  it('keeps saying so when an earlier pick fails after the last one did', async () => {
    let french: (reason: Error) => void = () => {};
    vi.mocked(loadLanguage)
      .mockReturnValueOnce(new Promise((_, reject) => (french = reject)))
      .mockRejectedValueOnce(new Error('offline'));
    const { result } = renderHook(() => useLanguage());
    act(() => result.current.setLang('fr'));
    await act(async () => result.current.setLang('de'));
    await act(async () => french(new Error('offline')));
    expect(result.current.langFailed).toBe(true);
  });

  it('keeps the language and says so when a pick fails to load', async () => {
    vi.mocked(loadLanguage).mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce();
    const { result } = renderHook(() => useLanguage());
    const before = result.current.lang;
    await act(async () => result.current.setLang('de'));
    expect(result.current.lang).toBe(before);
    expect(result.current.langFailed).toBe(true);
    await act(async () => result.current.setLang('en')); // another pick clears it
    expect(result.current.langFailed).toBe(false);
  });
});
