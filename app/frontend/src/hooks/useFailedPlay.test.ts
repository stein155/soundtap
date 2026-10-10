// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, expect, it, vi } from 'vitest';
import { renderHook } from '@testing-library/react';
import { useFailedPlay } from './useFailedPlay';
import type { NowPlaying } from '../lib/types';

const now = (failed: boolean, stationName = 'A', playStatus = 'STOP_STATE') =>
  ({ standby: false, failed, stationName, playStatus }) as NowPlaying;

describe('useFailedPlay', () => {
  const start = (first: NowPlaying) => {
    const notify = vi.fn();
    const hook = renderHook(
      ({ speaker, np }: { speaker: string; np: NowPlaying }) => useFailedPlay(speaker, np, notify),
      { initialProps: { speaker: 'a', np: first } }
    );
    return { notify, ...hook };
  };

  it('says so once, however often the speaker retries', () => {
    const { notify, rerender } = start(now(false, 'A', 'BUFFERING_STATE'));
    rerender({ speaker: 'a', np: now(true) });
    rerender({ speaker: 'a', np: now(false, 'A', 'BUFFERING_STATE') });
    rerender({ speaker: 'a', np: now(true) });
    expect(notify).toHaveBeenCalledTimes(1);
  });

  it('says so again for the next selection, and after it played', () => {
    const { notify, rerender } = start(now(false));
    rerender({ speaker: 'a', np: now(true) });
    rerender({ speaker: 'a', np: now(true, 'B') });
    expect(notify).toHaveBeenCalledTimes(2);
    rerender({ speaker: 'a', np: now(false, 'B', 'PLAY_STATE') });
    rerender({ speaker: 'a', np: now(true, 'B') });
    expect(notify).toHaveBeenCalledTimes(3);
  });

  it('stays quiet about a failure it did not see happen', () => {
    const { notify, rerender } = start(now(true));
    rerender({ speaker: 'b', np: now(false) });
    rerender({ speaker: 'a', np: now(true) });
    expect(notify).not.toHaveBeenCalled();
  });
});
