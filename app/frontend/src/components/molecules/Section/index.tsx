// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode } from 'react';
import { Block, Title } from './styled';

// A titled block of a page ("Rooms", "Recently played", ...). `stack` spaces its
// children as a column of cards.
export function Section({
  title,
  stack,
  children,
}: {
  title: string;
  stack?: boolean;
  children: ReactNode;
}) {
  return (
    <Block $stack={stack}>
      <Title $stack={stack}>{title}</Title>
      {children}
    </Block>
  );
}
