// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ErrorBoundary } from './ErrorBoundary';
import { loadLanguage } from './lib/i18n';

function Broken(): never {
  throw new Error('bad payload');
}

const crash = () =>
  render(
    <ErrorBoundary>
      <Broken />
    </ErrorBoundary>
  );

describe('ErrorBoundary', () => {
  afterEach(() => {
    document.documentElement.lang = '';
    vi.restoreAllMocks();
  });

  it('shows a reload and the diagnostics link instead of a blank page', () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    crash();
    expect(screen.getByRole('heading', { name: 'Something went wrong' })).toBeTruthy();
    expect(screen.getByRole('alert').textContent).toContain('bad payload');
    expect(screen.getByRole('link', { name: 'Diagnostics' }).getAttribute('href')).toBe(
      '/api/debug'
    );
  });

  it('reloads the page when asked', () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    const reload = vi.fn();
    vi.spyOn(window, 'location', 'get').mockReturnValue({ ...window.location, reload });
    crash();
    fireEvent.click(screen.getByRole('button', { name: 'Reload now' }));
    expect(reload).toHaveBeenCalled();
  });

  it('speaks the language the page was shown in', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    await loadLanguage('nl');
    document.documentElement.lang = 'nl';
    crash();
    expect(screen.getByRole('heading', { name: 'Er ging iets mis' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Nu herladen' })).toBeTruthy();
  });

  it('falls back to English when the page’s language never arrived', () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    document.documentElement.lang = 'ko';
    crash();
    expect(screen.getByRole('heading', { name: 'Something went wrong' })).toBeTruthy();
  });
});
