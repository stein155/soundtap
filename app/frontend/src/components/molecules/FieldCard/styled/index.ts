// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';
import { Card as GroupCard } from '../../SetMenu/styled';

// The same straight group as the settings rows.
export const Card = styled(GroupCard)<{ $spaced?: boolean }>`
  margin-top: ${({ $spaced }) => ($spaced ? 8 : 0)}px;
`;
