// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode } from 'react';
import { Turning } from './styled';

// Turns its child (an icon) while `active`: a retry in progress.
export function Spinning({ active, children }: { active: boolean; children: ReactNode }) {
  return <Turning $active={active}>{children}</Turning>;
}
