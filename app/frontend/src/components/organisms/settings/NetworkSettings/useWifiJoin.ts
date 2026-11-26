// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState } from 'react';
import { failureText } from '../../../../lib/failure';
import { useI18n } from '../../../../lib/i18n';
import { setWifi } from '../../../../lib/api';

// WEP is left out of Stockholm's list: the speakers cannot join it.
export type Security = 'none' | 'wpa_or_wpa2';
export interface JoinTarget {
  ssid: string;
  secure: boolean;
  password: string;
}
export interface Phase {
  kind: 'idle' | 'busy' | 'done' | 'error';
  text: string;
}
const IDLE: Phase = { kind: 'idle', text: '' };

// WPA passphrases are 8 to 63 bytes, or the raw key as 64 hex digits; the speaker refuses
// anything else.
export function badPassword(password: string): boolean {
  if (/^[0-9a-fA-F]{64}$/u.test(password)) return false;
  const bytes = new TextEncoder().encode(password).length;
  return !!password && (bytes < 8 || bytes > 63);
}

// An SSID is at most 32 bytes, and the speaker counts them as UTF-8. The field's
// maxLength uses it too: a typed character is at least one byte.
export const SSID_MAX_LENGTH = 32;

export function badSsid(ssid: string): boolean {
  return new TextEncoder().encode(ssid).length > SSID_MAX_LENGTH;
}

export function canJoin({ ssid, secure, password }: JoinTarget): boolean {
  return !!ssid.trim() && !badSsid(ssid) && (!secure || (!!password && !badPassword(password)));
}

// Sends one network to the speaker and tracks how that goes.
export interface WifiJoin {
  phase: Phase;
  reset: () => void;
  connect: (target: JoinTarget) => Promise<void>;
}

export function useWifiJoin(speaker?: string, beforeConnect?: () => Promise<unknown>): WifiJoin {
  const { t } = useI18n();
  const [phase, setPhase] = useState<Phase>(IDLE);
  const reset = () => setPhase(IDLE);
  const connect = async ({ ssid, secure, password }: JoinTarget) => {
    setPhase({ kind: 'busy', text: t('wifiConnecting') });
    try {
      await beforeConnect?.();
      // SSID bytes are significant: send exactly what was picked or typed.
      await setWifi(
        { ssid, security: secure ? 'wpa_or_wpa2' : 'none', password: secure ? password : '' },
        speaker
      );
      setPhase({ kind: 'done', text: t('wifiSaved') });
    } catch (e) {
      setPhase({ kind: 'error', text: failureText(e, t('wifiSetError'), t) });
    }
  };
  return { phase, reset, connect };
}
