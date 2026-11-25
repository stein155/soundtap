// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState, useEffect, useCallback, useRef, type RefObject } from 'react';
import { subscribeState } from '../../lib/events';
import { POLL_IDLE_MS, pollDelay } from '../../lib/speakerPlayer';
import type { NowPlaying, PlayerStatus } from '../../lib/types';
import { usePageVisible } from '../usePageVisible';

type Timer = ReturnType<typeof setTimeout>;

const UNREACHABLE_MISSES = 2;

interface Feed {
  speaker: string;
  status: PlayerStatus;
  applyState: (np: NowPlaying | null | undefined, vol: number | null | undefined) => void;
  refresh: () => Promise<boolean>;
  refreshPresets: () => Promise<boolean>;
  discardInFlight: () => void;
}

// What the push and the poll share.
interface Link {
  // Reads of now-playing that failed in a row. Two misses (or one with the live stream
  // down) mean the speaker this page talks to is gone (a reboot, an update, the phone off
  // its Wi-Fi): the page says so instead of showing the last state as if it were live.
  // Any answer clears it.
  missesRef: RefObject<number>;
  // Whether the SSE stream is live. While connected, state is pushed and polling
  // drops to a slow safety net; when the stream drops we resume adaptive polling.
  connectedRef: RefObject<boolean>;
  kickRef: RefObject<() => void>;
  setUnreachable: (unreachable: boolean) => void;
}

// Polling: adaptive (quick while starting / buffering) when the SSE stream is
// down; a slow safety net while it's live, in case a push is ever missed.
function usePoll(
  refresh: () => Promise<boolean>,
  status: PlayerStatus,
  visible: boolean,
  link: Link
) {
  const { missesRef, connectedRef, kickRef, setUnreachable } = link;
  // Read by the poll loop.
  const statusRef = useRef<PlayerStatus>('idle');
  useEffect(() => {
    statusRef.current = status;
  }, [status]);
  useEffect(() => {
    if (!visible) return;
    let timer: Timer;
    let cancelled = false;
    // One read at a time, and one timer: stream errors can come in a row, and each used to
    // start a loop of its own. One that comes during a read asks for one more.
    let reading = false;
    let again = false;
    // Schedule the next tick in a finally: a refresh that rejects (or is aborted by
    // its deadline while the speaker is offline) must not be able to kill the loop.
    const tick = async () => {
      reading = true;
      try {
        const answered = await refresh();
        missesRef.current = answered ? 0 : missesRef.current + 1;
        setUnreachable(missesRef.current >= UNREACHABLE_MISSES);
      } finally {
        reading = false;
        if (!cancelled) {
          const delay = pollDelay(connectedRef.current, statusRef.current, missesRef.current);
          timer = setTimeout(tick, again ? 0 : delay);
          again = false;
        }
      }
    };
    timer = setTimeout(tick, POLL_IDLE_MS);
    // The stream just dropped: poll now, not when the 30 s backstop comes round.
    kickRef.current = () => {
      if (reading) {
        again = true;
        return;
      }
      clearTimeout(timer);
      timer = setTimeout(tick, 0);
    };
    return () => {
      cancelled = true;
      clearTimeout(timer);
      kickRef.current = () => {
        // Stopped: a kick after cleanup has nothing to wake.
      };
    };
  }, [refresh, visible, missesRef, connectedRef, kickRef, setUnreachable]);
}

export interface SpeakerFeed {
  unreachable: boolean;
  resetUnreachable: () => void;
}

// The feed side of useSpeaker: the live SSE push, the poll that backs it up, and the
// unreachable verdict they share.
export function useSpeakerFeed({
  speaker,
  status,
  applyState,
  refresh,
  refreshPresets,
  discardInFlight,
}: Feed): SpeakerFeed {
  const visible = usePageVisible();
  const missesRef = useRef(0);
  const connectedRef = useRef(false);
  const kickRef = useRef<() => void>(() => {
    // Nothing to wake until the feed's effect has started.
  });
  const [unreachable, setUnreachable] = useState(false);
  // A new speaker starts without misses.
  useEffect(() => {
    missesRef.current = 0;
  }, [refresh]);

  // Live push: the server streams now-playing + volume the instant they change.
  useEffect(() => {
    // A hidden page holds no stream open; it reopens, and replays, when shown.
    if (!visible) return;
    // The presets revision last seen. A different one means a slot changed somewhere
    // else (the speaker's buttons, another phone, MQTT clients): read them again.
    // The stream replays the current revision on reconnect, so a change made while it
    // was down is caught too.
    let presetsRev: string | undefined;
    const unsubscribe = subscribeState(
      {
        onState: ({ now, volume, presets }) => {
          missesRef.current = 0;
          setUnreachable(false);
          discardInFlight();
          applyState(now, volume);
          if (presets === undefined) return;
          if (presetsRev === undefined || presets === presetsRev) {
            presetsRev = presets;
            return;
          }
          // The revision counts as seen only once the read worked: the next push retries.
          void refreshPresets().then((ok) => {
            if (ok) presetsRev = presets;
          });
        },
        onOpen: () => {
          connectedRef.current = true;
        },
        onError: () => {
          connectedRef.current = false;
          kickRef.current(); // find out now whether the speaker itself is gone
        },
      },
      speaker
    );
    return () => {
      unsubscribe();
      connectedRef.current = false;
    };
  }, [applyState, refreshPresets, discardInFlight, speaker, visible]);

  // Back on screen after being hidden: catch up at once rather than on the next tick.
  const wasHidden = useRef(false);
  useEffect(() => {
    if (!visible) {
      wasHidden.current = true;
      return;
    }
    if (!wasHidden.current) return;
    wasHidden.current = false;
    void refresh();
    void refreshPresets();
  }, [visible, refresh, refreshPresets]);

  usePoll(refresh, status, visible, { missesRef, connectedRef, kickRef, setUnreachable });
  const resetUnreachable = useCallback(() => setUnreachable(false), []);
  return { unreachable, resetUnreachable };
}
