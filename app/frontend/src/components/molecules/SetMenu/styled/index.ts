// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

// One group of settings, as the design's lists: white rows across the whole width (out
// through the page's side gutter), a faint hairline between rows from edge to edge, on top
// of the row's own height as in the design.
export const Card = styled.div<{ $apart?: boolean | undefined }>`
  display: flex;
  flex-direction: column;
  margin-inline: calc(-1 * var(--page-gutter, 0px));
  background: var(--surface);
  ${({ $apart }) => ($apart === true ? 'border-block: 1px solid var(--border-strong);' : '')}

  & > * + * {
    border-top: 1px solid var(--border);
  }
`;

// A group with its title above and its note below. A titled group follows the one above
// it straight away: its grey bar is the separation.
export const Group = styled.section`
  display: flex;
  flex-direction: column;

  & + & {
    margin-top: 22px;
  }

  & + &:has(> h2) {
    margin-top: 0;
  }
`;

// Stockholm's section bar, as in the installer: grey, edge to edge, the group's title in it.
export const Bar = styled.h2`
  display: flex;
  align-items: center;
  min-height: 51px;
  margin: 0 calc(-1 * var(--page-gutter, 0px));
  padding: 4px var(--page-gutter, 16px);
  background: var(--section-bar);
  font-size: 13px;
  font-weight: 700;
  letter-spacing: 0.5px;
  color: var(--ink-2);
`;

// The note under a card: the hint's own 12 px above it, as the design's notes.
export const Note = styled.div``;
