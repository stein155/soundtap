// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { afterEach, describe, expect, it, vi } from 'vitest';
import { exitDuration, flingEase, settleExit, takeOver, token } from './motion';

interface Fake {
  animationName: string;
  playState: string;
  effect: { target: Element; pseudoElement: string | null };
  commitStyles: ReturnType<typeof vi.fn>;
  cancel: ReturnType<typeof vi.fn>;
}

function anim(
  animationName: string,
  target: Element,
  pseudo: string | null = null,
  playState = 'running'
): Fake {
  const a: Fake = {
    animationName,
    playState,
    effect: { target, pseudoElement: pseudo },
    commitStyles: vi.fn(),
    cancel: vi.fn(() => {
      a.playState = 'idle';
    }),
  };
  return a;
}

// An overlay with a layer in it, whose animations (the overlay's own list holds its
// subtree's, pseudo-elements included) are the ones given.
function layers(cls: string, list: (overlay: HTMLElement, layer: HTMLElement) => Fake[]) {
  const overlay = document.createElement('div');
  overlay.className = 'overlay';
  const layer = document.createElement('div');
  layer.className = cls;
  overlay.append(layer);
  document.body.append(overlay);
  const all = list(overlay, layer);
  const of = (el: Element) => (opts?: { subtree?: boolean }) =>
    all.filter((a) => opts?.subtree === true || a.effect.target === el);
  Object.assign(overlay, { getAnimations: of(overlay) });
  Object.assign(layer, { getAnimations: of(layer) });
  return { overlay, layer, all };
}

function computed(values: { opacity?: string; transform?: string }) {
  vi.stubGlobal('getComputedStyle', () => ({ ...values, getPropertyValue: () => '' }));
}

afterEach(() => {
  vi.unstubAllGlobals();
  document.body.innerHTML = '';
});

describe('settleExit', () => {
  // Closed while coming in: the exit goes on from where the entrance got to.
  it('freezes a layer and its dim still coming in, and leaves their exits running', () => {
    computed({ opacity: '0.42' });
    const { overlay, all } = layers('sheet', (o, l) => [
      anim('dim-in', o, '::before'),
      anim('dim-out', o, '::before'),
      anim('slide-in', l),
      anim('slide-out', l),
    ]);
    const [dimIn, dimOut, slideIn, slideOut] = all;
    settleExit(overlay);
    expect(overlay.style.getPropertyValue('--pull')).toBe('0.420');
    expect(dimIn?.cancel).toHaveBeenCalled();
    expect(slideIn?.commitStyles).toHaveBeenCalled();
    expect(slideIn?.cancel).toHaveBeenCalled();
    expect(dimOut?.cancel).not.toHaveBeenCalled();
    expect(slideOut?.cancel).not.toHaveBeenCalled();
  });

  // A swipe's --pull stays: the exit fades on from how far the finger lightened it.
  it('leaves an entrance that is over, and a swipe-given --pull, alone', () => {
    computed({ opacity: '1' });
    const { overlay, all } = layers('drawer', (o, l) => [
      anim('dim-in', o, '::before', 'finished'),
      anim('slide-left-in', l, null, 'finished'),
    ]);
    overlay.style.setProperty('--pull', '0.600');
    settleExit(overlay);
    expect(overlay.style.getPropertyValue('--pull')).toBe('0.600');
    for (const a of all) expect(a.cancel).not.toHaveBeenCalled();
  });
});

describe('takeOver', () => {
  it('ends a sheet still coming in and says how far it still was from open', () => {
    computed({ transform: 'matrix(1, 0, 0, 1, 0, 300)' });
    const { layer, all } = layers('sheet', (o, l) => [
      anim('dim-in', o, '::before'),
      anim('slide-in', l),
    ]);
    expect(takeOver(layer, false)).toBe(300);
    for (const a of all) expect(a.cancel).toHaveBeenCalled();
  });

  it('measures the drawer to the left, and an open layer from zero', () => {
    computed({ transform: 'matrix(1, 0, 0, 1, -150, 0)' });
    const drawer = layers('drawer', (_o, l) => [anim('slide-left-in', l)]);
    expect(takeOver(drawer.layer, true)).toBe(150);
    const open = layers('sheet', () => []);
    expect(takeOver(open.layer, false)).toBe(0);
  });
});

describe('flingEase', () => {
  it('starts at the finger speed, never slower than evenly, at most four times', () => {
    expect(flingEase(0, 400, 220)).toBe('cubic-bezier(0.25, 0.250, 0.5, 1)');
    // 2 px/ms over 400 px in 220 ms: 1.1 times the even pace
    expect(flingEase(2, 400, 220)).toBe('cubic-bezier(0.25, 0.275, 0.5, 1)');
    expect(flingEase(20, 100, 220)).toBe('cubic-bezier(0.25, 1.000, 0.5, 1)');
    expect(flingEase(2, 0, 220)).toBe('cubic-bezier(0.25, 0.250, 0.5, 1)');
  });
});

describe('exitDuration', () => {
  const tokens = (value: string) => {
    vi.stubGlobal('getComputedStyle', () => ({
      getPropertyValue: (name: string) => (name === '--dur-exit' ? value : ''),
    }));
  };
  const reduced = (on: boolean) => {
    vi.stubGlobal('matchMedia', (query: string) => ({ matches: on && query.includes('reduce') }));
  };

  it('reads --dur-exit in ms or s', () => {
    reduced(false);
    tokens(' 220ms');
    expect(exitDuration()).toBe(220);
    tokens('0.3s');
    expect(exitDuration()).toBe(300);
    expect(token('--dur-exit')).toBe(300);
  });

  it('waits not at all with less motion, or without the token', () => {
    reduced(true);
    tokens('220ms');
    expect(exitDuration()).toBe(0);
    reduced(false);
    tokens('');
    expect(exitDuration()).toBe(0);
  });
});
