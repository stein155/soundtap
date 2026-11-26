// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useCallback, useEffect, useRef } from 'react';

// A write sent once a slider rests, not on every step of a drag: each call replaces the one
// still waiting. One still waiting when the card goes (back to the list, another speaker)
// is sent then: dropping it lost the last move made before leaving.
export function useTrailingSend(delayMs: number): (send: () => void) => void {
  const waiting = useRef<{ id: number; send: () => void } | null>(null);
  useEffect(
    () => () => {
      const last = waiting.current;
      if (!last) return;
      waiting.current = null;
      window.clearTimeout(last.id);
      last.send();
    },
    []
  );
  return useCallback(
    (send: () => void) => {
      if (waiting.current) window.clearTimeout(waiting.current.id);
      const id = window.setTimeout(() => {
        waiting.current = null;
        send();
      }, delayMs);
      waiting.current = { id, send };
    },
    [delayMs]
  );
}
