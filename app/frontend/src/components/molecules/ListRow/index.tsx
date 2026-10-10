// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode } from 'react';
import { useLongPress } from '../../../hooks/useLongPress';
import { Art, Row, Subtitle, Text, Title } from './styled';

interface Props {
  // Left of the title: a logo or cover. Folders have none.
  art?: ReactNode;
  title: string;
  subtitle?: string;
  onClick: () => void;
  // What a long press does: save it on a preset, as a drag onto a preset in Stockholm.
  onHold?: () => void;
}

// A row in a browse list: a station, a category, a playlist, a track.
export function ListRow({ art, title, subtitle, onClick, onHold }: Props) {
  const press = useLongPress(onHold ?? null, onClick);
  return (
    <Row {...press}>
      {art && <Art>{art}</Art>}
      <Text>
        <Title>{title}</Title>
        {subtitle && <Subtitle>{subtitle}</Subtitle>}
      </Text>
    </Row>
  );
}
