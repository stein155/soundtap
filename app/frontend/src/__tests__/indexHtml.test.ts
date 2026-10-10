// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, expect, it } from 'vitest';
import html from '../../index.html?raw';

// Stray text in <head> ends it on the spot: it shows at the top of the page, and every
// tag after it lands in <body>, where iOS ignores its launch images.
describe('index.html', () => {
  it('keeps the head intact', () => {
    const { body } = new DOMParser().parseFromString(html, 'text/html');
    const text = [...body.childNodes].filter((node) => node.nodeType === Node.TEXT_NODE);
    expect(text.map((node) => node.textContent?.trim()).join('')).toBe('');
    expect(body.querySelector('link, meta, title')).toBeNull();
  });
});
