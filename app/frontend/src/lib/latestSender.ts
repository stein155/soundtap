// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// A sender for values that change faster than a speaker can take them, like a volume
// slider being dragged: one request in flight at a time, and only the newest value
// queued behind it. Serialised (in order), coalesced (in-between values are skipped)
// and the final value is always sent. `onError` hears of a send that failed, so the
// caller can say so instead of the value silently snapping back later.
export function createLatestSender<T>(
  send: (value: T) => Promise<unknown>,
  onError?: (value: T) => void
) {
  let inflight = false;
  let next: { value: T } | null = null;
  return (value: T): void => {
    next = { value };
    if (inflight) return;
    inflight = true;
    void (async () => {
      while (next) {
        const { value: current } = next;
        next = null;
        // eslint-disable-next-line no-await-in-loop -- one request in flight at a time is the point
        await send(current).catch(() => onError?.(current));
      }
      inflight = false;
    })();
  };
}
