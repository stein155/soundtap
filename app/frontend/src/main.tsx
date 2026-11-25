// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// The UI font ships with the app: the speaker's own setup access point has no internet,
// and loading it from Google sent every visitor's address there.
import '@fontsource-variable/montserrat';
import React from 'react';
import ReactDOM from 'react-dom/client';
import App from './App';
import { ErrorBoundary } from './ErrorBoundary';
import { initialLang } from './hooks/useLanguage';
import { loadLanguage } from './lib/i18n';
import { GlobalStyle } from './theme/GlobalStyle';

// A chunk that hangs must not keep the app away: past this the page opens in the fallback.
const LANG_WAIT_MS = 3000;
const langWait = new Promise((resolve) => setTimeout(resolve, LANG_WAIT_MS));

// The language has to be here before the first paint; without it the page falls back.
void Promise.race([loadLanguage(initialLang()), langWait])
  .catch(() => {
    // A language that failed to load: the page opens in the fallback all the same.
  })
  .finally(() => {
    const root = document.getElementById('root');
    if (!root) throw new Error('index.html has no #root');
    ReactDOM.createRoot(root).render(
      <React.StrictMode>
        <GlobalStyle />
        <ErrorBoundary>
          <App />
        </ErrorBoundary>
      </React.StrictMode>
    );
  });
