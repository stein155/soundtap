// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { useSpeakerLanguage } from './useSpeakerLanguage';
import * as api from '../lib/api';
import { I18nContext, makeT } from '../lib/i18n';
import { PREF, writePref } from '../lib/storage';
import { speakerArg, type Room } from './useRooms';
import type { Settings } from '../lib/types';

vi.mock('../lib/api', () => ({ getSettings: vi.fn(), saveSettings: vi.fn() }));

const rooms = [
  { id: 'a', name: 'Kitchen', self: true, online: true },
  { id: 'b', name: 'Study', self: false, online: true },
  { id: 'c', name: 'Attic', self: false, online: false },
] as Room[];

const render = (initial: string) =>
  renderHook(() => useSpeakerLanguage(rooms), {
    wrapper: ({ children }: { children: ReactNode }) => (
      <I18nContext.Provider
        value={{ t: makeT('en'), lang: initial, setLang: () => {}, langFailed: false }}
      >
        {children}
      </I18nContext.Provider>
    ),
  });

describe('useSpeakerLanguage', () => {
  beforeEach(() => {
    localStorage.clear();
    vi.mocked(api.saveSettings).mockReset().mockResolvedValue(null);
    vi.mocked(api.getSettings).mockReset();
  });

  it('moves only a speaker still on the default English', async () => {
    vi.mocked(api.getSettings).mockImplementation(
      async (s) => ({ language: s ? 'de' : 'en' }) as Settings
    );
    render('nl');
    await waitFor(() => expect(api.saveSettings).toHaveBeenCalledWith({ language: 'nl' }, ''));
    expect(api.saveSettings).toHaveBeenCalledTimes(1);
  });

  it('leaves the speakers alone once this browser has picked', () => {
    writePref(PREF.lang, 'nl');
    render('nl');
    expect(api.getSettings).not.toHaveBeenCalled();
    expect(api.saveSettings).not.toHaveBeenCalled();
  });

  it('gives a pick to a speaker that comes online later', async () => {
    writePref(PREF.lang, 'en');
    let lang = 'en';
    const view = renderHook(({ list }) => useSpeakerLanguage(list), {
      initialProps: { list: rooms },
      wrapper: ({ children }: { children: ReactNode }) => (
        <I18nContext.Provider
          value={{ t: makeT('en'), lang, setLang: () => {}, langFailed: false }}
        >
          {children}
        </I18nContext.Provider>
      ),
    });
    expect(api.saveSettings).not.toHaveBeenCalled();
    lang = 'nl';
    view.rerender({ list: [...rooms] });
    expect(api.saveSettings).toHaveBeenCalledTimes(2);
    view.rerender({ list: rooms.map((r) => ({ ...r, online: true })) });
    await waitFor(() => expect(api.saveSettings).toHaveBeenCalledTimes(3));
    expect(api.saveSettings).toHaveBeenLastCalledWith({ language: 'nl' }, speakerArg(rooms[2]));
    view.rerender({ list: rooms.map((r) => ({ ...r, online: true })) });
    expect(api.saveSettings).toHaveBeenCalledTimes(3);
  });
});
