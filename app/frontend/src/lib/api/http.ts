// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { apiFetch, apiURL, needsPress, PRESS_WINDOW_MS } from '../base';
import type { ApiError } from '../types';

// When the speaker drops off Wi-Fi or reboots, its TCP connections black-hole rather
// than reset, so a fetch with no deadline hangs for the browser's default (minutes on
// Chrome). The polling loop awaits its refresh before scheduling the next one, so a
// single hung request used to freeze the whole UI — including after the speaker came
// back. Every request therefore carries a deadline.
export const REQUEST_TIMEOUT_MS = 8000;
// A write waits on the speaker doing it: playing a station or preset takes up to 12 s
// there, and a write cut off at 8 s said "not responding" while the station started.
export const WRITE_TIMEOUT_MS = 15000;
// A read the speaker cannot answer by itself: TuneIn, a media server and
// BlueZ get up to 12 s there (slowReadDeadline), and a read cut off at 8 s showed an error
// for an answer that was on its way.
export const SLOW_READ_TIMEOUT_MS = 15000;

// AbortSignal.timeout is unavailable on older WebViews; fall back to a manual signal
// rather than dropping the deadline. null (fetch's "no signal") only where neither exists.
export function deadline(ms = REQUEST_TIMEOUT_MS): AbortSignal | null {
  if (typeof AbortSignal !== 'undefined' && 'timeout' in AbortSignal) {
    return AbortSignal.timeout(ms);
  }
  if (typeof AbortController === 'undefined') return null;
  const c = new AbortController();
  setTimeout(() => c.abort(), ms);
  return c.signal;
}

// failure builds the ApiError of an error body: {"error": "...", "code": "..."} (ADR 0091).
// The message stays English for logs and the console; the UI says the code.
function failure(status: number, body: unknown, msg: string): ApiError {
  const fields: { error?: unknown; code?: unknown } =
    typeof body === 'object' && body !== null ? body : {};
  const { error, code } = fields;
  const err: ApiError = new Error(typeof error === 'string' && error ? error : msg);
  err.status = status;
  if (typeof code === 'string' && code) err.code = code;
  return err;
}

// Every API error is {"error", "code"} (ADR 0091); the status line stands in when a
// proxy or an older speaker answered with something else.
export async function apiFailure(r: Response, what: string): Promise<ApiError> {
  let body: unknown;
  try {
    body = await r.json();
  } catch {
    /* not JSON */
  }
  return failure(r.status, body, `${what} -> ${r.status}`);
}

// An upload answered by an error: the {"error"} reason (ADR 0091), not the raw JSON body
// the user used to see, and the status and code so the UI can tell a refusal from an outage.
export function httpError(status: number, body: string, what: string): ApiError {
  let parsed: unknown;
  try {
    parsed = JSON.parse(body);
  } catch {
    /* not JSON */
  }
  return failure(status, parsed, body.trim() || `${what} -> ${status}`);
}

// The one place a body is taken at its word: a response whose shape the server's OpenAPI
// contract (schema.d.ts) fixes. Everything else from the network is checked field by field.
export async function contract<T>(body: Promise<unknown>): Promise<T> {
  // eslint-disable-next-line @typescript-eslint/no-unsafe-type-assertion -- the server's OpenAPI contract (schema.d.ts) types this body
  return (await body) as T;
}

export async function getJSON<T>(path: string, speaker?: string, timeoutMs?: number): Promise<T> {
  const r = await apiFetch(apiURL(path, speaker), {
    headers: { Accept: 'application/json' },
    signal: deadline(timeoutMs),
  });
  if (!r.ok) throw await apiFailure(r, `GET ${path}`);
  return contract<T>(r.json());
}

export interface SendOptions {
  body?: unknown;
  speaker?: string | undefined;
  timeoutMs?: number | undefined;
}

// send writes to the API within the usual write timeout.
export const send = async (
  path: string,
  method: string,
  body?: unknown,
  speaker?: string
): Promise<unknown> => sendWithin(path, method, { body, speaker });

// sendWithin is send with a timeout of its own, for the writes that take longer.
export async function sendWithin(
  path: string,
  method: string,
  { body, speaker, timeoutMs = WRITE_TIMEOUT_MS }: SendOptions
): Promise<unknown> {
  const url = apiURL(path, speaker);
  // A route held for a key press does its work after the press: timeoutMs is for that
  // work, so the press window comes on top.
  const hasBody = Boolean(body); // any value that reads as true is sent as JSON
  const wait = needsPress(method, url) ? timeoutMs + PRESS_WINDOW_MS : timeoutMs;
  const r = await apiFetch(url, {
    method,
    ...(hasBody
      ? { headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) }
      : {}),
    signal: deadline(wait),
  });
  if (!r.ok) {
    throw await apiFailure(r, `${method} ${path}`);
  }
  return r.status === 204 ? null : r.json().catch(() => null);
}

// text reads a string field from loosely typed JSON; anything else is empty.
export const text = (v: unknown): string => (typeof v === 'string' ? v : '');
