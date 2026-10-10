// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

export const Track = styled.div`
  display: flex;
  gap: 2px;
  padding: 3px;
  border-radius: var(--radius);
  background: var(--surface-2);
`;

export const Segment = styled.button<{ $selected: boolean }>`
  padding: 6px 10px;
  border-radius: var(--radius-sm);
  font-size: 12.5px;
  font-weight: 600;
  color: ${({ $selected }) => ($selected ? 'var(--ink)' : 'var(--ink-2)')};
  background: ${({ $selected }) => ($selected ? 'var(--surface)' : 'transparent')};
  box-shadow: ${({ $selected }) => ($selected ? '0 1px 3px rgba(22, 24, 27, 0.12)' : 'none')};
`;
