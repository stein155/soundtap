// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useCallback, useLayoutEffect, useRef, type RefObject } from 'react';

const RESTORE_FOR_MS = 3000;

// The scroll position of a scroller per `key`. On a change of key the new key's
// saved position comes back, or the top for one never seen. A level that is still
// loading has no height to scroll to yet, so the restore waits (briefly) for its rows.
export function useScrollMemory(
  key: string | undefined
): readonly [RefObject<HTMLDivElement | null>, () => void] {
  const ref = useRef<HTMLDivElement | null>(null);
  const saved = useRef(new Map<string, number>());
  const current = useRef(key);
  const onScroll = useCallback(() => {
    const el = ref.current;
    if (el && current.current !== undefined) saved.current.set(current.current, el.scrollTop);
  }, []);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el || key === current.current) return;
    current.current = key;
    const target = key === undefined ? 0 : (saved.current.get(key) ?? 0);
    el.scrollTop = target;
    if (el.scrollTop >= target) return;
    const until = Date.now() + RESTORE_FOR_MS;
    const retry = new MutationObserver(() => {
      el.scrollTop = target;
      if (el.scrollTop >= target || Date.now() > until) retry.disconnect();
    });
    retry.observe(el, { childList: true, subtree: true });
    return () => retry.disconnect();
  }, [key]);
  return [ref, onScroll] as const;
}
