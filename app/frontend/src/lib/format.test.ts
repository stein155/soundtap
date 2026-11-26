// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect } from 'vitest';
import { formatBytes } from './format';

describe('formatBytes', () => {
  it('writes the decimals the way the language does (1,5 MiB in Dutch)', () => {
    expect(formatBytes(1.5 * 2 ** 20, 'nl')).toBe('1,5 MiB');
    expect(formatBytes(1.5 * 2 ** 20, 'en')).toBe('1.5 MiB');
    expect(formatBytes(12, 'nl')).toBe('12 B');
  });
});
