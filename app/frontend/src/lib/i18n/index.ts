// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Translation lookup for the UI. LANGS is the language picker (each language named in
// itself); the chosen language is kept per browser (see hooks/useLanguage).
import { createContext, useContext } from 'react';
import en, { type I18nKey, type Messages } from './messages/en';

export type { I18nKey } from './messages/en';

// Each with the locale TuneIn's Browse.ashx expects, so category names ("By Location",
// ...) come back translated. TuneIn carries it into the child URLs it returns.
export const LANGS = [
  { code: 'af', label: 'Afrikaans', tuneIn: 'af-ZA' },
  { code: 'cs', label: 'Čeština', tuneIn: 'cs-CZ' },
  { code: 'da', label: 'Dansk', tuneIn: 'da-DK' },
  { code: 'de', label: 'Deutsch', tuneIn: 'de-DE' },
  { code: 'en', label: 'English', tuneIn: 'en-US' },
  { code: 'es', label: 'Español', tuneIn: 'es-ES' },
  { code: 'fr', label: 'Français', tuneIn: 'fr-FR' },
  { code: 'it', label: 'Italiano', tuneIn: 'it-IT' },
  { code: 'hu', label: 'Magyar', tuneIn: 'hu-HU' },
  { code: 'nl', label: 'Nederlands', tuneIn: 'nl-NL' },
  { code: 'nb', label: 'Norsk', tuneIn: 'nb-NO' },
  { code: 'pl', label: 'Polski', tuneIn: 'pl-PL' },
  { code: 'pt', label: 'Português', tuneIn: 'pt-BR' },
  { code: 'ro', label: 'Română', tuneIn: 'ro-RO' },
  { code: 'sl', label: 'Slovenščina', tuneIn: 'sl-SI' },
  { code: 'fi', label: 'Suomi', tuneIn: 'fi-FI' },
  { code: 'sv', label: 'Svenska', tuneIn: 'sv-SE' },
  { code: 'tr', label: 'Türkçe', tuneIn: 'tr-TR' },
  { code: 'el', label: 'Ελληνικά', tuneIn: 'el-GR' },
  { code: 'ru', label: 'Русский', tuneIn: 'ru-RU' },
  { code: 'th', label: 'ไทย', tuneIn: 'th-TH' },
  { code: 'ja', label: '日本語', tuneIn: 'ja-JP' },
  { code: 'ko', label: '한국어', tuneIn: 'ko-KR' },
  { code: 'zh-hans', label: '简体中文', tuneIn: 'zh-CN' },
  { code: 'zh-hant', label: '繁體中文', tuneIn: 'zh-TW' },
];

export const DEFAULT_LANG = 'en';

export const tuneInLocale = (lang: string): string =>
  LANGS.find((l) => l.code === lang)?.tuneIn ?? 'en-US';

// English is always there, as the reference and the fallback. The rest are fetched when
// chosen, so a page does not carry every language for every visitor.
const LOADERS = import.meta.glob<{ default: Messages }>(['./messages/*.ts', '!./messages/en.ts']);
const loaded: Partial<Record<string, Messages>> = { en };

export async function loadLanguage(lang: string): Promise<void> {
  loaded[lang] ??= (await LOADERS[`./messages/${lang}.ts`]()).default;
}

// The language a page can actually show: one whose chunk failed to load falls back.
export const shownLang = (lang: string): string => (loaded[lang] ? lang : DEFAULT_LANG);

// Values for {placeholders} in a message: t('groupOf', { name: 'Kitchen' }).
export type Vars = Record<string, string | number>;
export type TranslateFn = (key: I18nKey, vars?: Vars) => string;

export const isSupportedLang = (code: string): boolean => LANGS.some((l) => l.code === code);

// A browser's language tag as ours. As in stock, plain zh and the mainland read simplified.
function langOfTag(tag: string): string {
  const [base, region] = tag.toLowerCase().split('-');
  if (base === 'no' || base === 'nn') return 'nb';
  if (base !== 'zh') return base;
  return !region || ['cn', 'sg', 'hans'].includes(region) ? 'zh-hans' : 'zh-hant';
}

// The browser's preferred language when it is one we have, else English.
export function detectInitialLang(): string {
  const candidates =
    typeof navigator !== 'undefined' ? [...navigator.languages, navigator.language] : [];
  for (const candidate of candidates) {
    const lang = langOfTag(String(candidate || ''));
    if (isSupportedLang(lang)) return lang;
  }
  return DEFAULT_LANG;
}

const interpolate = (text: string, vars?: Vars) =>
  vars
    ? text.replace(/\{(\w+)\}/g, (match, name: string) =>
        name in vars ? String(vars[name]) : match
      )
    : text;

export function makeT(lang: string): TranslateFn {
  const messages: Partial<Messages> = loaded[shownLang(lang)] ?? en;
  return (key, vars) => interpolate(messages[key] ?? key, vars);
}

interface I18n {
  t: TranslateFn;
  lang: string;
  setLang: (lang: string) => void;
  langFailed: boolean;
}

export const I18nContext = createContext<I18n>({
  t: makeT(DEFAULT_LANG),
  lang: DEFAULT_LANG,
  setLang: () => {},
  langFailed: false,
});
export const useI18n = () => useContext(I18nContext);
