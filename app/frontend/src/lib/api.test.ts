// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi, afterEach, beforeAll } from 'vitest';
import {
  normalizeNowPlaying,
  getVolume,
  getPresets,
  searchTuneIn,
  getMultiroom,
  getRecent,
  setVolume,
  setWifi,
  restoreBackup,
} from './api';
import { PRESS_WINDOW_MS } from './base';
import { loadLanguage } from './i18n';

// Dutch is fetched on demand like every language but English.
beforeAll(() => loadLanguage('nl'));

// Stub global.fetch with a JSON response for the fetch-backed helpers.
function mockFetch(
  body: unknown,
  { ok = true, status = 200 }: { ok?: boolean; status?: number } = {}
) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => ({
      ok,
      status,
      json: async () => body,
    }))
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

// A route held for a key press works after it: the UI waits the press window plus the
// speaker's own deadline for that work, or it reports a join that then succeeds.
describe('press routes', () => {
  it('wait the press window plus the work after it', async () => {
    mockFetch(null);
    const timeout = vi.spyOn(AbortSignal, 'timeout');
    await setWifi({ ssid: 'home', security: 'none', password: '' });
    await restoreBackup(new File(['{}'], 'backup.json'));
    // wifiJoinDeadline and restoreDeadline on the speaker.
    expect(timeout.mock.calls[0][0]).toBeGreaterThan(PRESS_WINDOW_MS + 60000);
    expect(timeout.mock.calls[1][0]).toBeGreaterThan(PRESS_WINDOW_MS + 20000);
  });
});

describe('normalizeNowPlaying', () => {
  it('names Liked Songs in the page language, whatever Spotify called it', () => {
    document.documentElement.lang = 'nl';
    const np = normalizeNowPlaying({
      source: 'SPOTIFY',
      station: 'Liked Songs',
      stationId: 'spotify:collection',
    });
    expect(np?.stationName).toBe('Gelikete nummers');
  });

  it('returns null for empty input', () => {
    expect(normalizeNowPlaying(null)).toBeNull();
    expect(normalizeNowPlaying(undefined)).toBeNull();
  });

  it('maps standby / invalid sources to { standby: true }', () => {
    expect(normalizeNowPlaying({ source: 'STANDBY' })).toEqual({
      standby: true,
    });
    expect(normalizeNowPlaying({ source: 'INVALID_SOURCE' })).toEqual({
      standby: true,
    });
    expect(normalizeNowPlaying({ source: '' })).toEqual({ standby: true });
  });

  it('normalises a playing station (trims, maps stationId -> tuneInId)', () => {
    const np = normalizeNowPlaying({
      source: 'TUNEIN',
      station: '  Jazz FM ',
      track: ' Song ',
      artist: ' Artist ',
      playStatus: 'PLAY_STATE',
      art: 'http://a',
      stationId: 's42',
    });
    expect(np).toEqual({
      standby: false,
      source: 'TUNEIN',
      stationName: 'Jazz FM',
      track: 'Song',
      artist: 'Artist',
      album: '',
      playStatus: 'PLAY_STATE',
      art: 'http://a',
      tuneInId: 's42',
      canSkip: false,
      failed: false,
    });
  });

  it('falls back to the track for the station name and null tuneInId', () => {
    const np = normalizeNowPlaying({ source: 'TUNEIN', track: 'Only Track' });
    expect(np).toMatchObject({ stationName: 'Only Track', tuneInId: null });
  });
});

describe('getVolume', () => {
  it('returns the numeric volume', async () => {
    mockFetch({ volume: 42 });
    expect(await getVolume()).toBe(42);
  });
  it('shows a muted speaker as silent although it keeps its level', async () => {
    mockFetch({ volume: 42, muted: true });
    expect(await getVolume()).toBe(0);
  });
  it('returns null on a non-numeric / failed response', async () => {
    mockFetch({});
    expect(await getVolume()).toBeNull();
  });
});

