// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode } from 'react';
import { Text } from './styled';

// A one-line explanation under a control, or an error when `error`.
export function Hint({
  children,
  error,
  role,
}: {
  children: ReactNode;
  error?: boolean;
  role?: 'status' | 'alert';
}) {
  return (
    <Text $error={error} role={role}>
      {children}
    </Text>
  );
}
