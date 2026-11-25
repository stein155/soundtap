// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode, ReactElement } from 'react';
import { useLongPress } from '../../../hooks/useLongPress';
import { Icon } from '../../atoms/Icon';
import { Art, Chevron, Row, Subtitle, Text, Title } from './styled';

interface Props {
  // Left of the title: a logo or cover. Folders have none.
  art?: ReactNode;
  title: string;
  subtitle?: string;
  onClick: () => void;
  // What a long press does: save it on a preset, as a drag onto a preset in Stockholm.
  onHold?: (() => void) | undefined;
  // A track: the smaller cover.
  small?: boolean;
}

// A row in a browse list: a station, a category, a playlist, a track. A row without a
// picture is a folder: plain, with a chevron to what it opens.
export function ListRow({ art, title, subtitle, onClick, onHold, small }: Props): ReactElement {
  const press = useLongPress(onHold ?? null, onClick);
  const hasArt = Boolean(art);
  const folder = !hasArt;
  return (
    <Row {...press} $small={small} $folder={folder}>
      {hasArt && <Art $small={small}>{art}</Art>}
      <Text>
        <Title $folder={folder}>{title}</Title>
        {subtitle && <Subtitle>{subtitle}</Subtitle>}
      </Text>
      {folder && (
        <Chevron aria-hidden="true">
          <Icon.chevron width="18" height="18" />
        </Chevron>
      )}
    </Row>
  );
}
