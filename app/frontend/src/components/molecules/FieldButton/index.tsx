// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode } from 'react';
import { Label, Row } from './styled';

interface Props {
  children: ReactNode;
  selected?: boolean;
  disabled?: boolean;
  expanded?: boolean;
  onClick: () => void;
}

// A tappable row inside a FieldCard: a network, a town, a folder.
export function FieldButton({ children, selected, disabled, expanded, onClick }: Props) {
  return (
    <Row
      type="button"
      $selected={selected}
      disabled={disabled}
      aria-expanded={expanded}
      onClick={onClick}
    >
      {children}
    </Row>
  );
}

// The main text of a FieldButton, which takes the free width and ellipsizes.
export function FieldButtonLabel({ children }: { children: ReactNode }) {
  return <Label>{children}</Label>;
}
