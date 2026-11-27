// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState } from 'react';
import { failureText } from '../../../../lib/failure';
import { useI18n } from '../../../../lib/i18n';
import { setWifi } from '../../../../lib/api';

// The choices of Stockholm's list, WEP among them (ADR 0136).
export type Security = 'none' | 'wpa_or_wpa2' | 'wep';
// wep: the password is a WEP key (a surveyed WEP network, or WEP picked by hand).
export interface JoinTarget {
  ssid: string;
  secure: boolean;
  password: string;
  wep?: boolean | undefined;
}
export interface Phase {
  kind: 'idle' | 'busy' | 'done' | 'error';
  text: string;
}
const IDLE: Phase = { kind: 'idle', text: '' };

// WPA passphrases are 8 to 63 bytes, or the raw key as 64 hex digits; a WEP key is 5 or 13
// characters, or 10 or 26 hex digits. The speaker refuses anything else.
export function badPassword(password: string, wep = false): boolean {
  if (wep)
    return (
      !!password && !/^(?:[ -~]{5}|[ -~]{13}|[0-9a-fA-F]{10}|[0-9a-fA-F]{26})$/u.test(password)
    );
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

export function canJoin({ ssid, secure, password, wep = false }: JoinTarget): boolean {
  return (
    !!ssid.trim() && !badSsid(ssid) && (!secure || (!!password && !badPassword(password, wep)))
  );
}

// What the speaker is told the network's security is.
function securityOf(secure: boolean, wep: boolean): Security {
  if (!secure) return 'none';
  return wep ? 'wep' : 'wpa_or_wpa2';
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
  const connect = async ({ ssid, secure, password, wep = false }: JoinTarget) => {
    setPhase({ kind: 'busy', text: t('wifiConnecting') });
    try {
      await beforeConnect?.();
      // SSID bytes are significant: send exactly what was picked or typed.
      await setWifi(
        { ssid, security: securityOf(secure, wep), password: secure ? password : '' },
        speaker
      );
      setPhase({ kind: 'done', text: t('wifiSaved') });
    } catch (e) {
      setPhase({ kind: 'error', text: failureText(e, t('wifiSetError'), t) });
    }
  };
  return { phase, reset, connect };
}
