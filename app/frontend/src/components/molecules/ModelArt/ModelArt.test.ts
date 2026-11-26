// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect } from 'vitest';
import { modelKey } from '.';

describe('modelKey', () => {
  // The Wireless Link drew as an ST10 wherever the model came as its short name.
  it.each([
    ['SoundTouch Wireless Link Adapter', 'wla'],
    ['WLA', 'wla'],
    ['SoundTouch 300', 'st300'],
    ['ST300', 'st300'],
    ['SoundTouch 20', 'st20'],
    ['ST20', 'st20'],
    ['SoundTouch 10', 'st10'],
    ['ST10', 'st10'],
    [undefined, 'st10'],
  ])('draws %s as %s', (model, key) => {
    expect(modelKey(model)).toBe(key);
  });
});
