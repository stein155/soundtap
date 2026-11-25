// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi, afterEach } from 'vitest';
import {
  statusView,
  idOf,
  spotifyPlay,
  spotifyStatus,
  SpotifyError,
  LIKED_CONTEXT,
} from './spotify';
import { must } from '../test/must';

const status = (over: Partial<Parameters<typeof statusView>[0] & object>) => ({
  available: true,
  authenticated: false,
  connected: false,
  reauthRequired: false,
  state: 'unpaired',
  ...over,
});

describe('statusView', () => {
  it('maps every session state to one view', () => {
    expect(statusView(null)).toBe('unavailable');
    expect(statusView(status({ available: false, state: 'unavailable' }))).toBe('unavailable');
    expect(statusView(status({}))).toBe('pair');
    expect(statusView(status({ authenticated: true, state: 'reconnecting' }))).toBe('connecting');
    expect(statusView(status({ reauthRequired: true, state: 'auth_required' }))).toBe('reauth');
    expect(statusView(status({ authenticated: true, connected: true }))).toBe('ready');
  });
});

describe('api', () => {
  afterEach(() => vi.restoreAllMocks());

  it('takes the id out of a URI', () => {
    expect(idOf('spotify:album:2noRn2Aes5aoNVsU6iWThc')).toBe('2noRn2Aes5aoNVsU6iWThc');
  });

  it('posts a track inside its context', async () => {
    const fetchMock = vi
      .spyOn(globalThis, 'fetch')
      .mockResolvedValue(new Response('{"status":"ok"}', { status: 200 }));
    await spotifyPlay('spotify:track:x', LIKED_CONTEXT);
    const [url, init] = must(fetchMock.mock.calls[0], 'a fetch');
    expect(String(url)).toContain('/api/spotify/play');
    expect(JSON.parse(String(init?.body))).toEqual({
      uri: 'spotify:track:x',
      context: LIKED_CONTEXT,
    });
  });

  it('surfaces the speaker error code', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('{"error":"spotify_not_authenticated"}', { status: 409 })
    );
    await expect(spotifyStatus()).rejects.toEqual(new SpotifyError('spotify_not_authenticated'));
  });

  // Every call carries a deadline, so a hung request can't stall the status poll.
  it('gives every request a deadline', async () => {
    const fetchMock = vi
      .spyOn(globalThis, 'fetch')
      .mockResolvedValue(new Response('{"status":"ok"}', { status: 200 }));
    await spotifyStatus();
    expect(fetchMock.mock.calls[0]?.[1]?.signal).toBeInstanceOf(AbortSignal);
  });
});
