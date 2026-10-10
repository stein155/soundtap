// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode } from 'react';
import { Column } from './styled';

// Children in a column with an even gap; `offset` is the space above it.
export function Stack({
  gap = 0,
  offset = 0,
  children,
}: {
  gap?: number;
  offset?: number;
  children: ReactNode;
}) {
  return (
    <Column $gap={gap} $offset={offset}>
      {children}
    </Column>
  );
}
