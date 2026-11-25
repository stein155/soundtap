// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

export const Block = styled.section<{ $stack?: boolean | undefined }>`
  padding: 18px 16px 0;
  display: ${({ $stack }) => ($stack === true ? 'flex' : 'block')};
  flex-direction: column;
  gap: 10px;
`;

export const Title = styled.h2<{ $stack?: boolean | undefined }>`
  margin: 0 0 ${({ $stack }) => ($stack === true ? 0 : 10)}px;
  font-size: 13px;
  font-weight: 600;
  color: var(--ink-3);
`;
