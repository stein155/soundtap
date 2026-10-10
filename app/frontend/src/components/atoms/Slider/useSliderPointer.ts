// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useRef } from 'react';
import type * as React from 'react';

export const clamp = (v: number, lo: number, hi: number) => Math.max(lo, Math.min(hi, v));
// How far a finger moves before it counts as a drag, and how far one tap moves the value.
const DRAG_SLOP = 6;
const TAP_STEP = 10;

interface Range {
  value: number;
  min: number;
  max: number;
  onChange: (value: number) => void;
}

type PointerHandler = (e: React.PointerEvent<HTMLDivElement>) => void;

// The slider's pointer handling: a mouse drag anywhere on the track (captured, so it keeps
// tracking outside the element), and a finger that only drags when it moves sideways.
export function useSliderPointer({ value, min, max, onChange }: Range) {
  const track = useRef<HTMLDivElement | null>(null);
  const span = Math.max(1, max - min);

  const valueAt = (clientX: number) => {
    const box = track.current?.getBoundingClientRect();
    if (!box?.width) return null;
    const ratio = clamp((clientX - box.left) / box.width, 0, 1);
    return clamp(Math.round(min + ratio * span), min, max);
  };
  const seek = (clientX: number) => {
    const next = valueAt(clientX);
    if (next !== null) onChange(next);
  };

  // A finger is not a mouse: on a touch screen the slider only follows a drag that
  // is clearly sideways, so a swipe that happens to start on it still scrolls the sheet,
  // and a tap nudges the level towards the finger instead of jumping there (a tap at the
  // far end of a volume slider used to go from 30 straight to 90).
  const touch = useRef<{ id: number; x: number; y: number; dragging: boolean } | null>(null);
  const onPointerDown: PointerHandler = (e) => {
    if (e.pointerType === 'touch' || e.pointerType === 'pen') {
      touch.current = { id: e.pointerId, x: e.clientX, y: e.clientY, dragging: false };
      return;
    }
    // jsdom has no pointer capture.
    if ('setPointerCapture' in e.currentTarget) e.currentTarget.setPointerCapture(e.pointerId);
    seek(e.clientX);
  };
  const onPointerMove: PointerHandler = (e) => {
    const t = touch.current;
    if (t && t.id === e.pointerId) {
      if (!t.dragging) {
        const dx = Math.abs(e.clientX - t.x);
        const dy = Math.abs(e.clientY - t.y);
        if (dy > DRAG_SLOP && dy >= dx) {
          touch.current = null; // a scroll: leave it to the page
          return;
        }
        if (dx <= DRAG_SLOP) return;
        t.dragging = true;
        if ('setPointerCapture' in e.currentTarget) e.currentTarget.setPointerCapture(e.pointerId);
      }
      seek(e.clientX);
      return;
    }
    if ('hasPointerCapture' in e.currentTarget && e.currentTarget.hasPointerCapture(e.pointerId))
      seek(e.clientX);
  };
  const onPointerUp: PointerHandler = (e) => {
    const t = touch.current;
    touch.current = null;
    if (!t || t.id !== e.pointerId || t.dragging) return;
    const target = valueAt(e.clientX);
    if (target !== null)
      onChange(clamp(value + clamp(target - value, -TAP_STEP, TAP_STEP), min, max));
  };
  const onPointerCancel = () => {
    touch.current = null;
  };

  return { track, handlers: { onPointerDown, onPointerMove, onPointerUp, onPointerCancel } };
}
