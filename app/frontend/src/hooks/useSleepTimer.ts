// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useCallback, useEffect, useRef, useState } from 'react';
import { getSleep, setSleep, type SleepState } from '../lib/api';

const REREAD_MS = 30000;
// How soon a read comes again when no deadline says when: after a failed first read,
// or past the end of a timer only a read can clear, which must not go back to back.
const RETRY_MS = 5000;

export type SleepTimer = { minutes: number; endsAt: number } | null;

const toTimer = (state: SleepState | null): SleepTimer =>
  state?.endsAt ? { minutes: state.minutes, endsAt: Date.parse(state.endsAt) } : null;

// The selected speaker's sleep timer, read when the now-playing screen opens. The timer
// itself runs on the speaker, so it fires with the page closed.
export function useSleepTimer(speaker: string, visible: boolean, onFailed?: () => void) {
  // Kept with the speaker it was read from: after a switch, the previous room's countdown
  // is not shown under the new one while its own read is still out.
  const [held, setHeld] = useState<{ speaker: string; timer: SleepTimer }>({
    speaker,
    timer: null,
  });
  const setTimer = useCallback((next: SleepTimer) => setHeld({ speaker, timer: next }), [speaker]);
  const timer = held.speaker === speaker ? held.timer : null;
  // Bumped by a failed read, which re-arms the periodic read below.
  const [misses, setMisses] = useState(0);
  // Bumped by every change made here: a read sent before it must not undo it.
  const gen = useRef(0);
  // The speaker on screen now: a timer set on another one must not land here.
  const current = useRef(speaker);
  useEffect(() => {
    current.current = speaker;
    gen.current += 1; // whatever was in flight belongs to the speaker left behind
  }, [speaker]);
  const read = useCallback(
    (isCurrent: () => boolean) => {
      const g = gen.current;
      return getSleep(speaker).then((state) => {
        if (!isCurrent() || g !== gen.current) return;
        // null is a failed read, not a cancelled timer: keep what was last known and retry.
        if (state) {
          setTimer(toTimer(state));
          setMisses(0);
        } else setMisses((n) => n + 1);
      });
    },
    [speaker, setTimer]
  );
  useEffect(() => {
    if (!visible) return undefined;
    // A slow answer from the previous speaker must not land under the next one.
    let current = true;
    void read(() => current);
    return () => {
      current = false;
    };
  }, [visible, read]);
  // The speaker clears the timer when it fires or someone else cancels it, and another
  // phone can start one: read it again every half minute while shown, and at the end of a
  // running timer, or an expired timer reads 1 min. Every answer is a new held, which
  // schedules the next read.
  useEffect(() => {
    if (!visible) return undefined;
    let current = true;
    const untilEnd = timer ? timer.endsAt - Date.now() + 1000 : 0;
    let wait = misses ? RETRY_MS : REREAD_MS;
    if (timer) wait = untilEnd > 0 ? Math.min(REREAD_MS, untilEnd) : RETRY_MS;
    const id = setTimeout(() => read(() => current), wait);
    return () => {
      current = false;
      clearTimeout(id);
    };
  }, [visible, read, held, timer, misses]);
  const start = async (minutes: number) => {
    const g = ++gen.current;
    const target = speaker;
    const state = await setSleep(minutes, target).catch(() => {
      // Say so, and show what the speaker really has.
      onFailed?.();
      return getSleep(target).catch(() => null);
    });
    if (g !== gen.current || current.current !== target) return;
    // As with a read: no answer is not a cancelled timer.
    if (state) setTimer(toTimer(state));
    else setMisses((n) => n + 1);
  };
  // Powering off cancels a running timer on the speaker; show that at once.
  const clear = () => {
    gen.current += 1;
    setTimer(null);
  };
  return { timer, start, clear };
}
