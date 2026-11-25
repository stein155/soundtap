// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect } from 'vitest';
import { recentItems, timeAgo } from './recent';

describe('recentItems', () => {
  it('keeps replayable entries once, newest first', () => {
    const at = '2026-10-04T08:00:00Z';
    const items = recentItems([
      {
        source: 'TUNEIN',
        name: 'NPO 3FM',
        location: '/v1/playback/station/s6707',
        art: '',
        stoppedAt: at,
      },
      { source: 'SPOTIFY', name: 'Song', location: '', art: '', stoppedAt: at },
      {
        source: 'TUNEIN',
        name: 'npo 3fm',
        location: '/v1/playback/station/s6707',
        art: '',
        stoppedAt: at,
      },
      {
        source: 'UPNP',
        name: 'Music Assistant',
        location: 'http://ma/flow/1.mp3',
        art: '',
        stoppedAt: at,
      },
      {
        source: 'SPOTIFY',
        name: 'Road trip',
        location: 'spotify:playlist:37i9dQZF1DXcBWIGoYBM5M',
        art: '',
        stoppedAt: at,
      },
    ]);
    expect(items.map((i) => [i.name, i.tuneInId, i.spotifyUri])).toEqual([
      ['NPO 3FM', 's6707', null],
      ['Road trip', null, 'spotify:playlist:37i9dQZF1DXcBWIGoYBM5M'],
    ]);
  });

  it('keeps different stations that share a name', () => {
    const at = '2026-10-04T08:00:00Z';
    const jazz = (source: string, location: string) => ({
      source,
      name: 'Jazz',
      location,
      art: '',
      stoppedAt: at,
    });
    const items = recentItems([
      jazz('TUNEIN', '/v1/playback/station/s111'),
      jazz('TUNEIN', '/v1/playback/station/s222'),
    ]);
    expect(items.map((i) => i.key)).toEqual(['tunein:s111', 'tunein:s222']);
  });
});

describe('recentItems locations', () => {
  it('keeps every entry a card stands for, so removing it leaves none behind', () => {
    const items = recentItems([
      {
        source: 'TUNEIN',
        name: 'Jazz',
        location: '/v1/playback/station/s1',
        art: '',
        stoppedAt: '',
      },
      { source: 'TUNEIN', name: 'Jazz', location: 's1', art: '', stoppedAt: '' },
    ]);
    expect(items).toHaveLength(1);
    expect(items[0]?.source).toBe('TUNEIN');
    expect(items[0]?.locations).toEqual(['/v1/playback/station/s1', 's1']);
  });

  it('starts a stream of the own URL again by that URL', () => {
    const url = 'https://radio.example/live';
    const [item] = recentItems([
      { source: 'TUNEIN', name: 'Mine', location: url, art: '', stoppedAt: '' },
    ]);
    expect(item?.tuneInId).toBe(url);
  });
});

describe('timeAgo', () => {
  const now = Date.parse('2026-10-04T12:00:00Z');
  it('uses the largest unit that fits', () => {
    expect(timeAgo('2026-10-04T11:59:30Z', 'en', now)).toBe('30 seconds ago');
    expect(timeAgo('2026-10-04T09:30:00Z', 'en', now)).toBe('2 hours ago');
    expect(timeAgo('2026-10-02T12:00:00Z', 'en', now)).toBe('2 days ago');
  });
  it('says nothing about an unreadable time', () => {
    expect(timeAgo('', 'en', now)).toBe('');
  });
});
