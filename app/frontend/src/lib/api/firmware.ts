// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { apiFetch, apiURL, PRESS_TIMEOUT_MS, whilePress } from '../base';
import { contract, deadline, httpError, getJSON, send } from './http';
import type { VersionInfo, Releases, UpdateResult, FirmwareReport } from '../types';

// getVersion returns { version, updatable }. updatable is true only on an installed
// speaker (where SoundTap can replace its own binary). Returns null if unreachable.
export async function getVersion(speaker?: string): Promise<VersionInfo | null> {
  try {
    return await getJSON<VersionInfo>('/api/version', speaker);
  } catch {
    return null;
  }
}

// getReleases returns what the speaker can update to:
// { current, updatable, stable: {tag,name}|null }. Returns null
// if unreachable (e.g. offline) so the UI can fall back to the plain Update button.
export async function getReleases(speaker?: string): Promise<Releases | null> {
  try {
    return await getJSON<Releases>('/api/releases', speaker);
  } catch {
    return null;
  }
}

// startUpdate asks the speaker to fetch a release and replace itself. With no tag
// it installs the latest stable; pass a release tag to install that one instead. On a real update the speaker restarts, so the next
// /api/version may briefly fail until it comes back — the caller polls for that.
export async function startUpdate(tag?: string, speaker?: string): Promise<UpdateResult> {
  // The tag also rides in the query, as the speaker's logs show it; no press either way.
  const path = tag ? `/api/update?tag=${encodeURIComponent(tag)}` : '/api/update';
  const r = await apiFetch(apiURL(path, speaker), {
    method: 'POST',
    headers: {
      Accept: 'application/json',
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ tag, confirm: true }),
    // The speaker answers before it downloads.
    signal: deadline(PRESS_TIMEOUT_MS),
  });
  let body: UpdateResult['body'] | null = null;
  try {
    body = await contract<UpdateResult['body']>(r.json());
  } catch {
    /* ignore */
  }
  return { ok: r.ok, status: r.status, body: body ?? {} };
}

interface Upload {
  url: string;
  headers: Record<string, string>;
  file: File;
  timeoutMs: number;
  onProgress: (percent: number) => void;
}

type UploadOutcome =
  { kind: 'load'; status: number; body: string } | { kind: 'error' | 'abort' | 'timeout' };

// upload posts a file with XMLHttpRequest: fetch has no upload-progress API yet.
async function upload(u: Upload): Promise<UploadOutcome> {
  return new Promise((resolve) => {
    const req = new XMLHttpRequest();
    req.open('POST', u.url);
    req.withCredentials = true;
    req.timeout = u.timeoutMs;
    for (const [name, value] of Object.entries(u.headers)) req.setRequestHeader(name, value);
    req.upload.onprogress = (event) => {
      if (event.lengthComputable) u.onProgress(Math.round((event.loaded / event.total) * 100));
    };
    req.onerror = () => resolve({ kind: 'error' });
    req.onabort = () => resolve({ kind: 'abort' });
    req.ontimeout = () => resolve({ kind: 'timeout' });
    req.onload = () => resolve({ kind: 'load', status: req.status, body: req.responseText });
    req.send(u.file);
  });
}

// An install upload waits for the press on the speaker and says so, signed or not
// (ADR 0048); a check without install=1 needs none.
const uploadWithPress = async (u: Upload): Promise<UploadOutcome> =>
  whilePress('POST', u.url, async () => upload(u));

export interface FirmwareUpload {
  // With install the package is also written to the inactive slot; without, only checked.
  install: boolean;
  onProgress: (percent: number) => void;
  speaker?: string;
  signature?: string | undefined;
  // The confirmation the check's report asked for.
  confirm?: FirmwareReport['confirm'] | undefined;
}

// Firmware uploads are raw STU streams.
export async function uploadFirmware(
  file: File,
  { install, onProgress, speaker, signature, confirm = 'install' }: FirmwareUpload
): Promise<FirmwareReport> {
  const headers: Record<string, string> = { 'Content-Type': 'application/octet-stream' };
  if (install) {
    headers['X-SoundTap-Confirm'] = confirm;
  }
  if (signature) headers['X-SoundTap-Signature'] = signature;
  const r = await uploadWithPress({
    url: apiURL(`/api/firmware${install ? '?install=1' : ''}`, speaker),
    headers,
    file,
    timeoutMs: 25 * 60 * 1000,
    onProgress,
  });
  switch (r.kind) {
    case 'error':
      throw new Error('Firmware upload failed.');
    case 'abort':
      throw new Error('Firmware upload was cancelled.');
    case 'timeout':
      throw new Error(
        'Firmware operation timed out. Do not reboot; retry after checking speaker connection.'
      );
    case 'load':
      break;
  }
  if (r.status < 200 || r.status >= 300) throw httpError(r.status, r.body, 'POST /api/firmware');
  try {
    return await contract<FirmwareReport>(Promise.resolve(JSON.parse(r.body)));
  } catch {
    throw new Error('Firmware response was invalid.');
  }
}

// startCoreUpdate has the speaker download the newest SoundTap Core release and install it
// into its spare slot; the speaker fetches it itself and it is signed, so no press. It answers
// when the install is done: with the install's report, after which the speaker restarts into
// the new slot on its own (restarting), or with status current when there is nothing newer.
// The download and the NAND write take minutes.
export async function startCoreUpdate(
  speaker?: string
): Promise<FirmwareReport | { status: 'current'; version?: string }> {
  const url = apiURL('/api/core-update', speaker);
  const r = await apiFetch(url, {
    method: 'POST',
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
    body: JSON.stringify({ confirm: true }),
    signal: deadline(PRESS_TIMEOUT_MS + 25 * 60 * 1000),
  });
  const body = await r.text();
  if (!r.ok) throw httpError(r.status, body, 'POST /api/core-update');
  try {
    return await contract<FirmwareReport | { status: 'current'; version?: string }>(
      Promise.resolve(JSON.parse(body))
    );
  } catch {
    throw new Error('Firmware response was invalid.');
  }
}

export async function reboot(speaker?: string): Promise<unknown> {
  return send('/api/reboot', 'POST', { confirm: true }, speaker);
}
