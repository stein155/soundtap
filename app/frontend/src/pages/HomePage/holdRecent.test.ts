// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, expect, it, vi } from 'vitest';
import type { RecentItem } from '../../lib/recent';
import { holdRecent } from './holdRecent';

const item = (over: Partial<RecentItem>): RecentItem => ({
  key: 'k',
  name: 'Radio 1',
  art: 'logo.png',
  tuneInId: null,
  spotifyUri: null,
  source: 'TUNEIN',
  stoppedAt: '',
  locations: [],
  ...over,
});

const setup = () => {
  let store: (slot: number) => void = vi.fn();
  const layers = {
    holdForPreset: vi.fn((_entry: unknown, s: (slot: number) => void) => {
      store = s;
    }),
  };
  const playback = { pick: vi.fn(), storeSpotify: vi.fn() };
  return { layers, playback, store: (slot: number) => store(slot) };
};

describe('holdRecent', () => {
  it('holds a station, and stores it on the slot tapped', () => {
    const { layers, playback, store } = setup();
    holdRecent(layers, playback, item({ tuneInId: 's1' }));
    expect(layers.holdForPreset).toHaveBeenCalledWith(
      { source: 'TUNEIN', name: 'Radio 1', location: 's1', art: 'logo.png' },
      expect.any(Function)
    );
    store(3);
    expect(playback.pick).toHaveBeenCalledWith(expect.objectContaining({ tuneInId: 's1' }), 3);
  });

  it('holds a Spotify list as Spotify', () => {
    const { layers, playback, store } = setup();
    holdRecent(layers, playback, item({ source: 'SPOTIFY', spotifyUri: 'spotify:playlist:x' }));
    store(2);
    expect(playback.storeSpotify).toHaveBeenCalledWith(2, {
      uri: 'spotify:playlist:x',
      name: 'Radio 1',
      artwork: 'logo.png',
    });
  });
});
