// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect } from 'react';
import { PREF, removePref } from '../lib/storage';
import { applyThemeColor } from '../lib/themeColor';

// The colour scheme follows the system, live: a concrete 'light' | 'dark' goes on
// <html data-theme>, which the tokens key off. index.html applies the same before first
// paint, so keep the two in step. There is no setting for it; a choice saved by an older
// build is dropped.

const darkScheme = () =>
  typeof window !== 'undefined' && 'matchMedia' in window
    ? window.matchMedia('(prefers-color-scheme: dark)')
    : null;

function apply() {
  const theme = darkScheme()?.matches === true ? 'dark' : 'light';
  document.documentElement.setAttribute('data-theme', theme);
  // A layer's own colour (now playing) stays while it shows.
  applyThemeColor();
}

export function useThemeMode(): void {
  useEffect(() => {
    removePref(PREF.theme);
    apply();
    const media = darkScheme();
    media?.addEventListener('change', apply);
    return () => media?.removeEventListener('change', apply);
  }, []);
}
