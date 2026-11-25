// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { afterEach, describe, expect, it } from 'vitest';
import { applyThemeColor, resetThemeColor, setThemeColor, washColor } from './themeColor';

const content = () => document.querySelector('meta[name="theme-color"]')?.getAttribute('content');

describe('theme colour', () => {
  afterEach(() => {
    document.head.innerHTML = '';
    document.documentElement.removeAttribute('data-theme');
  });

  it('darkens the wash for the bar above now playing', () => {
    expect(washColor('200 100 0')).toBe('#6e3700');
    expect(washColor('0 0 0')).toBe('#000000');
  });

  it("sets a layer's colour and goes back to the scheme's own", () => {
    document.head.innerHTML = '<meta name="theme-color" content="#f9f9f9" />';
    document.documentElement.setAttribute('data-theme', 'dark');
    setThemeColor('#123456');
    expect(content()).toBe('#123456');
    resetThemeColor();
    expect(content()).toBe('#121212');
  });

  it("keeps a layer's colour through a change of scheme", () => {
    document.head.innerHTML = '<meta name="theme-color" content="#f9f9f9" />';
    setThemeColor('#123456');
    document.documentElement.setAttribute('data-theme', 'dark');
    applyThemeColor();
    expect(content()).toBe('#123456');
    resetThemeColor();
    expect(content()).toBe('#121212');
  });
});
