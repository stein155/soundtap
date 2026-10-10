// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

// The broker fields, faded and inert while the bridge is off.
export const Broker = styled.fieldset<{ $off: boolean }>`
  min-width: 0;
  margin: 0;
  padding: 0;
  border: 0;
  opacity: ${({ $off }) => ($off ? 0.45 : 1)};
  transition: opacity 180ms ease;
`;

export const Link = styled.span`
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  color: var(--ink-2);
`;

export const Dot = styled.span<{ $connected: boolean }>`
  width: 8px;
  height: 8px;
  flex-shrink: 0;
  border-radius: 999px;
  background: ${({ $connected }) => ($connected ? '#2f9e5b' : 'var(--ink-3)')};
`;
