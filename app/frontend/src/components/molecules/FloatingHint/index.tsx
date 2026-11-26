// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode, ReactElement } from 'react';
import { Hint } from '../../atoms/Hint';
import { Float } from './styled';

// Feedback for a tap in a long list ("couldn't play", "playing X"), held at the bottom of
// the visible part of the scroller rather than at the top or end of the list, where it
// was thousands of pixels away from the row that was tapped.
export function FloatingHint({
  children,
  error,
  role = 'status',
}: {
  readonly children: ReactNode;
  readonly error?: boolean | undefined;
  readonly role?: 'status' | 'alert';
}): ReactElement {
  return (
    <Float>
      <Hint error={error} role={role}>
        {children}
      </Hint>
    </Float>
  );
}
