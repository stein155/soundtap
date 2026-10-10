// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

export const Card = styled.div`
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 16px;
  border-radius: var(--radius);
  background: var(--surface);
  box-shadow: 0 0 0 1px var(--border);
  font-size: 14px;
  color: var(--ink-2);

  & > svg {
    flex-shrink: 0;
    color: var(--ink-3);
  }
`;
