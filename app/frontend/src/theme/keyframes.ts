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

// A page pushed in over the list it came from, as iOS pushes a settings page: a short
// way in from the right, fading up. Only transform and opacity, so no layout runs.
export const pushIn = keyframes`
  from { opacity: 0; transform: translateX(var(--page-gutter, 16px)); }
`;

// Turns an animation off for people who asked their system for less motion.
export const reducedMotion = css`
  @media (prefers-reduced-motion: reduce) {
    animation: none;
  }
`;
