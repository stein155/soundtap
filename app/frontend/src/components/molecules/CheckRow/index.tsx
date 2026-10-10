// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode } from 'react';
import { Checkbox } from '../../atoms/Checkbox';
import { Description, Row, Text, Title } from './styled';

interface Props {
  on: boolean;
  title: string;
  desc?: ReactNode;
  disabled?: boolean;
  onToggle: () => void;
  // The bare row, for a card that adds more below it (the speakers sheet).
  plain?: boolean;
}

// Something that is on or off: a checkbox, a title and what it means. The whole row is
// the target.
export function CheckRow({ on, title, desc, disabled, onToggle, plain }: Props) {
  return (
    <Row
      type="button"
      role="checkbox"
      aria-checked={on}
      disabled={disabled}
      onClick={onToggle}
      $plain={plain}
    >
      <Checkbox checked={on} strong={plain} />
      <Text>
        <Title>{title}</Title>
        {desc && <Description>{desc}</Description>}
      </Text>
    </Row>
  );
}
