// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

export const Text = styled.div<{ $error?: boolean }>`
  margin-top: 7px;
  font-size: 11.5px;
  color: ${({ $error }) => ($error ? '#d44030' : 'var(--ink-3)')};

  /* Room before the next row, so the hint reads with the row above it. */
  &:not(:last-child) {
    margin-bottom: 14px;
  }
`;
