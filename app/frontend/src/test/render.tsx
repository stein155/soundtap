// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactElement, ReactNode } from 'react';
import { render, type RenderResult } from '@testing-library/react';
import { vi } from 'vitest';
import { I18nContext, makeT } from '../lib/i18n';

// Render a component inside an I18n provider (English by default, so assertions read
// in English).
export function renderWithI18n(
  ui: ReactElement,
  { lang = 'en', ...options }: { lang?: string } = {}
): RenderResult {
  const t = makeT(lang);
  const value = { t, lang, setLang: vi.fn(), langFailed: false };
  function Wrapper({ children }: { readonly children: ReactNode }) {
    return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
  }
  return render(ui, { wrapper: Wrapper, ...options });
}

export * from '@testing-library/react';
