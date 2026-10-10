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
  const out = render(<TrackTime playing={false} nowKey="a" />);
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

  it('shows how far a track is', async () => {
    await showWith(180000);
    expect(screen.getByText('1:05')).toBeTruthy();
    expect(screen.getByText('3:00')).toBeTruthy();
  });

  it('shows nothing for a stream without an end', async () => {
    const { container } = await showWith(0);
    expect(container.firstChild).toBeNull();
  });
});
