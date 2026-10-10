// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { TranslateFn } from './i18n';
import type { AlarmConfig, BassCaps, NetworkInfo, Preset } from './types';
import type { BluetoothStatus } from './api';

// The one-line values settings rows show while closed.

// "07:00 · NPO Radio 2", "07:00 · tone" when no preset is left to play, or "Off".
export function alarmSummary(
  alarm: AlarmConfig,
  presets: (Preset | null)[],
  t: TranslateFn
): string {
  const preset = presets[alarm.slot - 1] ?? presets.find(Boolean);
  if (!alarm.enabled) return t('off');
  return `${alarm.time} · ${preset ? preset.name : t('alarmTone')}`;
}

// The paired devices by name, or "No devices paired".
export function bluetoothSummary(status: BluetoothStatus, t: TranslateFn): string {
  return status.devices?.length
    ? status.devices.map((d) => d.name || d.address).join(', ')
    : t('btNone');
}

// "Ethernet", the Wi-Fi network's name, or a dash.
export function networkSummary(network: NetworkInfo | null, t: TranslateFn): string {
  return network?.type === 'ethernet' ? t('ethernetLabel') : network?.ssid || '—';
}

// Stock's Wireless Link has no bass control: its range is 0..0.
export const hasBass = (caps: BassCaps) => caps.min < caps.max;
