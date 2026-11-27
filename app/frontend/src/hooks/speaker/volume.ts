// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState, useCallback, useRef, useMemo } from 'react';
import { setVolume } from '../../lib/api';
import { createLatestSender } from '../../lib/latestSender';

interface VolumeHold {
  value: number;
  until: number;
}

const VOLUME_HOLD_MS = 2500; // ignore polled volume this long after a local change

// A local volume change, held against polled readings until it has had time to apply.
const holdVolume = (value: number): VolumeHold => ({ value, until: Date.now() + VOLUME_HOLD_MS });

export interface SpeakerVolume {
  volume: number;
  volumeFailures: number;
  changeVolume: (level: number, options?: { group?: boolean }) => void;
  showVolume: (level: number) => void;
  applyVolume: (vol: number) => void;
  releaseHold: () => void;
  resetVolume: () => void;
}

// The volume side of useSpeaker: the slider's value, the local change held against stale
// readings, and the coalescing sender.
export function useSpeakerVolume(speaker: string): SpeakerVolume {
  const [volume, setVolumeState] = useState(20);
  // Volume the user just set locally, held for a moment so a poll that reads the
  // speaker's not-yet-applied volume can't yank the slider back. { value, until }.
  const volumeHoldRef = useRef<VolumeHold | null>(null);
  // Bumped when a volume change fails: the page reports it, and the slider goes back to
  // what the speaker says at once rather than when the hold runs out.
  // Counted per room: a late failure from the room left behind says nothing about this
  // one's hold. A room switched to starts at none, also one switched back to: its failure
  // was said when it happened.
  const [failed, setFailed] = useState({ speaker, count: 0 });
  const volumeFailures = failed.speaker === speaker ? failed.count : 0;
  // Dragging the slider fires an onChange per pixel. Sending a POST for each one
  // floods the speaker with out-of-order writes, so the box audibly steps and the
  // slider jumps as stale echoes land. Instead the slider moves at once (local) and a
  // coalescing sender sends one request at a time, always ending on the final value.
  // There is one sender per speaker, so a drag on the next room can never be sent to
  // the previous one while its last request is still in flight.
  const sendVolume = useMemo(
    () =>
      createLatestSender(
        async ({ volume: level, group }: { volume: number; group: boolean }) =>
          setVolume(level, speaker, { group }),
        () => setFailed((f) => ({ speaker, count: f.speaker === speaker ? f.count + 1 : 1 }))
      ),
    [speaker]
  );
  // `group` marks a change made to the whole group (see setVolume). A group level may lie
  // past 0..100: the speaker stops at the bound but passes the full step to its followers.
  const changeVolume = useCallback(
    (level: number, { group = false } = {}) => {
      const shown = Math.max(0, Math.min(100, level));
      // Held from the moment it changes; the sender only ever sends this latest value.
      volumeHoldRef.current = holdVolume(shown);
      setVolumeState(shown);
      sendVolume({ volume: level, group });
    },
    [sendVolume]
  );
  // Shows a level the speaker reaches by other means than a volume write (a mute), held
  // like a local change so a stale reading cannot undo it, but never sent.
  const showVolume = useCallback((level: number) => {
    volumeHoldRef.current = holdVolume(level);
    setVolumeState(level);
  }, []);
  // Fold in a volume reading. While a local change is held, only accept the speaker's value
  // once it has caught up to what we set (or the hold expires) — otherwise a stale read
  // would make the slider jump back.
  const applyVolume = useCallback((vol: number) => {
    const hold = volumeHoldRef.current;
    if (hold && Date.now() < hold.until && vol !== hold.value) return; // keep the optimistic value
    volumeHoldRef.current = null;
    setVolumeState(vol);
  }, []);
  const releaseHold = useCallback(() => {
    volumeHoldRef.current = null;
  }, []);
  const resetVolume = useCallback(() => {
    setVolumeState(20);
    setFailed({ speaker, count: 0 });
  }, [speaker]);
  return {
    volume,
    volumeFailures,
    changeVolume,
    showVolume,
    applyVolume,
    releaseHold,
    resetVolume,
  };
}
