// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode, ReactElement } from 'react';
import { Checkbox } from '../../atoms/Checkbox';
import { Description, Row, Text, Title } from './styled';

interface Props {
  readonly on: boolean;
  readonly title: string;
  readonly desc?: ReactNode;
  readonly disabled?: boolean;
  readonly onToggle: () => void;
  // The bare row, for a card that adds more below it (the speakers sheet).
  readonly plain?: boolean;
}

// Something that is on or off: a checkbox, a title and what it means. The whole row is
// the target.
export function CheckRow({ on, title, desc, disabled, onToggle, plain }: Props): ReactElement {
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
        {Boolean(desc) && <Description>{desc}</Description>}
      </Text>
    </Row>
  );
}
