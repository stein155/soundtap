// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useEffectEvent, useLayoutEffect } from 'react';

// Overlays stack (now playing, then the speakers sheet over it), and Escape should
// leave only the top one. Each active overlay registers here; the last one wins.
//
// The phone's back button and back gesture do the same as Escape: while overlays are
// open the app holds one history entry, so "back" pops that entry instead of leaving the app.
// An overlay that closes keeps the entry for the next one: giving it back races with the next
// overlay and can step out of the app. The cost is one back press that does nothing
// after an overlay closed by its own button.
interface Entry {
  run: () => void;
}
const stack: Entry[] = [];
// Installed once and kept: the entry outlives the overlay that took it.
let listening = false;
const onOurEntry = () => {
  const state: unknown = window.history.state;
  return (
    typeof state === 'object' &&
    state !== null &&
    'soundtapOverlay' in state &&
    state.soundtapOverlay === true
  );
};
// Whether the history stands on our entry; a reload keeps standing on it.
let guarded = onOurEntry();

function guard() {
  if (guarded) return;
  window.history.pushState({ soundtapOverlay: true }, '');
  guarded = true;
}

function onKeyDown(e: KeyboardEvent) {
  const top = stack.at(-1);
  if (e.key !== 'Escape' || !top) return;
  e.preventDefault();
  top.run();
}

function onPopState() {
  guarded = onOurEntry();
  stack[stack.length - 1]?.run();
  // Something is still open after its effects ran, so it needs an entry for the next "back".
  setTimeout(() => {
    if (stack.length) guard();
  }, 0);
}

function register(run: () => void) {
  const entry: Entry = { run };
  if (!listening) {
    listening = true;
    window.addEventListener('popstate', onPopState);
  }
  if (!stack.length) window.addEventListener('keydown', onKeyDown);
  stack.push(entry);
  guard();
  return () => {
    stack.splice(stack.indexOf(entry), 1);
    if (!stack.length) window.removeEventListener('keydown', onKeyDown);
  };
}

// Calls onEscape when Escape (or the device's back) is pressed while `active` and nothing
// opened later is. A `layer` holds other users of this hook: it registers first even when
// they open with it, as effects run children first.
export function useEscape(active: boolean, onEscape: () => void, layer = false): void {
  const handle = useEffectEvent(onEscape);
  useLayoutEffect(() => {
    return active && layer ? register(() => handle()) : undefined;
  }, [active, layer]);
  useEffect(() => {
    return active && !layer ? register(() => handle()) : undefined;
  }, [active, layer]);
}
