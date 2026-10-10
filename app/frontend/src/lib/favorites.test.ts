// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, expect, it } from 'vitest';
import { playingEntry } from './favorites';

const playing = {
  name: 'Radio Lucas & Steve',
  art: 'cover.jpg',
  track: 'DNA',
  artist: 'TEN TIMES',
};

describe('playingEntry', () => {
  it('saves a Spotify list as Spotify, without the song cover', () => {
    const uri = 'spotify:playlist:37i9dQZF1E4z2jiuH7B09L';
    expect(playingEntry({ ...playing, tuneInId: uri, source: 'SPOTIFY' })).toEqual({
      source: 'SPOTIFY',
      name: 'Radio Lucas & Steve',
      location: uri,
      art: '',
    });
  });

  it('keeps the cover of a single Spotify track', () => {
    expect(
      playingEntry({ ...playing, tuneInId: 'spotify:track:abc', source: 'SPOTIFY' })?.art
    ).toBe('cover.jpg');
  });

  it('saves a TuneIn station by id', () => {
    expect(playingEntry({ ...playing, tuneInId: 's9483', source: 'TUNEIN' })).toEqual({
      source: 'TUNEIN',
      name: 'Radio Lucas & Steve',
      location: 's9483',
      art: '',
    });
  });

  it('has nothing to save without an id', () => {
    expect(playingEntry({ ...playing, tuneInId: null })).toBeNull();
  });
});
