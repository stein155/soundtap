// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled from 'styled-components';

export const Form = styled.form`
  display: grid;
  gap: 16px;
`;

// Refresh sits under the network list at the right, as in Stockholm.
export const Refresh = styled.div`
  justify-self: end;
`;

export const Searching = styled.p`
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 0;
  padding: 15px 16px;
  color: var(--ink-2);
`;

export const NoNetworks = styled.div`
  display: grid;
  gap: 6px;
  padding: 4px;
`;

export const NoNetworksTitle = styled.h3`
  margin: 0;
  font-size: 16px;
  font-weight: 600;
`;
