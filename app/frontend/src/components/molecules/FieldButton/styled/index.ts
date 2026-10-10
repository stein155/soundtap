// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';
import { rowLayout } from '../../FieldRow/styled';

export const Label = styled.span`
  flex: 1;
  min-width: 0;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  font-size: 15px;
  font-weight: 500;
`;

export const Row = styled.button<{ $selected?: boolean }>`
  ${rowLayout}
  width: 100%;
  text-align: left;
  color: var(--ink);

  &:hover:not(:disabled) {
    background: var(--surface-2);
  }

  ${Label} {
    color: ${({ $selected }) => ($selected ? 'var(--accent)' : 'inherit')};
    font-weight: ${({ $selected }) => ($selected ? 700 : 500)};
  }
`;
