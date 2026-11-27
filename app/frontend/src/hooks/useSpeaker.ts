// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState, useEffect, useCallback, useMemo } from 'react';
import { derivePlayer } from '../lib/speakerPlayer';
import type { NowPlaying, Player, PlayTarget, Preset } from '../lib/types';
import type { Pending } from '../lib/speakerPlayer';
import { useSpeakerFeed } from './speaker/feed';
import { useOptimisticPlay } from './speaker/play';
import { useSpeakerReads } from './speaker/reads';
import { useSpeakerVolume } from './speaker/volume';

// Live state for one speaker, the selected room, through its /api/* endpoints.
//
// After a tap the speaker passes through STANDBY or INVALID and BUFFERING_STATE before
// PLAY_STATE, and during a switch it may report the previous station as playing for a
// moment. The station picked stays on screen as a "pending" target until the speaker plays
// it or the pending window runs out. Components read a derived { status, station }, not the
// raw playStatus, so starting, buffering and live each show once, in that order.
//
// The pieces live in speaker/: play.ts (the pending target and now-playing), volume.ts,
// reads.ts (the reads and their staleness guards) and feed.ts (SSE push + poll);
// lib/speakerPlayer holds the pure rules. The speaker's slower reads (settings, version,
// presets, Bluetooth) are held once per page in speaker/store.ts, which useSpeakerInfo,
// useSpeakerSettings and this hook's presets share.

export interface SpeakerLive {
  player: Player;
  nowPlaying: NowPlaying | null;
  volume: number;
  presets: (Preset | null)[];
  loading: boolean;
  refreshPresets: () => Promise<boolean>;
  volumeFailures: number;
  unreachable: boolean;
  refreshNowPlaying: () => Promise<boolean>;
  changeVolume: (level: number, options?: { group?: boolean }) => void;
  // The slider shows level without sending it: the mute sets the speaker's state itself.
  showVolume: (level: number) => void;
  playOptimistic: (station: PlayTarget | null) => Pending | null;
  stopOptimistic: () => void;
  releaseStopHold: () => void;
  cancelPending: (which?: Pending | null) => void;
  gaveUp: Pending | null;
  nudge: () => void;
}

// `speaker` is the one to follow ('' = the one serving the page). Switching it starts the
// hook over against the new speaker: state is dropped rather than carried across, so one
// room's station can never flash up under another room's name.
export function useSpeaker(speaker = ''): SpeakerLive {
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
    if (volumeFailures === 0) return;
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
    showVolume: vol.showVolume,
    playOptimistic: play.playOptimistic,
    stopOptimistic: play.stopOptimistic,
    releaseStopHold,
    cancelPending: play.cancelPending,
    gaveUp: play.gaveUp,
    nudge: reads.nudge,
  };
}
