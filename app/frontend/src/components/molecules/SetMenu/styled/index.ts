// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

// Plain full-width rows on the page, a 1px line between them and above and below the
// group; it runs out to the screen's edges through the page's side gutter.
export const Card = styled.div`
  display: flex;
  flex-direction: column;
  margin-inline: calc(-1 * var(--page-gutter, 0px));
  border-bottom: 1px solid var(--border-strong);

  /* Stockholm: no line right under the page header. */
  &:not(:first-child),
  *:not(:first-child) > & {
    border-top: 1px solid var(--border-strong);
  }

  & > * + * {
    position: relative;
  }

  & > * + *::before {
    content: '';
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    height: 1px;
    background: var(--border-strong);
    pointer-events: none;
  }
`;
