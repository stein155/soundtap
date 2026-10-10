// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled, { css } from 'styled-components';
import { reducedMotion, spin } from '../../../../theme/keyframes';

export const Turning = styled.span<{ $active: boolean }>`
  display: inline-grid;
  ${({ $active }) =>
    $active &&
    css`
      animation: ${spin} 0.9s linear infinite;
      ${reducedMotion}
    `}
`;
