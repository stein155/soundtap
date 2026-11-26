// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode, ReactElement } from 'react';
import { Card } from './styled';

// The group fields sit in: plain full-width rows with a line between them, as
// Stockholm's settings and SetMenu. `spaced` sets it apart from what is above it.
export function FieldCard({
  children,
  spaced,
}: {
  readonly children: ReactNode;
  readonly spaced?: boolean;
}): ReactElement {
  return <Card $spaced={spaced}>{children}</Card>;
}
