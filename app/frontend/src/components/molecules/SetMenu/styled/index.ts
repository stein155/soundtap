// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

// One group of settings, as Stockholm's lists: straight rows across the whole width (out
// through the page's side gutter), a line above and below the group and a hairline between
// rows that starts where the row's text does.
export const Card = styled.div`
  display: flex;
  flex-direction: column;
  margin-inline: calc(-1 * var(--page-gutter, 0px));
  border-block: 1px solid var(--border-strong);

  & > * + * {
    position: relative;
  }

  & > * + *::before {
    content: '';
    position: absolute;
    top: 0;
    left: var(--page-gutter, 14px);
    right: 0;
    height: 1px;
    background: var(--border-strong);
    pointer-events: none;
  }
`;

// A group with its title above and its note below.
export const Group = styled.section`
  display: flex;
  flex-direction: column;

  & + & {
    margin-top: 22px;
  }
`;

export const Note = styled.div`
  padding-top: 6px;
`;
