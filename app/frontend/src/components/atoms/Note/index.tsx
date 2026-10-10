// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode } from 'react';
import { Paragraph } from './styled';

// Running text above a group of controls, explaining what they do.
export function Note({ children }: { children: ReactNode }) {
  return <Paragraph>{children}</Paragraph>;
}
