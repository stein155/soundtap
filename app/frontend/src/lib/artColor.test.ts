// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, expect, it } from 'vitest';
import { mainColor } from './artColor';

const pixels = (...colors: number[][]) => colors.flatMap((c) => [...c, 255]);

describe('mainColor', () => {
  it('takes the commonest colour and passes over black and white', () => {
    const rgba = pixels(
      [0, 0, 0],
      [0, 0, 0],
      [0, 0, 0],
      [255, 255, 255],
      [80, 40, 20],
      [82, 42, 22]
    );
    expect(mainColor(rgba)).toEqual([81, 41, 21]);
  });

  it('keeps pale artwork dark enough for white text', () => {
    expect(Math.max(...(mainColor(pixels([220, 200, 100])) ?? []))).toBe(110);
  });

  it('falls back on black artwork, and has nothing for a clear picture', () => {
    expect(mainColor(pixels([0, 0, 0]))).toEqual([0, 0, 0]);
    expect(mainColor([10, 20, 30, 0])).toBeNull();
  });
});
