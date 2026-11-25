// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode, ReactElement } from 'react';
import { Icon } from '../../atoms/Icon';
import { Message, Query, Row } from './styled';

// What a list says instead of rows, as Stockholm: an icon and a sentence beside it, with the
// query that found nothing when
// there was one.
export function SheetEmpty({
  query,
  children,
}: {
  query?: string;
  children: ReactNode;
}): ReactElement {
  return (
    <Message>
      {query && <Query>“{query}”</Query>}
      <Row>
        <Icon.info aria-hidden="true" />
        <div>{children}</div>
      </Row>
    </Message>
  );
}
