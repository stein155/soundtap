// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { css, keyframes } from 'styled-components';

// Motion shared by several components.

export const spin = keyframes`
  to { transform: rotate(360deg); }
`;

export const popIn = keyframes`
  from { opacity: 0; transform: translateY(-4px) scale(0.96); }
  to { opacity: 1; transform: translateY(0) scale(1); }
`;

// Turns an animation off for people who asked their system for less motion.
export const reducedMotion = css`
  @media (prefers-reduced-motion: reduce) {
    animation: none;
  }
`;
