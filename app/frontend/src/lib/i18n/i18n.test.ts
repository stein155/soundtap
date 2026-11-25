// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { beforeAll, describe, it, expect } from 'vitest';
import {
  detectInitialLang,
  makeT,
  isSupportedLang,
  loadLanguage,
  shownLang,
  DEFAULT_LANG,
  LANGS,
  type I18nKey,
} from '.';
const files = import.meta.glob<{ default: Record<string, string> }>('./messages/*.ts', {
  eager: true,
});
const I18N = new Map(
  Object.entries(files).map(([path, file]) => [path.replace(/.*\/|\.ts$/g, ''), file.default])
);
// One language's messages; a language without a file fails the test.
const messages = (lang: string): Record<string, string> => {
  const found = I18N.get(lang);
  if (!found) throw new Error(`no messages/${lang}.ts`);
  return found;
};

describe('makeT', () => {
  beforeAll(async () => Promise.all(['de', 'fr', 'nl'].map(loadLanguage)));

  it('returns strings in the requested language', () => {
    const t = makeT('en');
    expect(t('settings')).toBe('Settings');
    expect(t('liveShort')).toBe('Live');
  });

  it('translates the same key differently per language', () => {
    expect(makeT('nl')('settings')).toBe('Instellingen');
    expect(makeT('de')('settings')).toBe('Einstellungen');
    expect(makeT('fr')('settings')).toBe('Paramètres');
  });

  it('falls back to English for an unknown language', () => {
    expect(DEFAULT_LANG).toBe('en');
    expect(makeT('xx')('settings')).toBe('Settings');
  });

  // English is the fallback, so it is in the main bundle: a language whose chunk was
  // never fetched (or failed to arrive) shows English, not raw keys.
  it('shows English for a language that was not loaded', () => {
    expect(shownLang('ja')).toBe('en');
    expect(makeT('ja')('settings')).toBe('Settings');
  });

  it('returns the key itself when it is unknown in every dictionary', () => {
    // A key from outside the type, as stale stored data could hold: unchecked JSON.
    const stale: I18nKey = JSON.parse('"nope_not_a_key"');
    expect(makeT('en')(stale)).toBe('nope_not_a_key');
  });
});

describe('interpolation', () => {
  it('fills {placeholders} and leaves unknown ones visible', () => {
    const t = makeT('en');
    expect(t('groupOf', { name: 'Kitchen' })).toBe('Kitchen group');
    expect(t('groupOf')).toBe('{name} group');
  });
});

describe('dictionaries', () => {
  it('every language ships every key (no silent fallbacks)', () => {
    const langs = LANGS.map((l) => l.code);
    const reference = Object.keys(messages(DEFAULT_LANG)).sort();
    for (const lang of langs) {
      expect(Object.keys(messages(lang)).sort(), `keys for ${lang}`).toEqual(reference);
    }
  });
});

describe('detectInitialLang', () => {
  const browser = (languages: string[]) =>
    Object.defineProperty(window.navigator, 'languages', { value: languages, configurable: true });

  it.each([
    ['zh-CN', 'zh-hans'],
    ['zh-SG', 'zh-hans'],
    ['zh', 'zh-hans'],
    ['zh-TW', 'zh-hant'],
    ['zh-Hant-HK', 'zh-hant'],
    ['no', 'nb'],
    ['nn-NO', 'nb'],
    ['pt-PT', 'pt'],
  ])('reads %s as %s', (tag, lang) => {
    browser([tag]);
    expect(detectInitialLang()).toBe(lang);
  });

  it('takes the first language the browser prefers that we have', () => {
    browser(['xx-YY', 'de-AT', 'fr']);
    expect(detectInitialLang()).toBe('de');
  });

  it('is English when the browser prefers nothing we have', () => {
    browser(['xx-YY']);
    Object.defineProperty(window.navigator, 'language', { value: 'xx', configurable: true });
    expect(detectInitialLang()).toBe('en');
  });
});

describe('isSupportedLang', () => {
  it('accepts shipped languages and rejects others', () => {
    expect(isSupportedLang('en')).toBe(true);
    expect(isSupportedLang('nl')).toBe(true);
    expect(isSupportedLang('zz')).toBe(false);
  });
});

// Key parity above proves no key is missing. These two catch the other way a
// language ends up showing English: a key that exists but was never translated.
describe('translation quality', () => {
  const langs = LANGS.map((l) => l.code);

  it('has no empty values', () => {
    const empty: string[] = [];
    for (const lang of langs) {
      for (const [key, value] of Object.entries(messages(lang))) {
        if (typeof value !== 'string' || value.trim() === '') empty.push(`${lang}.${key}`);
      }
    }
    expect(empty).toEqual([]);
  });

  // Values that are legitimately identical to English: product and protocol names,
  // and words spelled the same in the target language ("Offline" in Dutch and
  // German, "Latitude" in French, "General" in Spanish). Anything else that matches
  // English is treated as a forgotten translation.
  const shared = new Set([
    // Words the Radio v3 design keeps as-is in more than one language.
    'host',
    'broker',
    'topics',
    'normal',
    'in',
    'charts',
    'app',
    'radios',
    'station',
    'stations',
    'active',
    'source',
    'diagnostics',
    'kernel',
    'modules',
    'firmware',
    // The release names (ADR 0035) and the word in languages that spell it the same.
    'soundtap core',
    'soundtap app',
    'version',
    'wifi',
    'mqtt',
    'upnp',
    'airplay',
    'bluetooth',
    'hdmi',
    'spotify',
    'albums',
    'playlists',
    'songs',
    'aux',
    'tv',
    'tunein',
    'snapcast',
    'url',
    'ip',
    'hostname',
    'port',
    'reset',
    'standby',
    'stereo',
    'mono',
    'balance',
    'bass',
    'presets',
    'radio',
    'status',
    'model',
    'volume',
    'start',
    'stop',
    'pause',
    'no',
    'ok',
    'audio',
    'server',
    'servers',
    'media',
    'update',
    'updates',
    'software',
    'system',
    'display',
    'clock',
    'format',
    'language',
    'latency',
    'delay',
    'name',
    'version',
    'offline',
    'ethernet',
    'menu',
    'live',
    'vol',
    'multiroom',
    'signal',
    'latitude',
    'longitude',
    'excellent',
    'general',
    'speaker',
    'speakers',
    'n min',
    // Loanwords and cognates in the stock languages.
    'alarm',
    'buffering',
    'direct',
    'download',
    'for',
    'just for you',
    'password',
    'smart home',
    'stream url',
    'singles', // de, fr, nl
    'compilations', // fr
    'tone',
    'preset', // de, it, nl: the word Bose's app uses
    'update  name', // "Update · {name}" in de and nl
    'volume n', // "volume {n}" in af, fr, it, nl, pt
  ]);

  it('does not silently reuse the English string', () => {
    const untranslated: string[] = [];
    for (const lang of langs) {
      if (lang === 'en') continue;
      for (const [key, value] of Object.entries(messages(lang))) {
        const en = messages('en')[key];
        if (!en || en !== value) continue;
        const word = value
          .toLowerCase()
          .replace(/[^a-z ]/g, '')
          .trim();
        if (shared.has(word)) continue;
        untranslated.push(`${lang}.${key} ("${value}")`);
      }
    }
    // All at once: fixing one failure per run is how gaps accumulate.
    expect(untranslated).toEqual([]);
  });
});
