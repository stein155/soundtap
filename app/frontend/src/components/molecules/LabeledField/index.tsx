// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode, ReactElement } from 'react';
import { Hint } from '../../atoms/Hint';
import { Field, Label } from './styled';

// A form field as Stockholm lays it out: the label above the control, any error below it.
export function LabeledField({
  id,
  label,
  error,
  children,
}: {
  readonly id: string;
  readonly label: string;
  readonly error?: string | null;
  readonly children: ReactNode;
}): ReactElement {
  return (
    <Field>
      <Label htmlFor={id}>{label}</Label>
      {children}
      {error && (
        <Hint error role="alert">
          {error}
        </Hint>
      )}
    </Field>
  );
}
