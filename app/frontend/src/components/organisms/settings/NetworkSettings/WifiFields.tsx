// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState } from 'react';
import { useI18n } from '../../../../lib/i18n';
import type { WifiNetwork } from '../../../../lib/types';
import { Icon } from '../../../atoms/Icon';
import { TextField } from '../../../atoms/TextField';
import { CheckRow } from '../../../molecules/CheckRow';
import { LabeledField } from '../../../molecules/LabeledField';
import { SelectField } from '../../../molecules/SelectField';
import { SignalGlyph } from './SignalGlyph';
import { badSsid, SSID_MAX_LENGTH, type Security } from './useWifiJoin';

export function NetworkSelect({
  networks,
  chosen,
  onPick,
  disabled,
}: {
  networks: WifiNetwork[];
  chosen: WifiNetwork;
  onPick: (ssid: string) => void;
  disabled: boolean;
}) {
  const { t } = useI18n();
  return (
    <LabeledField id="wifi-network" label={t('wifiSelectNetwork')}>
      <SelectField
        id="wifi-network"
        label={t('wifiSelectNetwork')}
        value={chosen.ssid}
        options={networks.map((n) => ({ value: n.ssid, label: n.ssid }))}
        onChange={onPick}
        disabled={disabled}
        trail={
          <>
            {chosen.secure && <Icon.lock width="15" height="15" />}
            {chosen.signal && <SignalGlyph signal={chosen.signal} />}
          </>
        }
      />
    </LabeledField>
  );
}

export function ManualFields({
  ssid,
  onSsid,
  security,
  onSecurity,
}: {
  ssid: string;
  onSsid: (ssid: string) => void;
  security: Security;
  onSecurity: (security: Security) => void;
}) {
  const { t } = useI18n();
  return (
    <>
      <LabeledField
        id="wifi-ssid"
        label={t('wifiNetwork')}
        error={badSsid(ssid) ? t('wifiSsidLength') : null}
      >
        <TextField
          id="wifi-ssid"
          value={ssid}
          onChange={(e) => onSsid(e.target.value)}
          placeholder={`*${t('requiredField')}`}
          maxLength={SSID_MAX_LENGTH}
          autoComplete="off"
        />
      </LabeledField>
      <LabeledField id="wifi-security" label={t('wifiSecurityType')}>
        <SelectField
          id="wifi-security"
          label={t('wifiSecurityType')}
          value={security}
          options={[
            { value: 'none', label: t('wifiSecurityNone') },
            { value: 'wpa_or_wpa2', label: 'WPA / WPA2' },
          ]}
          onChange={onSecurity}
        />
      </LabeledField>
    </>
  );
}

// Stockholm ticks "Show password": a Wi-Fi password is long and typed on a phone.
export function PasswordField({
  value,
  onChange,
  tooShort,
}: {
  value: string;
  onChange: (password: string) => void;
  tooShort: boolean;
}) {
  const { t } = useI18n();
  const [shown, setShown] = useState(true);
  return (
    <>
      <LabeledField
        id="wifi-pw"
        label={t('wifiPassword')}
        error={tooShort ? t('wifiPasswordLength') : null}
      >
        <TextField
          id="wifi-pw"
          type={shown ? 'text' : 'password'}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={`*${t('requiredField')}`}
          autoComplete="off"
          autoCapitalize="none"
          autoCorrect="off"
          spellCheck={false}
        />
      </LabeledField>
      <CheckRow plain on={shown} title={t('showPw')} onToggle={() => setShown(!shown)} />
    </>
  );
}
