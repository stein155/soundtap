// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

export const Chevron = styled.span<{ $open: boolean }>`
  display: grid;
  flex-shrink: 0;
  color: var(--ink-3);
  transform: rotate(${({ $open }) => ($open ? -90 : 90)}deg);
  transition: transform 180ms ease;
`;
