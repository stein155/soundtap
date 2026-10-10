// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { apiFetch, apiURL, PRESS_TIMEOUT_MS, whilePress } from '../base';
import { deadline, httpError, getJSON, send, sendWithin } from './http';
import type { VersionInfo, Releases, UpdateResult, FirmwareReport, OwnerKey } from '../types';

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
  // The tag also rides in the query so the UI knows to show the press prompt.
  const path = tag ? `/api/update?tag=${encodeURIComponent(tag)}` : '/api/update';
  const r = await apiFetch(apiURL(path, speaker), {
    method: 'POST',
    headers: {
      Accept: 'application/json',
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ tag, confirm: true }),
    // With a tag the speaker answers once its key is pressed, before it downloads.
    signal: deadline(PRESS_TIMEOUT_MS),
  });
  let body: UpdateResult['body'] | null = null;
  try {
    body = (await r.json()) as UpdateResult['body'];
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
function upload(u: Upload): Promise<UploadOutcome> {
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

const uploadWithPress = (u: Upload) => whilePress('POST', u.url, () => upload(u));

export interface FirmwareUpload {
  // With install the package is also written to the inactive slot; without, only checked.
  install: boolean;
  onProgress: (percent: number) => void;
  speaker?: string;
  signature?: string;
  // The confirmation the check's report asked for.
  confirm?: FirmwareReport['confirm'];
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
    return JSON.parse(r.body) as FirmwareReport;
  } catch {
    throw new Error('Firmware response was invalid.');
  }
}

// startCoreUpdate has the speaker download the newest SoundTap Core release and install it
// into its spare slot, after a press on the speaker. It answers when the install is done:
// with the install's report, which offers the reboot, or with status current when there
// is nothing newer. The download and the NAND write take minutes.
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
    return JSON.parse(body) as FirmwareReport | { status: 'current'; version?: string };
  } catch {
    throw new Error('Firmware response was invalid.');
  }
}

export async function reboot(speaker?: string) {
  return send('/api/reboot', 'POST', { confirm: true }, speaker);
}

// getOwnerKey says whether the speaker trusts an owner key (ADR 0048) next to the release
// keys, and which. null when the speaker could not answer.
export async function getOwnerKey(speaker?: string): Promise<OwnerKey | null> {
  try {
    return await getJSON<OwnerKey>('/api/owner-key', speaker);
  } catch {
    return null;
  }
}

// setOwnerKey makes the speaker trust key, an ed25519 public key in base64, after a key
// held on the speaker. It answers with the new state.
export async function setOwnerKey(key: string, speaker?: string): Promise<OwnerKey> {
  return (await sendWithin('/api/owner-key', 'PUT', { body: { key }, speaker })) as OwnerKey;
}

// removeOwnerKey takes the owner key away, after a key held on the speaker.
export async function removeOwnerKey(speaker?: string): Promise<OwnerKey> {
  return (await sendWithin('/api/owner-key', 'DELETE', { speaker })) as OwnerKey;
}
