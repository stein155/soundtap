// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { flingEase, takeOver, token } from './motion';

// How far (px), or how fast (px/ms) at the end, a finger must pull a layer away to close it.
const closeDistance = 110;
const closeSpeed = 0.5;

interface Drag {
  layer: HTMLElement;
  x: number;
  y: number;
  // the last move, for the speed at release
  at: number;
  last: number;
  speed: number;
  on?: boolean;
  // how far the layer still was from open when the finger took it over mid-entrance
  base: number;
}

// How far the finger moved toward closing, and across that.
const away = (t: Touch, d: Drag, drawer: boolean) => (drawer ? d.x - t.clientX : t.clientY - d.y);
const across = (t: Touch, d: Drag, drawer: boolean) => (drawer ? t.clientY - d.y : t.clientX - d.x);

function put(layer: HTMLElement, px: number, drawer: boolean) {
  layer.style.transform = px === 0 ? '' : `translate${drawer ? 'X' : 'Y'}(${drawer ? -px : px}px)`;
  const size = drawer ? layer.offsetWidth : layer.offsetHeight;
  const overlay = layer.closest<HTMLElement>('.overlay');
  if (!overlay) return;
  // The dim (the overlay's ::before) lighter the further the layer is pulled. An exit
  // fades it on from there, and slides the layer on from its offset: the exit keyframes
  // have no start of their own, so they start from these inline values (home.css).
  if (px !== 0 && size !== 0) {
    overlay.style.setProperty('--pull', Math.max(0, 1 - px / size).toFixed(3));
  } else {
    overlay.style.removeProperty('--pull');
  }
}

// pulling marks the overlay while a finger holds its layer: the dim follows the finger
// then, and eases back with the layer after.
function pulling(layer: HTMLElement, on: boolean) {
  const overlay = layer.closest<HTMLElement>('.overlay');
  if (on) overlay?.setAttribute('data-pulling', '');
  else overlay?.removeAttribute('data-pulling');
  layer.style.willChange = on ? 'transform' : '';
}

// grab starts a drag on one finger on the layer, unless it is in a text field or, for a
// sheet, in a list scrolled away from its top.
function grab(e: TouchEvent, drawer: boolean): Drag | null {
  const t = e.target;
  if (!(t instanceof Element)) return null;
  const layer = t.closest<HTMLElement>(drawer ? '.drawer' : '.sheet');
  if (!layer || e.touches.length !== 1 || t.closest('input:not([type="range"]), textarea')) {
    return null;
  }
  if (!drawer) {
    for (let n: Element | null = t; n && n !== layer; n = n.parentElement) {
      if (n.scrollTop > 0) return null;
    }
  }
  const p = e.touches[0];
  if (!p) return null;
  return { layer, x: p.clientX, y: p.clientY, at: e.timeStamp, last: 0, speed: 0, base: 0 };
}

// follow moves the layer with the finger; it gives the drag up (null) when the finger
// went across instead.
function follow(e: TouchEvent, d: Drag, drawer: boolean): Drag | null {
  const t = e.touches[0];
  if (!t) return d;
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
    // A layer still coming in goes on from where its entrance got to.
    d.base = takeOver(d.layer, drawer);
    pulling(d.layer, true);
  }
  e.preventDefault();
  const dt = e.timeStamp - d.at;
  if (dt > 0) d.speed = (a - d.last) / dt;
  d.at = e.timeStamp;
  d.last = a;
  put(d.layer, Math.max(0, d.base + a), drawer);
  return d;
}

// springBack eases the layer, and its dim, back to open.
function springBack(layer: HTMLElement, drawer: boolean) {
  const overlay = layer.closest<HTMLElement>('.overlay');
  overlay?.removeAttribute('data-flung');
  overlay?.style.removeProperty('--ease-fling');
  layer.style.transition = 'transform var(--dur-base) var(--ease-enter)';
  put(layer, 0, drawer);
}

// release says whether a let-go closes the layer; if not, the layer springs back. A
// layer that closes stays where the finger left it, and its exit runs on from there, at
// the finger's speed (data-flung, home.css).
function release(e: TouchEvent, g: Drag, drawer: boolean): boolean {
  const lifted = e.changedTouches[0];
  const a = g.base + (lifted ? away(lifted, g, drawer) : 0);
  const fresh = e.timeStamp - g.at < 100; // a finger that stopped before lifting is no fling
  pulling(g.layer, false);
  if (
    e.type !== 'touchcancel' &&
    (a > closeDistance || (a > 30 && fresh && g.speed > closeSpeed))
  ) {
    const overlay = g.layer.closest<HTMLElement>('.overlay');
    if (overlay) {
      const size = drawer ? g.layer.offsetWidth : g.layer.offsetHeight;
      const speed = fresh ? g.speed : 0;
      overlay.style.setProperty('--ease-fling', flingEase(speed, size - a, token('--dur-exit')));
      overlay.setAttribute('data-flung', '');
    }
    return true;
  }
  springBack(g.layer, drawer);
  return false;
}

// After a let-go that asked to close: a layer its owner kept open (nothing started its
// exit by the next frame) springs back instead of staying where the finger left it.
function unlessClosed(layer: HTMLElement, drawer: boolean) {
  const check = () => {
    const overlay = layer.closest('.overlay');
    if (layer.isConnected && overlay && !overlay.hasAttribute('data-exiting')) {
      springBack(layer, drawer);
    }
  };
  window.requestAnimationFrame(check);
}

// swipeToClose lets a finger on el drag a sheet down, or the drawer to the left, and
// calls close when it lets go far or fast enough. It returns the cleanup.
export function swipeToClose(el: HTMLElement, drawer: boolean, close: () => void): () => void {
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
    if (g?.on === true && release(e, g, drawer)) {
      close();
      unlessClosed(g.layer, drawer);
    }
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
