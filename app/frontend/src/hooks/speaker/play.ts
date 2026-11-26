// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState, useEffect, useCallback, useRef, type RefObject } from 'react';
import type { NowPlaying, PlayTarget } from '../../lib/types';
import {
  PENDING_MAX_MS,
  pendingOutcome,
  stopHold,
  type Pending,
  type Picked,
} from '../../lib/speakerPlayer';

export interface OptimisticPlay {
  nowPlaying: NowPlaying | null;
  pending: Pending | null;
  gaveUp: Pending | null;
  lastPicked: Picked | null;
  shownNameRef: RefObject<string>;
  applyNowPlaying: (np: NowPlaying | null | undefined) => void;
  playOptimistic: (station: PlayTarget | null) => Pending | null;
  stopOptimistic: () => void;
  cancelPending: (which?: Pending | null) => void;
  reset: () => void;
  releaseStopHold: () => void;
}

// The playback side of useSpeaker: the raw now-playing, and the optimistic target a tap
// holds on screen until the box actually plays it (see useSpeaker for why).
export function useOptimisticPlay(): OptimisticPlay {
  const [nowPlaying, setNowPlaying] = useState<NowPlaying | null>(null); // raw normalised speaker state
  const [pending, setPending] = useState<Pending | null>(null); // target after a tap
  const [gaveUp, setGaveUp] = useState<Pending | null>(null); // a target the box could not play
  // Identity (logo + TuneIn id) of the station we last picked. The speaker's
  // own now-playing reports tuneInId: null and often a blank art right after a
  // switch, so once pending clears we'd otherwise drop back to the initials
  // fallback until TuneIn enrichment warms up. We keep what we already knew and
  // backfill it for the same station so the logo never regresses.
  const [lastPicked, setLastPicked] = useState<Picked | null>(null);

  // Read by applyNowPlaying, which runs from timers and the SSE stream.
  const pendingRef = useRef<Pending | null>(null);
  useEffect(() => {
    pendingRef.current = pending;
  }, [pending]);
  // The hard cap also holds when no state read arrives at all: applyNowPlaying only
  // checks it on a successful read.
  useEffect(() => {
    if (!pending) return undefined;
    const timer = setTimeout(
      () => setPending((p) => (p === pending ? null : p)),
      Math.max(0, pending.since + PENDING_MAX_MS - Date.now())
    );
    return () => clearTimeout(timer);
  }, [pending]);
  // What is on screen now (real or pending), so a tap can remember which station
  // it switches away from.
  const shownNameRef = useRef<string>('');
  // Set on Stop: while it holds, ignore the box still reporting the old station
  // as playing (its state transitions lag, just like on start).
  const stopHoldRef = useRef(0);

  // Fold a fresh now-playing (from a poll or an SSE push) into local state: object /
  // {standby} / null-or-undefined when unknown. Both sources funnel through here so the
  // optimistic holds behave identically whichever delivered the update.
  const applyNowPlaying = useCallback((np: NowPlaying | null | undefined) => {
    // Right after Stop the box still reports the old station as PLAY_STATE for a
    // moment; ignore now-playing until it catches up or the hold expires, so the
    // stopped station doesn't pop back on screen.
    const { holding, release } = stopHold(stopHoldRef.current, np, Date.now());
    if (release) stopHoldRef.current = 0;
    if (np == null || holding) return;
    setNowPlaying(np);
    const p = pendingRef.current;
    if (!p) return;
    const { failed, clear } = pendingOutcome(np, p, Date.now());
    if (failed) setGaveUp(p);
    if (clear) setPending(null);
  }, []);

  // Optimistically show a station as starting the instant the user taps it, so the
  // UI reacts immediately and keeps that station on screen through wake + buffering.
  // station: { name, tuneInId?, logo? }.
  // Returns the pending target, so a failed request can withdraw its own and no other.
  const playOptimistic = useCallback((station: PlayTarget | null): Pending | null => {
    if (!station) return null;
    stopHoldRef.current = 0;
    setLastPicked({
      name: station.name || '',
      logo: station.logo || '',
      tuneInId: station.tuneInId || null,
    });
    const next = {
      name: station.name || '',
      tuneInId: station.tuneInId || null,
      logo: station.logo || '',
      since: Date.now(),
      prevName: shownNameRef.current, // station we're switching away from
    };
    setPending(next);
    return next;
  }, []);

  // Stop: drop any pending target and show idle right away (the box follows shortly).
  const stopOptimistic = useCallback(() => {
    stopHoldRef.current = Date.now();
    setPending(null);
    setNowPlaying({ standby: true });
  }, []);

  // Drop the pending target (e.g. when the play request itself failed), so the
  // UI stops showing "starting" for a station that was never asked to play. Given the
  // target a request set, only that one goes: a newer tap's target stays.
  const cancelPending = useCallback(
    (which?: Pending | null) => setPending((p) => (which === undefined || p === which ? null : p)),
    []
  );

  // A new speaker starts from nothing.
  const reset = useCallback(() => {
    setNowPlaying(null);
    setPending(null);
    setLastPicked(null);
    setGaveUp(null);
  }, []);
  const releaseStopHold = useCallback(() => {
    stopHoldRef.current = 0;
  }, []);

  return {
    nowPlaying,
    pending,
    gaveUp,
    lastPicked,
    shownNameRef,
    applyNowPlaying,
    playOptimistic,
    stopOptimistic,
    cancelPending,
    reset,
    releaseStopHold,
  };
}