describe('multiroom', () => {
  it('reads clients, and null when the speaker runs no snapserver', async () => {
    mockFetch([]);
    expect(await getMultiroom()).toEqual([]);
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({
        ok: false,
        status: 503,
        json: async () => ({}),
      }))
    );
    expect(await getMultiroom()).toBeNull();
  });
});

describe('setVolume', () => {
  it('marks a group change, and only a group change', async () => {
    const fetchMock = vi.fn(async (..._args: unknown[]) => ({
      ok: true,
      status: 204,
      json: async () => null,
    }));
    vi.stubGlobal('fetch', fetchMock);
    await setVolume(30);
    await setVolume(40, '', { group: true });
    expect(fetchMock.mock.calls[0][1]).toMatchObject({ body: JSON.stringify({ volume: 30 }) });
    expect(fetchMock.mock.calls[1][1]).toMatchObject({
      body: JSON.stringify({ volume: 40, group: true }),
    });
  });
});

describe('getPresets', () => {
  it('expands to a fixed 6-slot array keyed by slot', async () => {
    mockFetch([
      {
        slot: 1,
        name: 'A',
        stationId: 's1',
        location: '/v1/playback/station/s1',
        logo: 'l1',
      },
      { slot: 3, name: 'C', location: '/v1/playback/station/s9' }, // tuneInId from location
      { slot: 7, name: 'X' }, // out of range -> ignored
    ]);
    const slots = (await getPresets())!;
    expect(slots).toHaveLength(6);
    expect(slots[0]).toMatchObject({
      slot: 1,
      name: 'A',
      tuneInId: 's1',
      logo: 'l1',
    });
    expect(slots[2]).toMatchObject({ slot: 3, name: 'C', tuneInId: 's9' }); // parsed from location
    expect(slots[1]).toBeNull();
    expect(slots[5]).toBeNull(); // slot 7 dropped
  });

  it('returns null, not empty slots, when the request fails', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({
        ok: false,
        status: 500,
        json: async () => ({}),
      }))
    );
    expect(await getPresets()).toBeNull();
  });

  it('keeps UPnP presets out of TuneIn fallback', async () => {
    mockFetch([
      {
        slot: 2,
        source: 'UPNP',
        name: 'Song',
        stationId: 'song.flac',
        location: 'http://media/song.flac',
      },
    ]);
    const slots = (await getPresets())!;
    expect(slots[1]).toMatchObject({ source: 'UPNP', name: 'Song', tuneInId: null });
  });
});

describe('getRecent', () => {
  it('names Liked Songs in the language of the page', async () => {
    document.documentElement.lang = 'nl';
    mockFetch([
      { source: 'SPOTIFY', location: 'spotify:collection', name: 'Liked Songs', art: '' },
      { source: 'TUNEIN', location: 's1', name: 'Jazz', art: '' },
    ]);
    const list = await getRecent();
    expect(list?.map((e) => e.name)).toEqual(['Gelikete nummers', 'Jazz']);
  });
});

describe('searchTuneIn', () => {
  it('flattens nested OPML, keeps only s-prefixed stations, caps at 30', async () => {
    const items = Array.from({ length: 40 }, (_, i) => ({
      type: 'audio',
      guide_id: `s${i}`,
      text: `Station ${i}`,
      image: `img${i}`,
    }));
    // A non-station and a nested child to exercise the walker.
    mockFetch({
      body: [{ type: 'link', guide_id: 'x1', text: 'not a station' }, { children: items }],
    });
    const res = await searchTuneIn('jazz');
    expect(res).toHaveLength(30); // capped
    expect(res![0]).toMatchObject({
      tuneInId: 's0',
      name: 'Station 0',
      logo: 'img0',
    });
    expect(res!.some((s) => s.tuneInId === 'x1')).toBe(false);
  });

  it('returns null on a failed search, so the UI can say TuneIn is unreachable', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({
        ok: false,
        status: 502,
        json: async () => ({}),
      }))
    );
    expect(await searchTuneIn('x')).toBeNull();
  });
});
