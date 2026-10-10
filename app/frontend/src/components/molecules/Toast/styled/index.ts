// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled, { keyframes } from 'styled-components';
import { desktop } from '../../../../theme/media';

// It sits above the dock, so it comes up out of it.
const rise = keyframes`
  from { opacity: 0; transform: translateY(8px); }
`;

// Floats above the dock, and above every sheet and the now-playing screen (z 40-60):
// an error from an action taken there has to be seen there. It lives in the
// TopLayer, which is as wide as the app frame.
export const Bubble = styled.div`
  position: absolute;
  left: 16px;
  right: 16px;
  bottom: calc(env(safe-area-inset-bottom) + 136px);
  z-index: 80;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px 12px 12px 16px;
  border-radius: var(--radius);
  background: var(--ink);
  color: var(--on-ink);
  font-size: 13.5px;
  font-weight: 600;
  box-shadow: var(--shadow-lg);
  animation: ${rise} 160ms var(--ease);
  pointer-events: auto;

  @media ${desktop} {
    bottom: 168px;
  }
`;

export const Text = styled.span`
  flex: 1;
  min-width: 0;
`;

export const Close = styled.button`
  width: 28px;
  height: 28px;
  flex-shrink: 0;
  display: grid;
  place-items: center;
  border-radius: var(--radius-sm);
  color: inherit;
  opacity: 0.8;
`;

export const UndoButton = styled.button`
  flex-shrink: 0;
  min-height: 32px;
  padding: 0 10px;
  border-radius: var(--radius-sm);
  color: var(--accent-fill);
  font-weight: 700;
`;
