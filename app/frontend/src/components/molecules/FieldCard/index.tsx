// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode } from 'react';
import { Card } from './styled';

// The group fields sit in: plain full-width rows with a line between them, as
// Stockholm's settings and SetMenu. `spaced` sets it apart from what is above it.
export function FieldCard({ children, spaced }: { children: ReactNode; spaced?: boolean }) {
  return <Card $spaced={spaced}>{children}</Card>;
}
