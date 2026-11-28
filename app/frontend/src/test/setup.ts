// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import '@testing-library/jest-dom/vitest';
import { configure } from '@testing-library/react';
import { afterEach, vi } from 'vitest';

// A find's default second is too short for a whole app render on a busy CI host.
configure({ asyncUtilTimeout: 10000 });

const storage = new Map<string, string>();
Object.defineProperty(globalThis, 'localStorage', {
  configurable: true,
  value: {
    getItem: (key: string) => storage.get(key) ?? null,
    setItem: (key: string, value: unknown) => storage.set(key, String(value)),
    removeItem: (key: string) => storage.delete(key),
    clear: () => storage.clear(),
  },
});
// A test's preferences (the last rooms seen, above all) must not seed the next one.
// Nor may a speaker's settings or presets the page held. Imported here, after the test, so
// the store is the one the test's own mocks of lib/api reached.
afterEach(async () => {
  storage.clear();
  (await import('../hooks/speaker/store')).resetSpeakerResources();
  // Nor an update the page was following.
  (await import('../hooks/updates')).resetUpdates();
});

// jsdom doesn't implement these; several components touch them.
if (!(window as Partial<Window>).matchMedia) {
  window.matchMedia = (query) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: () => false,
  });
}
