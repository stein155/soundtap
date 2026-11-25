// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';
import { Card as GroupCard } from '../../SetMenu/styled';

// The same straight group as the settings rows.
export const Card = styled(GroupCard)<{ $spaced?: boolean | undefined }>`
  margin-top: ${({ $spaced }) => ($spaced === true ? 8 : 0)}px;
`;
