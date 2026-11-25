// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useRef } from 'react';
import type * as React from 'react';

const HOLD_MS = 550;

export interface LongPress {
  onKeyDown: (e: React.KeyboardEvent) => void;
  onPointerDown: () => void;
  onPointerUp: () => void;
  onPointerLeave: () => void;
  onPointerCancel: () => void;
  onClick: (e: React.MouseEvent) => void;
  onContextMenu: (e: React.MouseEvent) => void;
}

// Tap and long-press on one element. After HOLD_MS the hold fires (with a short buzz)
// and the click that ends the press is swallowed, so holding a preset to store a
// station never also plays it. Without onHold it is a plain tap.
export function useLongPress(onHold: (() => void) | null, onTap: () => void): LongPress {
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const held = useRef(false);
  useEffect(() => () => clearTimeout(timer.current), []);
  const start = () => {
    // A second finger restarts the one hold; it must not leave the first one's timer
    // running past every cancel.
    clearTimeout(timer.current);
    held.current = false;
    if (!onHold) return;
    timer.current = setTimeout(() => {
      held.current = true;
      onHold();
      if ('vibrate' in navigator) navigator.vibrate(20);
    }, HOLD_MS);
  };
  const cancel = () => clearTimeout(timer.current);
  // What a held pointer does, for a keyboard: the menu key or Shift+F10.
  const onKeyDown = (e: React.KeyboardEvent) => {
    if (onHold && (e.key === 'ContextMenu' || (e.shiftKey && e.key === 'F10'))) {
      e.preventDefault();
      onHold();
    }
  };
  return {
    onKeyDown,
    onPointerDown: start,
    onPointerUp: cancel,
    onPointerLeave: cancel,
    onPointerCancel: cancel,
    onClick: (e: React.MouseEvent) => {
      if (held.current) {
        e.preventDefault();
        held.current = false;
        return;
      }
      onTap();
    },
    onContextMenu: (e: React.MouseEvent) => {
      if (onHold) e.preventDefault();
    },
  };
}
