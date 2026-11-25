// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactElement } from 'react';
import { TextField } from '../../../atoms/TextField';
import { LabeledField } from '../../../molecules/LabeledField';
import { NAME_MAX_LENGTH } from '../../../../lib/limits';

// The speaker's name on a page of its own: the label above the field, Save at the bottom.
export function NameRow({
  label,
  value,
  placeholder,
  onChange,
}: {
  label: string;
  value: string;
  placeholder: string;
  onChange: (name: string) => void;
}): ReactElement {
  return (
    <LabeledField id="speaker-name" label={label}>
      <TextField
        id="speaker-name"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        maxLength={NAME_MAX_LENGTH}
        autoComplete="off"
      />
    </LabeledField>
  );
}
