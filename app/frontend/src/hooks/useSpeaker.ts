// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState, useEffect, useCallback, useMemo } from 'react';
import { derivePlayer } from '../lib/speakerPlayer';
import type { NowPlaying } from '../lib/types';
import { useSpeakerFeed } from './speaker/feed';
import { useOptimisticPlay } from './speaker/play';
import { useSpeakerReads } from './speaker/reads';
import { useSpeakerVolume } from './speaker/volume';

// Live state for one speaker -- the selected room -- through its /api/* endpoints.
//
// Playback runs through a small state machine so the UI never flickers while a
// station is starting. After a tap the box passes through STANDBY/INVALID then
// BUFFERING_STATE before it reaches PLAY_STATE, and during a switch it may even
// report the *previous* station as PLAY_STATE for a moment. To keep the picked
// station on screen we hold a "pending" target until the box actually plays it
// (or the pending window expires). Components consume a derived { status, station }
// instead of the raw playStatus, so "starten / bufferen / live" render cleanly.
//
// The pieces live in speaker/: play.ts (the pending target and now-playing), volume.ts,
// reads.ts (the reads and their staleness guards) and feed.ts (SSE push + poll);
// lib/speakerPlayer holds the pure rules. The speaker's slower reads (settings, version,
// presets, Bluetooth) are held once per page in speaker/store.ts, which useSpeakerInfo,
// useSpeakerSettings and this hook's presets share.

// `speaker` is the one to follow ('' = the one serving the page). Switching it starts the
// hook over against the new speaker: state is dropped rather than carried across, so one
// room's station can never flash up under another room's name.
export function useSpeaker(speaker = '') {
  const [loading, setLoading] = useState(true);
  const play = useOptimisticPlay();
  const vol = useSpeakerVolume(speaker);
  // Both sources of a reading (a poll, an SSE push) funnel through here so the optimistic
  // holds behave identically whichever delivered the update. vol is a number (or
  // null/undefined when unknown).
  const { applyNowPlaying } = play;
  const { applyVolume } = vol;
  const applyState = useCallback(
    (np: NowPlaying | null | undefined, v: number | null | undefined) => {
      applyNowPlaying(np);
      if (v != null) applyVolume(v);
    },
    [applyNowPlaying, applyVolume]
  );
  const reads = useSpeakerReads(speaker, applyState);
  const { refresh, refreshPresets } = reads;

  // Volume failed: the slider goes back to what the speaker says at once.
  const { volumeFailures, releaseHold } = vol;
  useEffect(() => {
    if (!volumeFailures) return;
    releaseHold();
    void refresh();
  }, [volumeFailures, releaseHold, refresh]);

  const { pending, nowPlaying, lastPicked, shownNameRef } = play;
  const player = useMemo(
    () => derivePlayer(pending, nowPlaying, reads.presets, lastPicked),
    [pending, nowPlaying, reads.presets, lastPicked]
  );
  // Read by the next tap.
  useEffect(() => {
    shownNameRef.current = player.station?.name || '';
  }, [player, shownNameRef]);

  // Initial load, and again whenever the speaker changes.
  const { releaseStopHold } = play;
  useEffect(() => {
    releaseStopHold();
    releaseHold();
    // Only this room's load may end its loading state: a slow load of the room left behind
    // must not clear the skeletons of the room now on screen.
    let current = true;
    void Promise.all([refresh(), refreshPresets()]).finally(() => {
      if (current) setLoading(false);
    });
    return () => {
      current = false;
    };
  }, [refresh, refreshPresets, releaseStopHold, releaseHold]);

  const { discardInFlight } = reads;
  const feed = useSpeakerFeed({
    speaker,
    status: player.status,
    applyState,
    refresh,
    refreshPresets,
    discardInFlight,
  });

  // A new speaker starts from nothing, reset while rendering so the old room's state
  // never paints under the new room's name.
  const [following, setFollowing] = useState(speaker);
  if (following !== speaker) {
    setFollowing(speaker);
    setLoading(true);
    play.reset();
    vol.resetVolume();
    feed.resetUnreachable();
  }

  return {
    player,
    nowPlaying,
    volume: vol.volume,
    presets: reads.presets,
    loading,
    refreshPresets,
    volumeFailures,
    unreachable: feed.unreachable,
    refreshNowPlaying: refresh,
    changeVolume: vol.changeVolume,
    playOptimistic: play.playOptimistic,
    stopOptimistic: play.stopOptimistic,
    releaseStopHold,
    cancelPending: play.cancelPending,
    gaveUp: play.gaveUp,
    nudge: reads.nudge,
  };
}
