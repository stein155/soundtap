// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

export const Label = styled.span<{ $size: number }>`
  font-size: ${({ $size }) => $size}px;
  font-weight: 800;
  letter-spacing: 0.04em;
`;
