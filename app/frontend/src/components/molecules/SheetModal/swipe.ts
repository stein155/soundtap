// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// How far (px), or how fast (px/ms) at the end, a finger must pull a layer away to close it.
const closeDistance = 110;
const closeSpeed = 0.5;

type Drag = {
  layer: HTMLElement;
  x: number;
  y: number;
  // the last move, for the speed at release
  at: number;
  last: number;
  speed: number;
  on?: boolean;
};

// How far the finger moved toward closing, and across that.
const away = (t: Touch, d: Drag, drawer: boolean) => (drawer ? d.x - t.clientX : t.clientY - d.y);
const across = (t: Touch, d: Drag, drawer: boolean) => (drawer ? t.clientY - d.y : t.clientX - d.x);

function put(layer: HTMLElement, px: number, drawer: boolean) {
  layer.style.transform = px ? `translate${drawer ? 'X' : 'Y'}(${drawer ? -px : px}px)` : '';
  const size = drawer ? layer.offsetWidth : layer.offsetHeight;
  const overlay = layer.closest<HTMLElement>('.overlay');
  if (overlay) {
    // the overlay's own rgba(0, 0, 0, 0.35), lighter the further the layer is pulled
    overlay.style.backgroundColor =
      px && size ? `rgba(0, 0, 0, ${(0.35 * Math.max(0, 1 - px / size)).toFixed(3)})` : '';
  }
}

// grab starts a drag on one finger on the layer, unless it is in a text field or, for a
// sheet, in a list scrolled away from its top.
function grab(e: TouchEvent, drawer: boolean): Drag | null {
  const t = e.target as HTMLElement;
  const layer = t.closest<HTMLElement>(drawer ? '.drawer' : '.sheet');
  if (!layer || e.touches.length !== 1 || t.closest('input:not([type="range"]), textarea')) {
    return null;
  }
  if (!drawer) {
    for (let n: HTMLElement | null = t; n && n !== layer; n = n.parentElement) {
      if (n.scrollTop > 0) return null;
    }
  }
  const p = e.touches[0];
  return { layer, x: p.clientX, y: p.clientY, at: e.timeStamp, last: 0, speed: 0 };
}

// follow moves the layer with the finger; it gives the drag up (null) when the finger
// went across instead.
function follow(e: TouchEvent, d: Drag, drawer: boolean): Drag | null {
  const t = e.touches[0];
  const a = away(t, d, drawer);
  if (d.on === undefined) {
    const b = across(t, d, drawer);
    // Claim a move toward closing at once: once a list under it starts to scroll,
    // Safari no longer lets its moves be cancelled. A tap's jitter stays a tap.
    if (a > 2 && a >= Math.abs(b) && e.cancelable) e.preventDefault();
    if (Math.abs(a) < 8 && Math.abs(b) < 8) return d;
    d.on = a > Math.abs(b);
    if (!d.on) return null;
    d.layer.style.transition = 'none';
  }
  e.preventDefault();
  const dt = e.timeStamp - d.at;
  if (dt > 0) d.speed = (a - d.last) / dt;
  d.at = e.timeStamp;
  d.last = a;
  put(d.layer, Math.max(0, a), drawer);
  return d;
}

// release says whether a let-go closes the layer; if not, the layer springs back.
function release(e: TouchEvent, g: Drag, drawer: boolean): boolean {
  const a = away(e.changedTouches[0], g, drawer);
  const fresh = e.timeStamp - g.at < 100; // a finger that stopped before lifting is no fling
  if (e.type !== 'touchcancel' && (a > closeDistance || (a > 30 && fresh && g.speed > closeSpeed)))
    return true;
  g.layer.style.transition = 'transform 0.22s var(--ease)';
  put(g.layer, 0, drawer);
  return false;
}

// swipeToClose lets a finger on el drag a sheet down, or the drawer to the left, and
// calls close when it lets go far or fast enough. It returns the cleanup.
export function swipeToClose(el: HTMLElement, drawer: boolean, close: () => void) {
  let d: Drag | null = null;
  const start = (e: TouchEvent) => {
    d = grab(e, drawer);
  };
  const move = (e: TouchEvent) => {
    if (d) d = follow(e, d, drawer);
  };
  const end = (e: TouchEvent) => {
    const g = d;
    d = null;
    if (g?.on && release(e, g, drawer)) close();
  };
  el.addEventListener('touchstart', start, { passive: true });
  el.addEventListener('touchmove', move, { passive: false });
  el.addEventListener('touchend', end);
  el.addEventListener('touchcancel', end);
  return () => {
    el.removeEventListener('touchstart', start);
    el.removeEventListener('touchmove', move);
    el.removeEventListener('touchend', end);
    el.removeEventListener('touchcancel', end);
  };
}
