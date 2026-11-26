// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Hand-overs between a layer's motions: a close or a finger that cuts across its entrance,
// and a swipe that lets go. The keyframes are in pages/HomePage/home.css, the durations in
// theme/GlobalStyle.ts.

// The entrances (slide-in, slide-left-in, fade-in, pop-in, dim-in) still under way on el
// itself, or on its pseudo-element.
function entrances(el: Element, pseudo: string | null): Animation[] {
  if (!('getAnimations' in el)) return []; // jsdom
  return el.getAnimations({ subtree: pseudo !== null }).filter((a) => {
    const name = 'animationName' in a && typeof a.animationName === 'string' ? a.animationName : '';
    const { effect } = a;
    if (!effect || !('target' in effect) || effect.target !== el) return false;
    const on = 'pseudoElement' in effect ? effect.pseudoElement : null;
    return (
      name.endsWith('-in') &&
      (a.playState === 'running' || a.playState === 'paused') &&
      (on ?? null) === pseudo
    );
  });
}

// freezeDim keeps the dim where its entrance got to, in --pull, and ends that entrance: the
// dim's exit (or a finger) goes on from there. A dim whose entrance is over is left alone,
// with any --pull a finger gave it.
function freezeDim(overlay: HTMLElement): void {
  const running = entrances(overlay, '::before');
  if (running.length === 0) return;
  const opacity = Number.parseFloat(getComputedStyle(overlay, '::before').opacity);
  if (Number.isFinite(opacity)) overlay.style.setProperty('--pull', opacity.toFixed(3));
  for (const a of running) a.cancel();
}

// freezeLayer keeps the layer where its entrance got to, in its own style, and ends that
// entrance.
function freezeLayer(layer: HTMLElement): void {
  for (const a of entrances(layer, null)) {
    try {
      a.commitStyles();
    } catch {
      // Not rendered: there is no position to keep.
    }
    a.cancel();
  }
}

// settleExit runs as an overlay starts to leave (SheetModal's onExit): a layer closed
// while it was still coming in leaves from where it was, instead of jumping to its open
// position first. The exits have no start keyframe of their own; home.css keeps the
// entrance running into the exit so it can still be read here.
export function settleExit(overlay: HTMLElement): void {
  freezeDim(overlay);
  const layer = overlay.querySelector<HTMLElement>('.sheet, .drawer');
  if (layer) freezeLayer(layer);
}

// The translation in a computed transform: matrix(a, b, c, d, e, f) or none.
function translation(transform: string): { x: number; y: number } {
  const m = /^matrix\((?<values>.+)\)$/u.exec(transform);
  const v = m?.groups?.['values']?.split(',').map(Number) ?? [];
  const x = v[4] ?? 0;
  const y = v[5] ?? 0;
  return { x: Number.isFinite(x) ? x : 0, y: Number.isFinite(y) ? y : 0 };
}

// takeOver hands a layer still coming in to a finger: it ends the entrance and returns how
// far (px) the layer still was from open, toward closing, so the drag goes on from there.
export function takeOver(layer: HTMLElement, drawer: boolean): number {
  const running = entrances(layer, null);
  const overlay = layer.closest<HTMLElement>('.overlay');
  if (overlay) {
    for (const a of entrances(overlay, '::before')) a.cancel();
  }
  if (running.length === 0) return 0;
  const { x, y } = translation(getComputedStyle(layer).transform);
  for (const a of running) a.cancel();
  return Math.max(0, drawer ? -x : y);
}

// A CSS time (220ms, 0.22s) in ms; 0 when it is not one.
function ms(value: string): number {
  const m = /^\s*(?<n>-?[\d.]+)(?<unit>ms|s)\s*$/u.exec(value);
  const n0 = m?.groups?.['n'];
  if (n0 === undefined) return 0;
  const n = Number.parseFloat(n0) * (m?.groups?.['unit'] === 's' ? 1000 : 1);
  return Number.isFinite(n) ? Math.max(0, n) : 0;
}

// The time token's value (theme/GlobalStyle.ts), in ms.
export function token(name: string, doc: Document = document): number {
  return ms(getComputedStyle(doc.documentElement).getPropertyValue(name));
}

// exitDuration is how long a layer takes to leave (--dur-exit): what waits for it waits that
// long, and not at all for someone who asked for less motion (the global rule ends it at once).
export function exitDuration(doc: Document = document): number {
  const win = doc.defaultView ?? window;
  if (win.matchMedia('(prefers-reduced-motion: reduce)').matches) return 0;
  return token('--dur-exit', doc);
}

// flingEase is the curve a swiped layer leaves on: it starts at the finger's speed (px/ms)
// over what is left of the way (px) in the exit's time (ms), and never slower than evenly,
// so the layer does not stall where it was let go. It settles into the edge.
export function flingEase(speed: number, remaining: number, duration: number): string {
  const raw = remaining > 0 && duration > 0 ? (Math.max(0, speed) * duration) / remaining : 1;
  const slope = Math.min(4, Math.max(1, raw));
  return `cubic-bezier(0.25, ${(0.25 * slope).toFixed(3)}, 0.5, 1)`;
}
