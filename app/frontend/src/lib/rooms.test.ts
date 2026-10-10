// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect } from 'vitest';
import { sourceOf, sourcesOf, stationOf, roomOn } from './rooms';

describe('room state', () => {
  it('reads a playing station, a paused Spotify session and an input', () => {
    const radio = {
      standby: false,
      source: 'TUNEIN',
      stationName: 'NPO 3FM',
      tuneInId: 's6707',
      playStatus: 'PLAY_STATE',
    };
    expect(stationOf(radio)).toMatchObject({
      name: 'NPO 3FM',
      title: 'NPO 3FM',
      key: 's6707',
      spotify: false,
      paused: false,
    });
    const sp = {
      standby: false,
      source: 'SPOTIFY',
      track: 'Song',
      artist: 'Band',
      playStatus: 'PAUSE_STATE',
    };
    expect(stationOf(sp)).toMatchObject({ name: 'Song', spotify: true, paused: true });
    const list = { ...sp, album: 'Record', stationName: 'My mix' };
    expect(stationOf(list)).toMatchObject({ name: 'My mix', title: 'Song', sub: 'Band • Record' });
    const aux = { standby: false, source: 'AUX', playStatus: 'PLAY_STATE' };
    expect(stationOf(aux)).toBeNull();
    expect(sourceOf(aux)).toBe('aux');
    expect(roomOn({ now: aux })).toBe(true);
    expect(sourceOf({ standby: false, source: 'PRODUCT', stationName: 'TV' })).toBe('tv');
    expect(sourceOf({ standby: false, source: 'PRODUCT', stationName: 'HDMI 1' })).toBe('hdmi');
    expect(sourceOf({ standby: false, source: 'PRODUCT', stationName: 'Optical 1' })).toBe('tv');
  });

  it('treats standby, a stopped stream and a follower as not playing their own station', () => {
    expect(roomOn({ now: { standby: true } })).toBe(false);
    expect(
      stationOf({ standby: false, source: 'TUNEIN', stationName: 'X', playStatus: 'STOP_STATE' })
    ).toBeNull();
    expect(
      stationOf({ standby: false, source: 'MULTIROOM', stationName: 'X', playStatus: 'PLAY_STATE' })
    ).toBeNull();
  });

  it('offers only the inputs the speaker has', () => {
    expect(sourcesOf([])).toEqual(['wifi']);
    expect(sourcesOf(['AUX', 'BLUETOOTH'])).toEqual(['wifi', 'bluetooth', 'aux']);
    expect(sourcesOf(['TV'])).toEqual(['wifi', 'tv']);
  });
});
