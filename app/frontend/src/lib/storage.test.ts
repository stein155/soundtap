// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, expect, it } from 'vitest';

import html from '../../index.html?raw';
import { PREF } from './storage';

describe('PREF.theme', () => {
  // The colour scheme follows the system and has no setting: the first-paint script must
  // not apply a choice an older build saved, or a phone would stay dark with no way back.
  it('is not read by the first-paint script in index.html', () => {
    expect(html).not.toContain(`localStorage.getItem('${PREF.theme}')`);
    expect(html).toContain('prefers-color-scheme: dark');
  });
});
