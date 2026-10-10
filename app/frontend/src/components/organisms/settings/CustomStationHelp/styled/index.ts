// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

export const Why = styled.p`
  margin: 0 4px 14px;
  font-size: 14px;
  font-weight: 700;
  color: var(--ink);
`;

export const Reasons = styled.ul`
  margin: 0 4px;
  padding: 0;
  list-style: none;
  font-size: 13px;
  line-height: 1.5;
  color: var(--ink-2);
`;

export const Reason = styled.li`
  margin-bottom: 8px;
  font-weight: 700;

  &::before {
    content: '- ';
  }
`;

export const Details = styled.ul`
  margin: 4px 0 0;
  padding-left: 32px;
  list-style: circle;
  font-weight: 400;
`;
