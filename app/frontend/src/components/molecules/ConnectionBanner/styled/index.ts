// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';
import { popIn } from '../../../../theme/keyframes';
import { desktop } from '../../../../theme/media';

export const Bar = styled.div`
  position: absolute;
  left: 12px;
  right: 12px;
  top: calc(env(safe-area-inset-top) + 8px);
  z-index: 85;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 14px;
  border-radius: var(--radius);
  background: var(--ink);
  color: var(--on-ink);
  font-size: 13.5px;
  font-weight: 600;
  box-shadow: var(--shadow-lg);
  animation: ${popIn} 160ms ease;
  pointer-events: auto;

  @media ${desktop} {
    top: 40px;
  }
`;
