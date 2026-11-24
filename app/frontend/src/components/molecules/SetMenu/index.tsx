// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode } from 'react';
import { FormSection } from '../../atoms/FormSection';
import { Hint } from '../../atoms/Hint';
import { Card, Group, Note } from './styled';

// One group of settings rows: an optional title above the card, the rows in it, and an
// optional note under it that explains the group (never a row of its own).
export function SetMenu({
  title,
  note,
  children,
}: {
  title?: string;
  note?: ReactNode;
  children: ReactNode;
}) {
  return (
    <Group aria-label={title}>
      {title && <FormSection>{title}</FormSection>}
      <Card>{children}</Card>
      {note && (
        <Note>
          <Hint>{note}</Hint>
        </Note>
      )}
    </Group>
  );
}
