// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi, afterEach } from 'vitest';
import { browseTuneIn } from './api';

// Capture the URL browseTuneIn fetches and reply with a canned OPML/JSON body.
function mockFetch(body: unknown): () => string {
  let seen = '';
  vi.stubGlobal('fetch', (url: string) => {
    seen = url;
    return Promise.resolve({ ok: true, json: () => Promise.resolve(body) } as Response);
  });
  return () => seen;
}

afterEach(() => vi.unstubAllGlobals());

describe('browseTuneIn', () => {
  it('forces the locale onto the request so labels come back translated', async () => {
    const seen = mockFetch({ body: [] });
    await browseTuneIn('/Browse.ashx?render=json', 'nl-NL');
    expect(seen()).toContain('locale=nl-NL');
  });

  it('parses stations nested under a group outline', async () => {
    mockFetch({
      body: [
        {
          element: 'outline',
          key: 'stations',
          children: [
            { type: 'audio', item: 'station', guide_id: 's123', text: 'Qmusic' },
            { type: 'link', item: 'show', guide_id: 'p9', text: 'A show' }, // not a station
          ],
        },
      ],
    });
    const res = await browseTuneIn('/Browse.ashx?render=json');
    expect(res!.stations.map((s) => s.tuneInId)).toEqual(['s123']);
  });

  it('rewrites the dead By-Language category ids to the working c=<cat> form', async () => {
    mockFetch({
      body: [
        {
          element: 'outline',
          type: 'link',
          text: 'Music',
          URL: 'http://opml.radiotime.com/Browse.ashx?id=c424724&filter=l101',
        },
      ],
    });
    const res = await browseTuneIn('/Browse.ashx?c=lang&filter=l101&render=json');
    expect(res!.categories).toHaveLength(1);
    const p = res!.categories[0].path;
    expect(p).toContain('c=music');
    expect(p).toContain('filter=l101');
    expect(p).not.toContain('id=c424724');
  });

  // Shows and podcasts are dead ends on a radio; "more stations" is not a folder.
  it('drops show and podcast links and keeps the more-stations page apart', async () => {
    mockFetch({
      body: [
        { type: 'link', text: 'Pop', URL: 'http://opml.radiotime.com/Browse.ashx?id=g61' },
        {
          type: 'link',
          item: 'show',
          guide_id: 'p1',
          text: 'Hitparade',
          URL: 'http://opml.radiotime.com/Tune.ashx?c=pbrowse&id=p1',
        },
        { type: 'link', text: 'Podcasts', URL: 'http://opml.radiotime.com/Browse.ashx?c=podcast' },
        {
          type: 'link',
          key: 'nextStations',
          text: 'Meer zenders',
          URL: 'http://opml.radiotime.com/Browse.ashx?offset=26&id=g61',
        },
      ],
    });
    const res = await browseTuneIn('/Browse.ashx?render=json');
    expect(res!.categories.map((c) => c.title)).toEqual(['Pop']);
    expect(res!.more?.title).toBe('Meer zenders');
  });
});
