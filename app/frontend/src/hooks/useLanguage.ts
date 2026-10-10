// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useMemo, useRef, useState } from 'react';
import { detectInitialLang, isSupportedLang, loadLanguage, makeT, shownLang } from '../lib/i18n';
import { PREF, readPref, writePref } from '../lib/storage';

export function initialLang(): string {
  const stored = readPref(PREF.lang);
  return stored && isSupportedLang(stored) ? stored : detectInitialLang();
}

// The UI language: the one picked in settings, else the browser's. Kept on <html lang>
// so a screen reader pronounces the page in the language it is shown in.
export function useLanguage() {
  const [lang, setLangState] = useState(() => shownLang(initialLang()));
  // The last pick: a slower load of an earlier one must not undo it.
  const wanted = useRef(lang);
  const [langFailed, setLangFailed] = useState(false);
  const t = useMemo(() => makeT(lang), [lang]);
  useEffect(() => {
    document.documentElement.lang = lang;
  }, [lang]);
  // main.tsx stops waiting for a slow chunk so the page can open in the fallback. When the
  // chunk still arrives the page moves to it, unless a pick came first.
  useEffect(() => {
    const first = initialLang();
    if (shownLang(first) === first) return;
    wanted.current = first;
    void (async () => {
      await loadLanguage(first);
      if (wanted.current === first) setLangState(shownLang(first));
    })().catch(() => null); // stays on the fallback, and is tried again next visit
  }, []);
  const setLang = (next: string) => {
    wanted.current = next;
    setLangFailed(false);
    loadLanguage(next)
      .then(() => {
        if (wanted.current !== next) return;
        setLangState(next);
        writePref(PREF.lang, next);
      })
      .catch(() => {
        if (wanted.current === next) setLangFailed(true);
      });
  };
  return { lang, t, setLang, langFailed };
}
