// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled, { css } from 'styled-components';
import { reducedMotion, spin } from '../../../../theme/keyframes';

export const Ring = styled.span<{ $small?: boolean; $big?: boolean }>`
  width: 20px;
  height: 20px;
  flex-shrink: 0;
  display: inline-block;
  border-radius: 50%;
  border: 2px solid var(--surface-3);
  border-top-color: var(--ink);
  animation: ${spin} 0.8s linear infinite;
  ${reducedMotion}
  ${({ $small }) =>
    $small &&
    css`
      width: 10px;
      height: 10px;
      border-top-color: var(--accent-fill);
    `}
  ${({ $big }) =>
    $big &&
    css`
      width: 40px;
      height: 40px;
      border-width: 3px;
    `}
`;
