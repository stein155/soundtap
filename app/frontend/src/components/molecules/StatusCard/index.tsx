// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode } from 'react';
import { Spinner } from '../../atoms/Spinner';
import { Card } from './styled';

// A one-line state in a card: an icon (or a spinner while busy) and a sentence, like
// "No devices paired", "Searching your network…", "You're up to date".
export function StatusCard({
  icon,
  busy,
  children,
}: {
  icon?: ReactNode;
  busy?: boolean;
  children?: ReactNode;
}) {
  return (
    <Card aria-busy={busy || undefined}>
      {busy ? <Spinner /> : icon}
      {children && <span>{children}</span>}
    </Card>
  );
}
