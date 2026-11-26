// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Live speaker state over Server-Sent Events (SoundTap's /api/events).
//
// The server pushes a { now, volume } snapshot the instant playback or volume
// changes, so the UI reacts immediately instead of waiting for the next poll.
// EventSource reconnects on its own after a dropped connection (honouring the server's
// `retry:` hint), so onError marks us offline; useSpeaker then falls back to polling
// until the stream comes back. An answer other than 200 (a 502 while a peer reboots, a
// 503 when the speaker has no subscriber slot left) closes the stream for good, so
// that case reopens it here.

import { normalizeNowPlaying } from './api/playback';
import { apiURL } from './base';
import type { NowPlaying } from './types';

// An event's JSON object, or null when it is no JSON or no object: such an event is skipped.
function eventObject(e: MessageEvent): object | null {
  try {
    const parsed: unknown = JSON.parse(String(e.data));
    return typeof parsed === 'object' && parsed !== null ? parsed : null;
  } catch {
    return null;
  }
}

// A reported level, which a muted speaker shows as silence.
function levelOf(volume: unknown, muted: unknown): number | undefined {
  if (typeof volume !== 'number') return undefined;
  return muted === true ? 0 : volume;
}

// The server pings every 5 s; three missed beats is a dead stream.
const STALE_MS = 15000;
const STALE_RETRY_MS = 3000;

interface StateHandlers {
  onState?: (s: {
    now?: NowPlaying | undefined;
    volume?: number | undefined;
    presets?: string | undefined;
  }) => void;
  onOpen?: (() => void) | undefined;
  onError?: (() => void) | undefined;
}

// watched opens an EventSource on url and keeps it honest. The server sends a ping every
// few seconds. A stream that stays quiet longer than that is dead even if the browser has
// not noticed (a rebooting speaker black-holes the connection): drop it, report it, and
// open a fresh one. Returns the close function.
function watched(
  url: string,
  { onOpen, onError }: { onOpen?: (() => void) | undefined; onError?: (() => void) | undefined },
  events: Record<string, (e: MessageEvent) => void>
): () => void {
  let es: EventSource;
  let watchdog: ReturnType<typeof setTimeout> | undefined;
  let reopen: ReturnType<typeof setTimeout> | undefined;
  let closed = false;
  const alive = () => {
    clearTimeout(watchdog);
    watchdog = setTimeout(() => {
      es.close();
      onError?.();
      clearTimeout(reopen);
      reopen = setTimeout(open, STALE_RETRY_MS);
    }, STALE_MS);
  };
  function open() {
    if (closed) return;
    const source = new EventSource(url);
    es = source;
    // A callback already queued when this stream was closed or replaced belongs to the
    // speaker or connection before: it must not reach the one shown now.
    const current = () => !closed && source === es;
    es.onopen = () => {
      if (!current()) return;
      alive();
      onOpen?.();
    };
    es.onerror = () => {
      if (!current()) return;
      onError?.();
      // A lost connection is retried by the browser itself (readyState CONNECTING). A
      // non-200 answer leaves the stream CLOSED, and the browser never tries again.
      if (source.readyState !== EventSource.CLOSED) return;
      clearTimeout(watchdog);
      clearTimeout(reopen);
      reopen = setTimeout(open, STALE_RETRY_MS);
    };
    es.addEventListener('ping', () => {
      if (current()) alive();
    });
    for (const [name, fn] of Object.entries(events)) {
      es.addEventListener(name, (e) => {
        if (!current()) return;
        alive();
        fn(e);
      });
    }
  }
  open();
  return () => {
    closed = true;
    clearTimeout(watchdog);
    clearTimeout(reopen);
    es.close();
  };
}

// subscribeState opens the stream and returns an unsubscribe function.
//   now    — normalised now-playing, or undefined when this push didn't include it.
//   volume — a number (0 while the speaker is muted), or undefined when this push didn't include it.
//   presets — a revision that changes whenever a preset slot does.
export function subscribeState(
  { onState, onOpen, onError }: StateHandlers = {},
  speaker?: string
): () => void {
  if (typeof EventSource === 'undefined') {
    return () => {
      // No SSE support: nothing to close, the caller keeps polling.
    };
  }
  return watched(
    apiURL('/api/events', speaker),
    { onOpen, onError },
    {
      state: (e) => {
        const parsed = eventObject(e);
        if (!parsed) return;
        const data: { now?: unknown; volume?: unknown; muted?: unknown; presets?: unknown } =
          parsed;
        onState?.({
          now: 'now' in data ? (normalizeNowPlaying(data.now) ?? undefined) : undefined,
          volume: levelOf(data.volume, data.muted),
          presets: typeof data.presets === 'string' ? data.presets : undefined,
        });
      },
    }
  );
}

// subscribeHousehold follows every speaker at once over /api/household/events: the
// serving speaker reads its peers' streams and tags each push with the peer's id, or
// with self for itself. One connection for the whole home screen, where a stream per
// room would use up the browser's six connections to this origin.
//   followers — the device ids playing along with that speaker, when it reports them.
//   devices   — a revision of that speaker's device list; it changes when /api/devices does.
export interface HouseholdState {
  now?: NowPlaying | undefined;
  volume?: number | undefined;
  followers?: string[] | undefined;
  devices?: string | undefined;
}

export function subscribeHousehold({
  onState,
  onOpen,
  onError,
}: {
  onState: (who: { id?: string | undefined; self: boolean }, s: HouseholdState) => void;
  onOpen?: (() => void) | undefined;
  onError?: (() => void) | undefined;
}): () => void {
  if (typeof EventSource === 'undefined') {
    return () => {
      // No SSE support: nothing to close, the caller keeps polling.
    };
  }
  return watched(
    apiURL('/api/household/events', ''),
    { onOpen, onError },
    {
      state: (e) => {
        const parsed = eventObject(e);
        if (!parsed) return;
        const data: { id?: unknown; self?: unknown; state?: unknown } = parsed;
        const s: {
          now?: unknown;
          volume?: unknown;
          muted?: unknown;
          followers?: unknown;
          devices?: unknown;
        } = typeof data.state === 'object' && data.state !== null ? data.state : {};
        onState(
          { id: typeof data.id === 'string' ? data.id : undefined, self: data.self === true },
          {
            now: 'now' in s ? (normalizeNowPlaying(s.now) ?? undefined) : undefined,
            volume: levelOf(s.volume, s.muted),
            followers: Array.isArray(s.followers)
              ? s.followers.filter((f): f is string => typeof f === 'string')
              : undefined,
            devices: typeof s.devices === 'string' ? s.devices : undefined,
          }
        );
      },
    }
  );
}
