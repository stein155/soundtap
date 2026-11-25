// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode, ReactElement } from 'react';
import { Heading } from './styled';

// A small uppercase heading between groups of a form. `spaced` sets it apart from the
// group above it.
export function FormSection({
  children,
  spaced,
}: {
  children: ReactNode;
  spaced?: boolean;
}): ReactElement {
  return <Heading $spaced={spaced}>{children}</Heading>;
}
