// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

export const Message = styled.div`
  padding: 24px 20px;
  font-size: 12px;
  font-weight: 400;
  color: var(--ink-2);
`;

export const Row = styled.div`
  display: flex;
  align-items: center;
  gap: 12px;
  svg {
    flex: none;
    width: 36px;
    height: 36px;
    color: var(--ink-3);
  }
`;

export const Query = styled.div`
  margin-bottom: 8px;
  font-size: 18px;
  font-weight: 700;
  color: var(--ink);
`;
