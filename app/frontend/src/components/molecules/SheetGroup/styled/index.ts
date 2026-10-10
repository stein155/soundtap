// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

export const Group = styled.div`
  margin-bottom: 20px;
`;

// Stockholm's section bar: grey, edge to edge.
export const Head = styled.div`
  display: flex;
  align-items: center;
  justify-content: space-between;
  min-height: 43px;
  margin-inline: calc(-1 * var(--page-gutter, 0px));
  padding: 4px var(--page-gutter, 12px);
  background: var(--section-bar);
  font-size: 13px;
  font-weight: 700;
  letter-spacing: 0.5px;
  color: var(--ink-2);
`;

export const Rows = styled.div`
  display: flex;
  flex-direction: column;
`;
