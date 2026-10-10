// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode } from 'react';
import { Label, Row, Value } from './styled';

export { RowInput, RowSelect, RowTime } from './styled';

interface Props {
  label: ReactNode;
  // The id of the control the label names; omit for a read-only value.
  htmlFor?: string;
  // `value` lays the row out as a label and a right-aligned value or small control
  // (a time, a select, "Connected"); the default gives an input the remaining width.
  layout?: 'input' | 'value';
  // The label takes the free width (a town name with a "Change" button beside it).
  wideLabel?: boolean;
  children?: ReactNode;
}

// One row inside a FieldCard.
export function FieldRow({ label, htmlFor, layout = 'input', wideLabel, children }: Props) {
  return (
    <Row $value={layout === 'value'}>
      <Label as={htmlFor ? 'label' : 'span'} htmlFor={htmlFor} $wide={wideLabel}>
        {label}
      </Label>
      {layout === 'value' && typeof children === 'string' ? <Value>{children}</Value> : children}
    </Row>
  );
}
