// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

export const Column = styled.div<{ $gap: number; $offset: number }>`
  display: flex;
  flex-direction: column;
  gap: ${({ $gap }) => $gap}px;
  margin-top: ${({ $offset }) => $offset}px;
`;
