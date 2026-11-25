// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import ReactDOM from 'react-dom/client';

vi.mock('@fontsource-variable/montserrat', () => ({}));
vi.mock('./App', () => ({ default: () => null }));
vi.mock('react-dom/client', () => ({
  default: { createRoot: vi.fn(() => ({ render: vi.fn() })) },
}));
vi.mock('./lib/i18n', async (actual) => ({
  ...(await actual<typeof import('./lib/i18n')>()),
  loadLanguage: async () => new Promise(vi.fn()),
}));

describe('startup', () => {
  it('opens the app when a language chunk hangs', async () => {
    vi.useFakeTimers();
    document.body.innerHTML = '<div id="root"></div>';
    await import('./main');
    expect(ReactDOM.createRoot).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(3000);
    expect(ReactDOM.createRoot).toHaveBeenCalledTimes(1);
    vi.useRealTimers();
  });
});
