// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled, { css } from 'styled-components';

export const Row = styled.button<{ $plain?: boolean }>`
  width: 100%;
  display: flex;
  align-items: flex-start;
  gap: 13px;
  text-align: left;
  ${({ $plain }) =>
    $plain
      ? css`
          align-items: center;
          gap: 12px;
        `
      : css`
          width: calc(100% + 2 * var(--page-gutter, 0px));
          margin-inline: calc(-1 * var(--page-gutter, 0px));
          padding: 15px var(--page-gutter, 16px);
          border-bottom: 1px solid var(--border-strong);

          /* Stockholm: no line right under the page header. */
          &:not(:first-child) {
            border-top: 1px solid var(--border-strong);
          }
        `}

  &:disabled {
    opacity: 0.5;
    cursor: default;
  }
`;

export const Text = styled.span`
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 3px;
`;

export const Title = styled.span`
  font-size: 15px;
  font-weight: 600;
`;

export const Description = styled.span`
  font-size: 12.5px;
  line-height: 1.4;
  color: var(--ink-3);
  text-wrap: pretty;
`;
