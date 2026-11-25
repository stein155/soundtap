// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode, ReactElement } from 'react';
import { Hint } from '../../atoms/Hint';
import { Bar, Card, Group, Note } from './styled';

// One group of settings rows: an optional title in a grey bar above the card, the rows in it, and an
// optional note under it that explains the group (never a row of its own). A group set
// apart (the factory reset) has a firmer line above and below it.
export function SetMenu({
  title,
  note,
  apart,
  children,
}: {
  title?: string | undefined;
  note?: ReactNode;
  apart?: boolean;
  children: ReactNode;
}): ReactElement {
  return (
    <Group aria-label={title}>
      {title && <Bar>{title}</Bar>}
      <Card $apart={apart}>{children}</Card>
      {Boolean(note) && (
        <Note>
          <Hint>{note}</Hint>
        </Note>
      )}
    </Group>
  );
}
