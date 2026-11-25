// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled, { keyframes } from 'styled-components';
import { reducedMotion } from '../../../../theme/keyframes';

const bounce = keyframes`
  0%, 100% { transform: scaleY(0.4); }
  50% { transform: scaleY(1); }
`;

// Heights and delays of the four bars, so they never move in step.
const BARS: [height: number, delay: number][] = [
  [60, 0],
  [100, 150],
  [40, 300],
  [80, 450],
];

export const Bars = styled.span<{ $height: number }>`
  display: inline-flex;
  align-items: end;
  gap: 3px;
  height: ${({ $height }) => $height}px;
  flex-shrink: 0;

  i {
    display: block;
    width: 3px;
    border-radius: 2px;
    background: var(--accent-fill);
    transform-origin: bottom;
    animation: ${bounce} 900ms ease-in-out infinite;
    ${reducedMotion}
  }

  ${BARS.map(([height, delay], i) => `i:nth-child(${i + 1}) { height: ${height}%; animation-delay: ${delay}ms; }`).join('\n')}
`;
