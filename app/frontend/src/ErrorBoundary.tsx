// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Component, type ErrorInfo, type ReactNode } from 'react';
import { DEFAULT_LANG, makeT } from './lib/i18n';

// The page's language as useLanguage last set it on <html lang>; English before that.
const pageLang = () => document.documentElement.lang || DEFAULT_LANG;

// The last line of defence: one bad payload used to blank the whole UI, the Wi-Fi setup
// page in AP mode included. Plain markup on purpose -- theme and the i18n context live
// inside what just failed -- but in the page's language: the messages it already loaded
// are read straight from lib/i18n, so the fallback needs nothing that may have broken.
export class ErrorBoundary extends Component<{ children: ReactNode }, { error?: Error }> {
  state: { error?: Error } = {};

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error(error, info.componentStack);
  }

  render() {
    if (!this.state.error) return this.props.children;
    const t = makeT(pageLang());
    return (
      <div role="alert" style={{ fontFamily: 'sans-serif', padding: 16, maxWidth: 480 }}>
        <h1 style={{ fontSize: 20 }}>{t('crashTitle')}</h1>
        <p>{t('crashHint')}</p>
        <p>
          <code>{this.state.error.message}</code>
        </p>
        <button type="button" onClick={() => window.location.reload()}>
          {t('reloadNow')}
        </button>{' '}
        <a href="/api/debug">{t('diagnostics')}</a>
      </div>
    );
  }
}
