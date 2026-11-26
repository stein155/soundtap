// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode, ReactElement } from 'react';
import { Column } from './styled';

// Children in a column with an even gap; `offset` is the space above it.
export function Stack({
  gap = 0,
  offset = 0,
  children,
}: {
  readonly gap?: number;
  readonly offset?: number;
  readonly children: ReactNode;
}): ReactElement {
  return (
    <Column $gap={gap} $offset={offset}>
      {children}
    </Column>
  );
}
