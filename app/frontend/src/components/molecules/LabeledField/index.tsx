// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode } from 'react';
import { Hint } from '../../atoms/Hint';
import { Field, Label } from './styled';

// A form field as Stockholm lays it out: the label above the control, any error below it.
export function LabeledField({
  id,
  label,
  error,
  children,
}: {
  id: string;
  label: string;
  error?: string | null;
  children: ReactNode;
}) {
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
