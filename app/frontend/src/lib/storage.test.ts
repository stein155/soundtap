// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, expect, it } from 'vitest';

import html from '../../index.html?raw';
import { PREF } from './storage';

describe('PREF.theme', () => {
  // index.html applies the saved theme before the bundle loads; with another key there
  // a dark-mode choice flashes light on every load (it read the old name's key after a
  // rename).
  it('is the key the first-paint script in index.html reads', () => {
    expect(html).toContain(`localStorage.getItem('${PREF.theme}')`);
  });
});
