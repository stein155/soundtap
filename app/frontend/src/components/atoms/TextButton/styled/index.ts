// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

export const Underlined = styled.button`
  align-self: flex-start;
  justify-self: start;
  padding: 0;
  border: 0;
  background: none;
  font: inherit;
  font-size: 13px;
  font-weight: 600;
  color: var(--ink);
  text-align: start;
  text-decoration: underline;
  cursor: pointer;
  position: relative;

  /* The text is 16px tall; a finger needs 44. Grow the hit area, not the layout. */
  &::before {
    content: '';
    position: absolute;
    inset: -14px 0;
  }

  &:disabled {
    color: var(--ink-3);
    cursor: default;
  }
`;
