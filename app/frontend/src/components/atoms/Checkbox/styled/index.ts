// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled, { css } from 'styled-components';

export const Box = styled.span<{ $checked: boolean; $strong?: boolean }>`
  width: 22px;
  height: 22px;
  flex-shrink: 0;
  margin-top: 1px;
  display: grid;
  place-items: center;
  border-radius: var(--radius);
  border: 2px solid var(--surface-3);
  background: var(--bg);
  color: var(--on-accent-fill);
  transition:
    background 160ms ease,
    border-color 160ms ease;
  ${({ $strong }) =>
    $strong &&
    css`
      margin-top: 0;
      border-color: var(--ink-3);
      background: transparent;
    `}
  ${({ $checked }) =>
    $checked &&
    css`
      background: var(--accent-fill);
      border-color: var(--accent-fill);
    `}
`;
