// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

export const Text = styled.div<{ $error?: boolean | undefined }>`
  margin-top: 12px;
  font-size: 12px;
  line-height: 1.5;
  color: ${({ $error }) => ($error === true ? '#d44030' : 'var(--ink-3)')};

  /* Room before the next row, so the hint reads with the row above it. */
  &:not(:last-child) {
    margin-bottom: 14px;
  }
`;
