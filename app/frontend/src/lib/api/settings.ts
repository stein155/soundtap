// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { apiFetch, apiURL, PRESS_TIMEOUT_MS } from '../base';
import {
  contract,
  deadline,
  apiFailure,
  getJSON,
  send,
  sendWithin,
  SLOW_READ_TIMEOUT_MS,
} from './http';
import type { CrashReport, Settings, Tone, WifiNetwork } from '../types';

// Settings: name, bass (with its range), network, display, alarm, multiroom and MQTT.
export async function getSettings(speaker?: string): Promise<Settings | null> {
  try {
    return await getJSON<Settings>('/api/settings', speaker);
  } catch {
    return null;
  }
}

// A soundbar's tone levels with their ranges; null when it has not answered.
export async function getTone(speaker?: string): Promise<Tone | null> {
  try {
    return await getJSON<Tone>('/api/soundbar/tone', speaker);
  } catch {
    return null;
  }
}

// A soundbar's lip-sync delay in ms (ms null until it has said) and the most it takes;
// null on a speaker without one.
export async function getLipSync(
  speaker?: string
): Promise<{ ms: number | null; max: number } | null> {
  try {
    return await getJSON<{ ms: number | null; max: number }>('/api/soundbar/lip-sync', speaker);
  } catch {
    return null;
  }
}

export async function setLipSync(ms: number, speaker?: string): Promise<unknown> {
  return send('/api/soundbar/lip-sync', 'PUT', { ms }, speaker);
}

// A soundbar's one-of-a-few settings: its audio mode and its HDMI-CEC mode.
export type SoundbarChoice = 'audio-mode' | 'cec-mode' | 'hdmi-input';

// The setting's current value; null when the bar has not answered or has no such setting.
export async function getSoundbarChoice(
  choice: SoundbarChoice,
  speaker?: string
): Promise<string | null> {
  try {
    return (await getJSON<{ mode: string }>(`/api/soundbar/${choice}`, speaker)).mode;
  } catch {
    return null;
  }
}

export async function setSoundbarChoice(
  choice: SoundbarChoice,
  mode: string,
  speaker?: string
): Promise<unknown> {
  return send(`/api/soundbar/${choice}`, 'PUT', { mode }, speaker);
}

// The soundbar takes all four levels at once.
export async function startAdaptiq(speaker?: string): Promise<unknown> {
  return send('/api/soundbar/adaptiq', 'POST', { confirm: true }, speaker);
}

export async function setTone(
  levels: Record<keyof Tone, number>,
  speaker?: string
): Promise<unknown> {
  return send('/api/soundbar/tone', 'PUT', levels, speaker);
}

// saveSettings applies any subset of the settings; the speaker validates all of it
// before it writes any of it.
export async function saveSettings(
  patch: Record<string, unknown>,
  speaker?: string
): Promise<unknown> {
  return send('/api/settings', 'PUT', patch, speaker);
}

// The crash the speaker offers to report, null when there is none, reports are off or the
// speaker did not answer (ADR 0126).
export async function getCrashReport(speaker?: string): Promise<CrashReport | null> {
  try {
    return (
      await getJSON<{ enabled: boolean; crash: CrashReport | null }>('/api/crash-report', speaker)
    ).crash;
  } catch {
    return null;
  }
}

// Records that the owner opened the issue for a crash or dismissed it: it is not offered again.
export async function recordCrashReport(
  id: string,
  outcome: 'reported' | 'dismissed',
  speaker?: string
): Promise<unknown> {
  return send('/api/crash-report', 'POST', { id, outcome }, speaker);
}

// The support archive (GET /api/debug?download=1): logs, the previous boot's records,
// diagnostics, versions and health in one zip, redacted on the speaker. It waits for a
// press on the speaker (ADR 0055), which apiFetch announces while it waits.
export async function getSupportBundle(speaker?: string): Promise<{ blob: Blob; name: string }> {
  const path = '/api/debug?download=1';
  const r = await apiFetch(apiURL(path, speaker), { signal: deadline(PRESS_TIMEOUT_MS) });
  if (!r.ok) throw await apiFailure(r, `GET ${path}`);
  const disposition = r.headers.get('Content-Disposition') ?? '';
  const name =
    /filename="(?<name>[^"]+)"/u.exec(disposition)?.groups?.['name'] ?? 'soundtap-support.zip';
  return { blob: await r.blob(), name };
}

// After the key press the speaker gives a factory reset up to 20 s (restoreDeadline, which
// also bounds POST /api/restore; the UI no longer offers a restore).
const RESTORE_TIMEOUT_MS = 35000;

// factoryReset clears presets, settings, the speaker's name, paired devices and
// Wi-Fi, then restarts into setup mode. The firmware is not touched.
export async function factoryReset(speaker?: string): Promise<unknown> {
  return sendWithin('/api/factory-reset', 'POST', {
    body: { confirm: true },
    speaker,
    timeoutMs: RESTORE_TIMEOUT_MS,
  });
}

