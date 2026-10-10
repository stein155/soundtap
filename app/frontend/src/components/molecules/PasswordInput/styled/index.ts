// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';
import { hitArea } from '../../../../theme/hitArea';

export const Wrap = styled.div`
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 6px;
`;

export const Toggle = styled.button<{ $visible: boolean }>`
  ${hitArea()}
  width: 30px;
  height: 30px;
  flex-shrink: 0;
  display: grid;
  place-items: center;
  border-radius: var(--radius);
  color: ${({ $visible }) => ($visible ? 'var(--accent)' : 'var(--ink-3)')};
  transition:
    color 160ms ease,
    background 160ms ease;

  &:hover {
    color: var(--ink);
    background: var(--surface-3);
  }
`;
