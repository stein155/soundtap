// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { PREF, readPref, removePref, writePref } from './storage';

// The speaker this page drives. Empty is the one that served it; any other id is reached
// through that speaker's /api/devices/{id}/ proxy, so the browser still resolves only the
// one name it was opened on. Kept per browser: it is a viewer's choice, not a setting.
let device = readPref(PREF.device) ?? '';

export function targetDevice(): string {
  return device;
}

export function setTargetDevice(id: string): void {
  device = id;
  if (id) writePref(PREF.device, id);
  else removePref(PREF.device);
}

// followDevice points the page at the leader of the chosen room without remembering it:
// grouped elsewhere, the room's API calls must go to the leader, and unlinked again, back
// to the room itself.
export function followDevice(id: string): void {
  device = id;
}

// apiURL resolves an absolute-path API route. The API is always same-origin: soundtapd
// serves the UI and the API on :80. The device list itself always comes from the serving
// speaker: it is the one that knows the others.
//
// `speaker` names the speaker explicitly; left out, it is the one the page is pointed at. The
// home screen shows every speaker at once, so a room that is not the selected one still
// has to be stopped, started and turned up without first becoming the selected one.
export function apiURL(path: string, speaker: string = device): string {
  if (speaker && path.startsWith('/api/') && path !== '/api/devices') {
    return `/api/devices/${encodeURIComponent(speaker)}${path}`;
  }
  return path;
}

// One id per user action. iOS sends a write on a kept-alive connection the speaker
// may have dropped and fails it as a network error without retrying; we send it once more
// under the same id, and a speaker that did act on the first try answers from that one.
// Not crypto.randomUUID: a page on http://soundtap.local is no secure context.
const REQUEST_HEADER = 'X-SoundTap-Request';

function requestID(): string {
  return Math.random().toString(36).slice(2) + Date.now().toString(36);
}

// The speaker holds these until someone presses one of its keys (ADR 0055): they
// install firmware, wipe it, take it off the network or read its memory. Matches
// soundtapd's needsPress.
const PRESS_ROUTES = [
  // Updates the speaker fetches are signed and need no press, an older release included;
  // every install upload needs one, signed or not (ADR 0048).
  /^POST \/api\/(factory-reset|restore|reboot|wifi|setup|soundtapd|bco\/firmware|bluetooth\/(clear|forget))$/,
  /^POST \/api\/firmware\?(.*&)?install=1(&|$)/,
  /^DELETE \/api\/soundtapd$/,
  /^GET \/api\/debug(\/|\?|$)/,
];

// How long the speaker waits for that press (web.ConfirmWindow), and how long a request
// to such a route with little work after the press may therefore take before the answer.
export const PRESS_WINDOW_MS = 60000;
export const PRESS_TIMEOUT_MS = PRESS_WINDOW_MS + 15000;

export function needsPress(method: string, url: string): boolean {
  const path = url.replace(/^\/api\/devices\/[^/]+(?=\/api\/)/, '');
  return PRESS_ROUTES.some((re) => re.test(`${method.toUpperCase()} ${path}`));
}

let pressWaits = 0;
const pressListeners = new Set<(waiting: boolean) => void>();

// onPressWait tells fn whether a request is waiting for a key press on a speaker.
export function onPressWait(fn: (waiting: boolean) => void): () => void {
  pressListeners.add(fn);
  fn(pressWaits > 0);
  return () => {
    pressListeners.delete(fn);
  };
}

// whilePress runs a request and, if it needs a key press on the speaker, says so for the
// length of the speaker's window: after that it was confirmed or refused.
export async function whilePress<T>(
  method: string,
  url: string,
  run: () => Promise<T>
): Promise<T> {
  if (!needsPress(method, url)) return run();
  const notify = () => pressListeners.forEach((fn) => fn(pressWaits > 0));
  let open = true;
  const close = () => {
    if (!open) return;
    open = false;
    pressWaits--;
    notify();
  };
  pressWaits++;
  notify();
  const timer = setTimeout(close, PRESS_WINDOW_MS);
  try {
    return await run();
  } finally {
    clearTimeout(timer);
    close();
  }
}

export async function apiFetch(url: string, init: RequestInit = {}): Promise<Response> {
  const method = (init.method ?? 'GET').toUpperCase();
  const write = method !== 'GET' && method !== 'HEAD';
  const id = requestID();
  const once = async (): Promise<Response> => {
    if (!write) return fetch(url, init);
    const headers = new Headers(init.headers);
    headers.set(REQUEST_HEADER, id);
    return fetch(url, { ...init, headers });
  };
  const go = async () => {
    try {
      return await once();
    } catch (err) {
      if (!write || !(err instanceof TypeError) || init.signal?.aborted === true) throw err;
      return once();
    }
  };
  return whilePress(method, url, go);
}
