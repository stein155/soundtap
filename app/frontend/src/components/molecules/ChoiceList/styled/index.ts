// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';
import { Glyph, Label, Row } from '../../SetItem/styled';

// A row of the list; a pick of things has a bar at the edge of the chosen one. A speaker's
// drawing is a size smaller here than on a settings row.
export const Choice = styled(Row)<{ $marked?: boolean | undefined; $tall?: boolean | undefined }>`
  box-shadow: ${({ $marked }) => ($marked === true ? 'inset 3px 0 0 var(--ink)' : 'none')};
  ${({ $tall }) => ($tall === true ? 'min-height: 54px;' : '')}

  /* A choice keeps its height with the hairline above it, as the design's language list;
     a tall one has the line on top of its height. */
  && + && {
    min-height: ${({ $tall }) => ($tall === true ? 55 : 52)}px;
  }

  & > ${Glyph} {
    width: 40px;
    height: 40px;
  }
`;

export const Tick = styled.span`
  display: grid;
  color: var(--accent);
`;

// The chosen thing's filled tick.
export const Mark = styled.span`
  width: 22px;
  height: 22px;
  flex-shrink: 0;
  display: grid;
  place-items: center;
  border-radius: 50%;
  background: var(--accent-fill);
  color: var(--on-accent-fill);

  svg {
    stroke-width: 3;
  }
`;

export const Trail = styled.span`
  flex-shrink: 0;
  display: flex;
  align-items: center;
  color: var(--ink-3);
`;

// The chosen option's name, in the weight its tick gives it.
export const Chosen = styled(Label)`
  font-weight: 600;
  color: var(--ink);
`;
