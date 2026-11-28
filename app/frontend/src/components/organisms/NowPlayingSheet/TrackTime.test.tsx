// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { act, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { clock, TrackTime } from './TrackTime';
import * as playback from '../../../lib/api/playback';

afterEach(() => vi.restoreAllMocks());

const showWith = async (durationMs: number) => {
  vi.spyOn(playback, 'trackPosition').mockResolvedValue({
    positionMs: 65000,
    durationMs,
    seekable: false,
  });
  const out = render(
    <TrackTime playing={false} nowKey="a">
      <i>art</i>
    </TrackTime>
  );
  await act(async () => {
    await Promise.resolve();
  });
  return out;
};

describe('TrackTime', () => {
  it('writes times the way a player does', () => {
    expect(clock(0)).toBe('0:00');
    expect(clock(65000)).toBe('1:05');
    expect(clock(3665000)).toBe('1:01:05');
  });

  it('shows how far a track is: a line on the artwork, the time played and the time left', async () => {
    await showWith(180000);
    expect(screen.getByText('art')).toBeTruthy();
    expect(screen.getByText('1:05')).toBeTruthy();
    expect(screen.getByText('−1:55')).toBeTruthy();
    const bar = screen.getByRole('progressbar', { name: 'Position in track' });
    expect(bar).toHaveAttribute('aria-valuenow', '65000');
    expect(bar).toHaveAttribute('aria-valuemax', '180000');
    expect(bar).toHaveAttribute('aria-valuetext', '1:05 / 3:00');
    // Only shown: nothing to drag.
    expect(screen.queryByRole('slider')).toBeNull();
  });

  it("drops the last track's time on a new track, and keeps it over a pause", async () => {
    const read = vi.spyOn(playback, 'trackPosition').mockResolvedValue({
      positionMs: 65000,
      durationMs: 180000,
      seekable: false,
    });
    const { rerender } = render(
      <TrackTime playing nowKey="one|PLAY_STATE">
        <i>art</i>
      </TrackTime>
    );
    await act(async () => {
      await Promise.resolve();
    });
    rerender(
      <TrackTime playing={false} nowKey="one|PAUSE_STATE">
        <i>art</i>
      </TrackTime>
    );
    expect(screen.getByText('−1:55')).toBeTruthy();
    read.mockReturnValue(
      new Promise(() => {
        // the new track's answer is still on its way
      })
    );
    rerender(
      <TrackTime playing nowKey="two|PLAY_STATE">
        <i>art</i>
      </TrackTime>
    );
    expect(screen.queryByText('−1:55')).toBeNull();
    expect(screen.queryByRole('progressbar')).toBeNull();
  });

  it('shows the artwork alone for a stream without an end', async () => {
    await showWith(0);
    expect(screen.getByText('art')).toBeTruthy();
    expect(screen.queryByRole('progressbar')).toBeNull();
    expect(screen.queryByText('1:05')).toBeNull();
  });

  it('does not ask the speaker while the page is hidden, and asks the moment it shows', async () => {
    vi.useFakeTimers();
    let state: DocumentVisibilityState = 'visible';
    Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => state });
    try {
      const read = vi.spyOn(playback, 'trackPosition').mockResolvedValue({
        positionMs: 65000,
        durationMs: 180000,
        seekable: false,
      });
      render(
        <TrackTime playing={false} nowKey="a">
          <i>art</i>
        </TrackTime>
      );
      expect(read).toHaveBeenCalledOnce();
      act(() => {
        state = 'hidden';
        document.dispatchEvent(new Event('visibilitychange'));
      });
      await act(async () => vi.advanceTimersByTimeAsync(60000));
      expect(read).toHaveBeenCalledOnce();
      act(() => {
        state = 'visible';
        document.dispatchEvent(new Event('visibilitychange'));
      });
      expect(read).toHaveBeenCalledTimes(2);
    } finally {
      state = 'visible';
      vi.useRealTimers();
    }
  });
});