// Bluetooth — what the speaker's own radio can say without a phone in hand: whether
// there is an adapter, the name a phone sees, and which devices it remembers. Stock
// answered this on /bluetoothInfo; SoundTap had no equivalent, so a pairing was
// invisible until you tried to play something.
export interface BluetoothDevice {
  address: string;
  name: string;
  connected: boolean;
}
export interface BluetoothStatus {
  adapter: boolean;
  alias: string;
  powered: boolean;
  discoverable: boolean;
  // null from a speaker on an older release with nothing paired.
  devices?: BluetoothDevice[] | null;
}

export async function getBluetooth(speaker?: string): Promise<BluetoothStatus | null> {
  try {
    return await getJSON<BluetoothStatus>('/api/bluetooth', speaker, SLOW_READ_TIMEOUT_MS);
  } catch {
    return null;
  }
}

// Forget one paired device by its address, as stock's app did. The speaker gives BlueZ up
// to 30 s for it (bluetoothClearDeadline). After the key press (sendWithin adds the press
// window): the speaker's 30 s, plus a proxied speaker's slack on top.
const BLUETOOTH_CLEAR_TIMEOUT_MS = 45000;

export async function forgetBluetoothDevice(address: string, speaker?: string): Promise<unknown> {
  return sendWithin('/api/bluetooth/forget', 'POST', {
    body: { address },
    speaker,
    timeoutMs: BLUETOOTH_CLEAR_TIMEOUT_MS,
  });
}

export async function pairBluetooth(speaker?: string): Promise<unknown> {
  return send('/api/bluetooth/pair', 'POST', undefined, speaker);
}

// Wi-Fi — scan nearby networks and switch the speaker onto one. The scan is a site
// survey on the speaker (slow; show a spinner) and may come back empty on some
// hardware, in which case the UI lets the user type the SSID in. Each network is
// { ssid, signal, secure }. null when the survey failed or timed out, so the UI can tell
// that from an empty one. The speaker gives a survey up to 80 s (an ST20's BCO takes tens
// of seconds), so the request waits a little longer than that.
const WIFI_SCAN_TIMEOUT_MS = 90000;
export async function scanWifi(speaker?: string): Promise<WifiNetwork[] | null> {
  try {
    // A POST: a survey takes the speaker's radio, so it goes through the write guard.
    const r = await contract<{ networks?: WifiNetwork[] } | null>(
      sendWithin('/api/wifi/scan', 'POST', { speaker, timeoutMs: WIFI_SCAN_TIMEOUT_MS })
    );
    return r?.networks ?? [];
  } catch {
    return null;
  }
}

// setWifi joins the speaker to a network. security is 'wpa_or_wpa2' | 'wep' | 'none'.
// Changing networks can briefly drop the speaker (and this app) — warn first. After the
// key press the speaker gives the join up to 60 s (wifiJoinDeadline): an ST20's BCO
// takes 10-50 s to associate.
const WIFI_JOIN_TIMEOUT_MS = 75000;
export async function setWifi(
  {
    ssid,
    security,
    password,
  }: {
    ssid: string;
    security: string;
    password: string;
  },
  speaker?: string
): Promise<unknown> {
  return sendWithin('/api/wifi', 'POST', {
    body: { ssid, security, password },
    speaker,
    timeoutMs: WIFI_JOIN_TIMEOUT_MS,
  });
}

// The networks the speaker remembers: names and an opaque id, never a passphrase.
export interface SavedWifi {
  id: string;
  ssid: string;
}
export async function getSavedWifi(speaker?: string): Promise<SavedWifi[]> {
  try {
    const r = await getJSON<{ networks?: SavedWifi[] }>('/api/wifi/saved', speaker);
    return r.networks ?? [];
  } catch {
    return [];
  }
}

// Why the speaker's last Wi-Fi join failed (wrong_password, not_found, ...), or ''.
export async function getWifiJoinFailed(speaker?: string): Promise<string> {
  try {
    const r = await getJSON<{ joinFailed?: string }>('/api/wifi/saved', speaker);
    return r.joinFailed ?? '';
  } catch {
    return '';
  }
}

// Why new settings did not take and the speaker went back to its last network, or ''.
export async function getWifiFellBack(speaker?: string): Promise<string> {
  try {
    const r = await getJSON<{ fellBack?: string }>('/api/wifi/saved', speaker);
    return r.fellBack ?? '';
  } catch {
    return '';
  }
}

export async function dismissWifiFellBack(speaker?: string): Promise<unknown> {
  return send('/api/wifi/fallback/dismiss', 'POST', undefined, speaker);
}

export async function forgetWifi(id: string, speaker?: string): Promise<unknown> {
  return send('/api/wifi/forget', 'POST', { id }, speaker);
}

// getMqttStatus returns { connected, lastError } for the MQTT link.
export async function getMqttStatus(speaker?: string): Promise<{
  connected: boolean;
  lastError: string;
} | null> {
  try {
    return await getJSON('/api/mqtt/status', speaker);
  } catch {
    return null;
  }
}
