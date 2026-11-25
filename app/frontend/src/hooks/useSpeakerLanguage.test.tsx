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
import { must } from '../test/must';
import { room } from './testFixtures';
import { settingsFixture } from '../test/settings';

vi.mock('../lib/api', () => ({ getSettings: vi.fn(), saveSettings: vi.fn() }));

const rooms: Room[] = [
  room({ id: 'a', name: 'Kitchen', self: true }),
  room({ id: 'b', name: 'Study' }),
  room({ id: 'c', name: 'Attic', online: false }),
];

const render = (initial: string) =>
  renderHook(() => useSpeakerLanguage(rooms), {
    wrapper: ({ children }: { children: ReactNode }) => (
      <I18nContext.Provider
        value={{ t: makeT('en'), lang: initial, setLang: vi.fn(), langFailed: false }}
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
    vi.mocked(api.getSettings).mockImplementation(async (s) =>
      settingsFixture({ language: s ? 'de' : 'en' })
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
        <I18nContext.Provider value={{ t: makeT('en'), lang, setLang: vi.fn(), langFailed: false }}>
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
    expect(api.saveSettings).toHaveBeenLastCalledWith(
      { language: 'nl' },
      speakerArg(must(rooms[2], 'the third room'))
    );
    view.rerender({ list: rooms.map((r) => ({ ...r, online: true })) });
    expect(api.saveSettings).toHaveBeenCalledTimes(3);
  });
});
