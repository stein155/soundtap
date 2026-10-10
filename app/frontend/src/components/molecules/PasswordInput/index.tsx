// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState } from 'react';
import { Icon } from '../../atoms/Icon';
import { RowInput } from '../FieldRow';
import { useI18n } from '../../../lib/i18n';
import { Toggle, Wrap } from './styled';

interface Props {
  id: string;
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  autoComplete?: string;
}

// A password field with a show/hide toggle, for use inside a FieldRow.
export function PasswordInput({ id, value, onChange, placeholder, autoComplete = 'off' }: Props) {
  const { t } = useI18n();
  const [visible, setVisible] = useState(false);
  return (
    <Wrap>
      <RowInput
        id={id}
        type={visible ? 'text' : 'password'}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        autoComplete={autoComplete}
      />
      <Toggle
        type="button"
        $visible={visible}
        onClick={() => setVisible((v) => !v)}
        aria-label={t('showPw')}
        aria-pressed={visible}
      >
        {visible ? <Icon.eyeOff width="18" height="18" /> : <Icon.eye width="18" height="18" />}
      </Toggle>
    </Wrap>
  );
}
