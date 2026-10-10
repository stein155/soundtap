// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

// Runs out to the screen's edges through the page's side gutter, like SetMenu.
export const Card = styled.div<{ $spaced?: boolean }>`
  margin-top: ${({ $spaced }) => ($spaced ? 8 : 0)}px;
  margin-inline: calc(-1 * var(--page-gutter, 0px));
  border-bottom: 1px solid var(--border-strong);

  /* Stockholm: no line right under the page header. */
  &:not(:first-child) {
    border-top: 1px solid var(--border-strong);
  }
`;
