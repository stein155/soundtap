// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { createContext, useCallback, useContext, useEffect, useState } from 'react';
import { PREF, readPref, writePref } from '../lib/storage';

// The colour scheme preference: follow the system, or always light or dark. Whatever
// the preference, a concrete 'light' | 'dark' goes on <html data-theme>, which is what
// tokens.css keys off; in 'system' mode it follows the OS live. index.html applies the
// same value before first paint, so keep the key and the resolution in step with it.

export type ThemeMode = 'system' | 'light' | 'dark';

const THEME_COLOR = { light: '#f9f9f9', dark: '#121212' };
const isMode = (v: unknown): v is ThemeMode => v === 'system' || v === 'light' || v === 'dark';
const darkScheme = () =>
  typeof window !== 'undefined' && 'matchMedia' in window
    ? window.matchMedia('(prefers-color-scheme: dark)')
    : null;

function apply(mode: ThemeMode) {
  const theme = mode === 'system' ? (darkScheme()?.matches ? 'dark' : 'light') : mode;
  document.documentElement.setAttribute('data-theme', theme);
  document.querySelector('meta[name="theme-color"]')?.setAttribute('content', THEME_COLOR[theme]);
}

export function useThemeMode() {
  const [mode, setModeState] = useState<ThemeMode>(() => {
    const stored = readPref(PREF.theme);
    return isMode(stored) ? stored : 'system';
  });

  useEffect(() => {
    apply(mode);
    writePref(PREF.theme, mode);
    if (mode !== 'system') return undefined;
    const media = darkScheme();
    const follow = () => apply('system');
    media?.addEventListener('change', follow);
    return () => media?.removeEventListener('change', follow);
  }, [mode]);

  const setMode = useCallback((next: ThemeMode) => setModeState(next), []);
  return { mode, setMode };
}

export const ThemeContext = createContext<{ mode: ThemeMode; setMode: (mode: ThemeMode) => void }>({
  mode: 'system',
  setMode: () => {},
});
export const useTheme = () => useContext(ThemeContext);
