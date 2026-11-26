// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useCallback, useRef } from 'react';
import { getNowPlaying, getVolume } from '../../lib/api';
import { PRESET_COUNT } from '../../lib/limits';
import type { NowPlaying, Preset } from '../../lib/types';
import { useSpeakerResource } from './resource';
import { refreshResource } from './store';

type Timer = ReturnType<typeof setTimeout>;

// Empty slots until the speaker has answered. One array, so the player's memo holds.
const NO_PRESETS: (Preset | null)[] = new Array<Preset | null>(PRESET_COUNT).fill(null);

export interface SpeakerReads {
  presets: (Preset | null)[];
  refresh: () => Promise<boolean>;
  refreshPresets: () => Promise<boolean>;
  discardInFlight: () => void;
  nudge: () => void;
}

// The reads side of useSpeaker: the now-playing + volume read, guarded against answers that
// arrive late for a speaker or a newer read, and the presets, which the page holds once per
// speaker (store.ts) so the settings page's alarm sees the same slots.
export function useSpeakerReads(
  speaker: string,
  applyState: (np: NowPlaying | null | undefined, vol: number | null | undefined) => void
): SpeakerReads {
  const presets = useSpeakerResource('presets', speaker) ?? NO_PRESETS;
  // Monotonic id per refresh, so a slow response can't clobber a newer one
  // (refresh is fired concurrently by the poll, nudge timers and action handlers).
  const refreshSeqRef = useRef(0);
  // The speaker on screen now. A refresh kept by a timer from before a room switch (a
  // stop's re-read) belongs to the room left behind: it must neither ask nor land.
  const speakerRef = useRef(speaker);
  useEffect(() => {
    speakerRef.current = speaker;
  }, [speaker]);
  const refresh = useCallback(async () => {
    if (speakerRef.current !== speaker) return true; // not a miss: just not ours any more
    refreshSeqRef.current += 1;
    const seq = refreshSeqRef.current;
    const [np, vol] = await Promise.all([getNowPlaying(speaker), getVolume(speaker)]);
    // A newer refresh in flight, or a room switch since: the answer belongs elsewhere.
    if (seq === refreshSeqRef.current && speakerRef.current === speaker) applyState(np, vol);
    return np !== null;
  }, [applyState, speaker]);
  // An SSE push is the freshest truth: bump the refresh sequence to discard any slower
  // in-flight poll response before folding it in.
  const discardInFlight = useCallback(() => {
    refreshSeqRef.current += 1;
  }, []);

  // A failed read keeps the slots on screen and says so, so the caller can retry. The
  // answer lands under the speaker it was asked of, never under the next one.
  const refreshPresets = useCallback(async () => {
    if (speakerRef.current !== speaker) return true;
    return refreshResource('presets', speaker);
  }, [speaker]);

  // Refresh now-playing a few times after an action so the optimistic state
  // converges to the real one (wake + buffering can take a moment). A new nudge
  // supersedes the previous one's remaining timers.
  const nudgeTimersRef = useRef<Timer[]>([]);
  const nudge = useCallback(() => {
    nudgeTimersRef.current.forEach(clearTimeout);
    nudgeTimersRef.current = [500, 1200, 2500, 4500, 7000, 10000, 13000].map((ms) =>
      setTimeout(refresh, ms)
    );
  }, [refresh]);
  // Clear outstanding nudge timers on unmount and on a speaker switch: they hold the
  // refresh of the old speaker, which would land its state under the new one.
  useEffect(() => () => nudgeTimersRef.current.forEach(clearTimeout), [speaker]);

  return { presets, refresh, refreshPresets, discardInFlight, nudge };
}
