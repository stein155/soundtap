// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect } from 'vitest';
import { makeT } from './i18n';
import {
  describePlayback,
  artSrc,
  detailLine,
  sourceLine,
  sourceName,
  type PlaybackView,
} from './playback';

const t = makeT('en');
const view = (patch: Partial<PlaybackView>): PlaybackView => ({
  artwork: { kind: 'station', src: 'logo.png', initials: 'J', color: '#000' },
  title: 'Jazz',
  detail: '',
  status: '',
  live: true,
  isSpotify: false,
  skips: false,
  source: 'TUNEIN',
  transport: 'stop',
  ...patch,
});

describe('sourceName', () => {
  it('names the service of a station, the input, or standby', () => {
    expect(sourceName(view({}), t)).toBe('TuneIn');
    expect(sourceName(view({ isSpotify: true, source: 'SPOTIFY' }), t)).toBe('Spotify');
    expect(sourceName(view({ artwork: { kind: 'bluetooth' }, title: 'Bluetooth' }), t)).toBe(
      'Bluetooth'
    );
    expect(sourceName(view({ artwork: { kind: 'speaker' } }), t)).toBe('Standby');
  });
});

describe('sourceLine', () => {
  it('says live for a running station and paused for a stopped one', () => {
    expect(sourceLine(view({}), t)).toBe('Live • TuneIn');
    expect(sourceLine(view({ live: false }), t)).toBe('Paused • TuneIn');
  });

  it('prefers the detail, and has nothing for a speaker at rest', () => {
    expect(
      sourceLine(view({ isSpotify: true, source: 'SPOTIFY', detail: 'Artist • Album' }), t)
    ).toBe('Artist • Album • Spotify');
    expect(sourceLine(view({ artwork: { kind: 'speaker' } }), t)).toBe('');
  });
});

describe('detailLine', () => {
  it('leaves the source to the top bar when there is something to say', () => {
    const spotify = { isSpotify: true, source: 'SPOTIFY', detail: 'Artist • Album' };
    expect(detailLine(view(spotify), t)).toBe('Artist • Album');
    expect(detailLine(view({ detail: 'Song' }), t)).toBe('Song');
    expect(detailLine(view({}), t)).toBe('Live • TuneIn');
  });

  it('does not show the song of a paused station', () => {
    expect(detailLine(view({ detail: 'Song', live: false }), t)).toBe('Paused • TuneIn');
  });
});

describe('artSrc', () => {
  it('is the station picture, and nothing for an input', () => {
    expect(artSrc(view({}))).toBe('logo.png');
    expect(artSrc(view({ artwork: { kind: 'bluetooth' } }))).toBeNull();
  });
});

describe('a phone on Bluetooth', () => {
  const now = { source: 'BLUETOOTH', track: 'Song', artist: 'Band', album: 'Record' };

  it('shows its track under the name of the source', () => {
    const v = describePlayback(now as never, t, '');
    expect(v.title).toBe('Song');
    expect(v.detail).toBe('Band • Record');
    expect(v.artwork.kind).toBe('bluetooth');
    expect(sourceName(v, t)).toBe('Bluetooth');
  });

  it('says what the input is while the phone names nothing', () => {
    const v = describePlayback({ source: 'BLUETOOTH', track: '' } as never, t, '');
    expect(v.title).toBe('Bluetooth');
    expect(v.detail).not.toBe('');
  });
});

describe('a connected phone with nothing playing', () => {
  it('is named under the source', () => {
    const v = describePlayback(
      { source: 'BLUETOOTH', track: '', stationName: 'Anna' } as never,
      t,
      ''
    );
    expect(v.title).toBe('Bluetooth');
    expect(v.detail).toBe('Anna');
  });
});

describe('describePlayback source matrix', () => {
  const cases: [string, string, boolean][] = [
    ['AIRPLAY', 'AirPlay', false],
    ['UPNP', 'UPnP', false],
    ['TUNEIN', 'TuneIn', true],
    ['SPOTIFY', 'Spotify', false],
  ];
  it.each(cases)(
    '%s is labelled %s; its song when known, Live for radio without one',
    (source, name, radio) => {
      const now = { source, standby: false, stationName: 'Name', playStatus: 'PLAY_STATE' };
      const v = describePlayback(now, t, '');
      expect(sourceName(v, t)).toBe(name);
      expect(sourceLine(v, t)).toBe(radio ? `Live • ${name}` : name);
      const withTrack = describePlayback({ ...now, track: 'A track', artist: 'An artist' }, t, '');
      expect(sourceName(withTrack, t)).toBe(name);
      expect(sourceLine(withTrack, t)).toBe(
        source === 'SPOTIFY' ? `An artist • ${name}` : `An artist • A track • ${name}`
      );
    }
  );
});

describe('describePlayback album', () => {
  it('names the album after the artist', () => {
    const now = {
      standby: false,
      source: 'UPNP',
      stationName: 'Song',
      artist: 'Band',
      album: 'Record',
    };
    expect(describePlayback(now, t).detail).toBe('Band • Record');
  });
});
