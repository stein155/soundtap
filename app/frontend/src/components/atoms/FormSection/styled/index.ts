// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

// Small uppercase section label, as in the Stockholm settings.
export const Heading = styled.h3<{ $spaced?: boolean }>`
  margin: ${({ $spaced }) => ($spaced ? '22px' : 0)} 0 0;
  padding: 0 0 8px;
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.5px;
  text-transform: uppercase;
  color: var(--ink-3);
`;
