// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { dismissLaunchScreen, LAUNCH_FADE_MS } from './launchScreen';

function reducedMotion(reduce: boolean) {
  vi.stubGlobal(
    'matchMedia',
    vi.fn((query: string) => ({ matches: reduce && query.includes('reduce') }))
  );
}

describe('dismissLaunchScreen', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    document.body.innerHTML = '<div id="launch"></div><div id="root"></div>';
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('fades the launch screen out after the app is painted, then removes it', () => {
    reducedMotion(false);
    dismissLaunchScreen();
    const launch = document.getElementById('launch');
    expect(launch?.classList.contains('launch-out')).toBe(false);
    vi.advanceTimersToNextFrame();
    vi.advanceTimersToNextFrame();
    expect(launch?.classList.contains('launch-out')).toBe(true);
    expect(document.getElementById('launch')).not.toBeNull();
    vi.advanceTimersByTime(LAUNCH_FADE_MS + 100);
    expect(document.getElementById('launch')).toBeNull();
    expect(document.getElementById('root')).not.toBeNull();
  });

  it('removes it at once when the system asks for less motion', () => {
    reducedMotion(true);
    dismissLaunchScreen();
    expect(document.getElementById('launch')).toBeNull();
  });

  it('does nothing when called twice or when the launch screen is gone', () => {
    reducedMotion(false);
    dismissLaunchScreen();
    dismissLaunchScreen();
    vi.runAllTimers();
    expect(document.getElementById('launch')).toBeNull();
    expect(() => dismissLaunchScreen()).not.toThrow();
  });
});
