// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  setSpotifyLiked,
  setSpotifyRepeat,
  setSpotifyShuffle,
  spotifyLiked,
  spotifyModes,
} from '../../../lib/api/playback';
import { SpotifyModeButtons } from './parts';
import { must } from '../../../test/must';

vi.mock('../../../lib/api/playback', () => ({
  spotifyModes: vi.fn(),
  setSpotifyShuffle: vi.fn().mockResolvedValue(null),
  setSpotifyRepeat: vi.fn().mockResolvedValue(null),
  spotifyLiked: vi.fn(),
  setSpotifyLiked: vi.fn().mockResolvedValue(null),
}));

describe('SpotifyModeButtons', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(spotifyLiked).mockResolvedValue({ available: false, liked: false });
  });

  it('saves what plays to Liked Songs and goes back when that fails', async () => {
    vi.mocked(spotifyModes).mockResolvedValue({ shuffle: false, repeat: 'off' });
    vi.mocked(spotifyLiked).mockResolvedValue({ available: true, liked: false });
    render(<SpotifyModeButtons speaker="a" track="one" />);
    const like = await screen.findByRole('button', { name: 'Liked Songs' });
    await userEvent.click(like);
    expect(setSpotifyLiked).toHaveBeenCalledWith(true, 'a');
    expect(like).toHaveAttribute('aria-pressed', 'true');

    vi.mocked(setSpotifyLiked).mockRejectedValueOnce(new Error('no'));
    await userEvent.click(like);
    expect(setSpotifyLiked).toHaveBeenLastCalledWith(false, 'a');
    await waitFor(() => expect(like).toHaveAttribute('aria-pressed', 'true'));
  });

  it('asks again when the track changes and hides for what cannot be liked', async () => {
    vi.mocked(spotifyModes).mockResolvedValue({ shuffle: false, repeat: 'off' });
    vi.mocked(spotifyLiked).mockResolvedValueOnce({ available: true, liked: true });
    const { rerender } = render(<SpotifyModeButtons track="one" />);
    await screen.findByRole('button', { name: 'Liked Songs' });
    rerender(<SpotifyModeButtons track="episode" />);
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Liked Songs' })).toBeNull());
    expect(spotifyLiked).toHaveBeenCalledTimes(2);
  });

  it('shows what the session has and sends the change', async () => {
    vi.mocked(spotifyModes).mockResolvedValue({ shuffle: true, repeat: 'context' });
    render(<SpotifyModeButtons speaker="a" />);
    const [first, second] = screen.getAllByRole('button');
    const shuffle = must(first, 'shuffle');
    const repeat = must(second, 'repeat');
    await waitFor(() => expect(shuffle).toHaveAttribute('aria-pressed', 'true'));
    expect(repeat).toHaveAttribute('aria-pressed', 'true');

    await userEvent.click(shuffle);
    expect(setSpotifyShuffle).toHaveBeenCalledWith(false, 'a');
    await userEvent.click(repeat);
    expect(setSpotifyRepeat).toHaveBeenCalledWith('track', 'a');
  });

  it('turns repeat off from the last step', async () => {
    vi.mocked(spotifyModes).mockResolvedValue({ shuffle: false, repeat: 'track' });
    render(<SpotifyModeButtons />);
    const repeat = must(screen.getAllByRole('button')[1], 'repeat');
    await waitFor(() => expect(repeat).toHaveAttribute('aria-pressed', 'true'));
    await userEvent.click(repeat);
    expect(setSpotifyRepeat).toHaveBeenCalledWith('off', undefined);
  });

  it('keeps a tap when a poll that left before it answers late', async () => {
    let answer: (modes: { shuffle: boolean; repeat: 'off' }) => void = vi.fn();
    vi.mocked(spotifyModes)
      .mockImplementationOnce(async () => new Promise((resolve) => (answer = resolve)))
      .mockResolvedValue({ shuffle: true, repeat: 'off' });
    vi.mocked(setSpotifyShuffle).mockReturnValueOnce(new Promise(vi.fn()));
    render(<SpotifyModeButtons />);
    const shuffle = must(screen.getAllByRole('button')[0], 'shuffle');
    await userEvent.click(shuffle);
    expect(shuffle).toHaveAttribute('aria-pressed', 'true');
    await act(async () => {
      answer({ shuffle: false, repeat: 'off' });
      await Promise.resolve();
    });
    expect(shuffle).toHaveAttribute('aria-pressed', 'true');
  });
});
