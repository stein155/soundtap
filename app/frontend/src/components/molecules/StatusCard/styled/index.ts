// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

// The design's notice card: white, a fine edge, the icon in ink at the top of the text.
export const Card = styled.div`
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 14px 16px;
  border-radius: var(--radius);
  background: var(--surface);
  box-shadow: 0 0 0 1px var(--border);
  font-size: 14px;
  line-height: 1.5;
  color: var(--ink);

  & > svg {
    flex-shrink: 0;
    color: var(--ink);
  }
`;
