// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode, ReactElement } from 'react';
import { Text } from './styled';

// A one-line explanation under a control, or an error when `error`.
export function Hint({
  children,
  error,
  role,
}: {
  children: ReactNode;
  error?: boolean | undefined;
  role?: 'status' | 'alert';
}): ReactElement {
  return (
    <Text $error={error} role={role}>
      {children}
    </Text>
  );
}
