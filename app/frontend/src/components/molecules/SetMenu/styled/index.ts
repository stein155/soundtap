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
  background: var(--surface);

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
  min-height: 43px;
  margin: 0 calc(-1 * var(--page-gutter, 0px));
  padding: 4px var(--page-gutter, 16px);
  background: var(--section-bar);
  font-size: 13px;
  font-weight: 700;
  letter-spacing: 0.5px;
  color: var(--ink-2);
`;

export const Note = styled.div`
  padding-top: 6px;
`;
