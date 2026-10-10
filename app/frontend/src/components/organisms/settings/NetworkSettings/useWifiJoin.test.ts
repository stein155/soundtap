// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, expect, it } from 'vitest';
import { badPassword, canJoin } from './useWifiJoin';

const open = (ssid: string) => canJoin({ ssid, secure: false, password: '' });

describe('canJoin', () => {
  it('counts a network name in bytes, as the speaker does', () => {
    expect(open('a'.repeat(32))).toBe(true);
    expect(open('é'.repeat(16))).toBe(true);
    expect(open('é'.repeat(17))).toBe(false);
    expect(open('😀'.repeat(9))).toBe(false);
  });
});

describe('badPassword', () => {
  it('takes a raw key of 64 hex digits, not 64 of anything else', () => {
    expect(badPassword('aB'.repeat(32))).toBe(false);
    expect(badPassword('x'.repeat(64))).toBe(true);
  });
});
