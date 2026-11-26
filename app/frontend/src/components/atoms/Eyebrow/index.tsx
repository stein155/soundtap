// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode, ReactElement } from 'react';
import { Small } from './styled';

// The small line above a headline: what the headline is about ("for Kitchen").
export function Eyebrow({ children }: { readonly children: ReactNode }): ReactElement {
  return <Small>{children}</Small>;
}
