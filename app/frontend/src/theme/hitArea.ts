// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { css } from 'styled-components';

// A touch target of at least 44×44 (Apple HIG, WCAG 2.5.5) around a control drawn
// smaller: an invisible ::after centred on it takes the taps, the look stays.
// The control must be positioned; pass `relative` unless it already is.
export const hitArea = (position: 'relative' | '' = 'relative') => css`
  ${position ? `position: ${position};` : ''}

  &::after {
    content: '';
    position: absolute;
    left: 50%;
    top: 50%;
    width: max(100%, 44px);
    height: max(100%, 44px);
    transform: translate(-50%, -50%);
  }
`;
