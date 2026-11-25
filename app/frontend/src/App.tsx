// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { lazy, Suspense, type ReactElement } from 'react';
import { HomePage } from './pages/HomePage';
import { PressPrompt } from './components/molecules/PressPrompt';
import { useLanguage } from './hooks/useLanguage';
import { useThemeMode } from './hooks/useThemeMode';
import { I18nContext } from './lib/i18n';

// Only the speaker's own setup access point opens it: other visits do not load it.
const SetupPage = lazy(async () =>
  import('./pages/SetupPage').then((m) => ({ default: m.SetupPage }))
);

// ?setup=wifi is how the speaker's own setup access point opens the page.
const isSetup = () => new URLSearchParams(window.location.search).get('setup') === 'wifi';

export default function App(): ReactElement {
  const i18n = useLanguage();
  useThemeMode();
  return (
    <I18nContext.Provider value={i18n}>
      {isSetup() ? (
        <Suspense fallback={null}>
          <SetupPage />
        </Suspense>
      ) : (
        <HomePage />
      )}
      <PressPrompt />
    </I18nContext.Provider>
  );
}
