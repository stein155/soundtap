// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode } from 'react';
import { Card } from './styled';

// The rounded card a list of settings rows sits in, with a divider between rows.
export function SetMenu({ children }: { children: ReactNode }) {
  return <Card>{children}</Card>;
}
