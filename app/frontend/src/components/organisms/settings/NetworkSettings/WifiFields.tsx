// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState, type ReactElement } from 'react';
import { useI18n } from '../../../../lib/i18n';
import { Icon } from '../../../atoms/Icon';
import { TextField } from '../../../atoms/TextField';
import { CheckRow } from '../../../molecules/CheckRow';
import { LabeledField } from '../../../molecules/LabeledField';
import { SelectField } from '../../../molecules/SelectField';
import { Reveal } from './styled';
import { badSsid, SSID_MAX_LENGTH, type Security } from './useWifiJoin';

export function ManualFields({
  ssid,
  onSsid,
  security,
  onSecurity,
}: {
  readonly ssid: string;
  readonly onSsid: (ssid: string) => void;
  readonly security: Security;
  readonly onSecurity: (security: Security) => void;
}): ReactElement {
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
            { value: 'wep', label: 'WEP' },
          ]}
          onChange={onSecurity}
        />
      </LabeledField>
    </>
  );
}

// Stockholm ticks "Show password": a Wi-Fi password is long and typed on a phone.
// label: the field's name where the page already says what it is for (the join sheet).
// eye: an eye inside the field shows and hides it instead, hidden to start (the first-run
// page, as drawn). wep: the password is a WEP key, and the length hint says what one is.
export function PasswordField({
  value,
  onChange,
  tooShort,
  label,
  eye = false,
  wep = false,
}: {
  readonly value: string;
  readonly onChange: (password: string) => void;
  readonly tooShort: boolean;
  readonly label?: string;
  readonly eye?: boolean | undefined;
  readonly wep?: boolean | undefined;
}): ReactElement {
  const { t } = useI18n();
  const [shown, setShown] = useState(!eye);
  const field = (
    <TextField
      id="wifi-pw"
      type={shown ? 'text' : 'password'}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      autoComplete="off"
      autoCapitalize="none"
      autoCorrect="off"
      spellCheck={false}
    />
  );
  return (
    <>
      <LabeledField
        id="wifi-pw"
        label={label ?? t('wifiPassword')}
        error={tooShort ? t(wep ? 'wifiWepKeyLength' : 'wifiPasswordLength') : null}
      >
        {eye ? (
          <Reveal>
            {field}
            <button
              type="button"
              aria-label={t('showPw')}
              aria-pressed={shown}
              // Keeps the field focused, so a phone's keyboard stays up.
              onMouseDown={(e) => e.preventDefault()}
              onClick={() => setShown(!shown)}
            >
              {shown ? <Icon.eyeOff width="20" height="20" /> : <Icon.eye width="20" height="20" />}
            </button>
          </Reveal>
        ) : (
          field
        )}
      </LabeledField>
      {!eye && <CheckRow plain on={shown} title={t('showPw')} onToggle={() => setShown(!shown)} />}
    </>
  );
}
