// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { SpotifyLibrary } from '.';
import type { SpotifyLibrary as Library } from '../../../hooks/useSpotifyLibrary';

const page = {
  title: 'Road trip',
  artwork: 'cover.jpg',
  context: 'spotify:playlist:37i9dQZF1DXcBWIGoYBM5M',
  tracks: [{ uri: 'spotify:track:4Wz1qVlDlRdG4iyDxORgSw', name: 'My Name', artists: [] }],
};

const library = (hold?: unknown) =>
  ({
    statusLoaded: true,
    homeLoaded: true,
    view: 'ready',
    trail: [{ kind: 'playlist', uri: page.context, title: page.title }],
    page,
    error: '',
    hold,
    play: vi.fn(),
  }) as unknown as Library;

describe('SpotifyLibrary', () => {
  it('opens what was played lately and the artists followed from the home list', () => {
    const open = vi.fn();
    const recent = [{ kind: 'album', uri: 'spotify:album:1', name: 'Discovery' }];
    const following = [{ uri: 'spotify:artist:1', name: 'Daft Punk' }];
    const home = {
      ...library(),
      trail: [],
      playlists: [],
      recent,
      following,
      open,
    } as unknown as Library;
    render(<SpotifyLibrary library={home} />);
    fireEvent.click(screen.getByText('Discovery'));
    expect(open).toHaveBeenLastCalledWith({
      kind: 'album',
      uri: 'spotify:album:1',
      title: 'Discovery',
    });
    fireEvent.click(screen.getByText('Daft Punk'));
    expect(open).toHaveBeenLastCalledWith({
      kind: 'artist',
      uri: 'spotify:artist:1',
      title: 'Daft Punk',
    });
  });

  it('names the list, not the track, when a track starts it', () => {
    const l = library();
    render(<SpotifyLibrary library={l} />);
    fireEvent.click(screen.getByText('My Name'));
    expect(l.play).toHaveBeenCalledWith(page.tracks[0].uri, page.context, {
      uri: page.context,
      name: 'Road trip',
      artwork: 'cover.jpg',
    });
  });

  it('hands a held track to the preset page and does not play it', () => {
    vi.useFakeTimers();
    const hold = vi.fn();
    const l = library(hold);
    render(<SpotifyLibrary library={l} />);
    const row = screen.getByText('My Name');
    fireEvent.pointerDown(row);
    vi.advanceTimersByTime(600);
    fireEvent.pointerUp(row);
    fireEvent.click(row);
    vi.useRealTimers();
    expect(hold).toHaveBeenCalledWith({
      uri: page.tracks[0].uri,
      name: 'My Name',
      artwork: undefined,
    });
    expect(l.play).not.toHaveBeenCalled();
  });

  it('opens an artist of the page, once each, and not from their own page', () => {
    const ref = { uri: 'spotify:artist:1', name: 'The Band' };
    const tracks = [
      { uri: 'spotify:track:1', name: 'One', artists: [ref] },
      { uri: 'spotify:track:2', name: 'Two', artists: [ref] },
    ];
    const open = vi.fn();
    const l = { ...library(), open, page: { ...page, tracks } } as unknown as Library;
    const { rerender } = render(<SpotifyLibrary library={l} />);
    expect(screen.getAllByText('The Band')).toHaveLength(3);
    // Each track names the artist too; the list under them comes last.
    fireEvent.click(screen.getAllByText('The Band').at(-1)!);
    expect(open).toHaveBeenCalledWith({ kind: 'artist', uri: ref.uri, title: ref.name });

    const own = { ...l, page: { ...l.page, context: ref.uri } } as unknown as Library;
    rerender(<SpotifyLibrary library={own} />);
    expect(screen.queryByText('Artists')).toBeNull();
  });
});
