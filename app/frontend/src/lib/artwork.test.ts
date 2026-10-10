// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, expect, it } from 'vitest';
import { initials, logoURL } from './artwork';

describe('logoURL', () => {
  // The speaker's own display icon is a file:// path: the station's TuneIn logo it is.
  it('skips a logo the browser cannot load', () => {
    expect(logoURL({ logo: 'file:///opt/Bose/Internet-Radio.png', tuneInId: 's6712' })).toBe(
      'http://cdn-radiotime-logos.tunein.com/s6712g.png'
    );
  });

  // CDN logos load straight from the CDN, not through the speaker's connections.
  it('loads a CDN logo directly and speaker artwork through the speaker', () => {
    expect(logoURL({ logo: 'https://i.scdn.co/image/x' })).toBe('https://i.scdn.co/image/x');
    expect(logoURL({ logo: '/api/art/1' }, 'abc')).toBe('/api/devices/abc/api/art/1');
  });
});

describe('initials', () => {
  it('takes the first letters of the first two words', () => {
    expect(initials('NPO Radio 2')).toBe('NR');
    expect(initials('Studio Brussel')).toBe('SB');
    expect(initials('103.7 Chuck FM')).toBe('CF');
    expect(initials('kink')).toBe('KI');
    expect(initials('')).toBe('?');
  });
});
